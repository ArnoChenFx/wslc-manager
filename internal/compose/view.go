package compose

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// 本文件定义界面使用的「视图类型」，以及从 compose-go 模型到它们的投影。
//
// 为什么不直接把 types.Project 交给界面：界面只关心展示用的少数字段，
// 而且需要稳定的、可以直接放进测试里手写的小结构体；
// compose-go 的类型字段上百个，随版本变化，不适合穿透到视图层。

// ProjectConfig 是界面展示用的项目配置（compose 解析结果的精简投影）。
type ProjectConfig struct {
	// Name 是项目名。
	Name string
	// Directory 是项目根目录的绝对路径。
	Directory string
	// ComposeFiles 是实际参与合并的 compose 文件列表。
	ComposeFiles []string
	// EnvFile 是显式指定的 .env 文件，没有为 nil。
	EnvFile *string
	// Services 是服务名 → 服务定义。
	Services map[string]ServiceConfig
	// Warnings 是加载时发现的、wslc 不支持而被忽略的设置。
	Warnings []string
}

// ServiceConfig 是单个服务的展示用配置。
type ServiceConfig struct {
	Name       string
	Image      string
	Build      *BuildConfig
	Command    any
	Entrypoint any
	Container  string
	Env        map[string]*string
	Ports      []PortMapping
	Volumes    []VolumeMapping
	Networks   []string
	DependsOn  []string
	Labels     map[string]string
	Restart    *string
	Profiles   []string
	Replicas   int
	Health     *HealthcheckConfig
	User       *string
	WorkingDir *string
}

// BuildConfig 是构建配置。
type BuildConfig struct {
	Context    string
	Dockerfile string
}

// PortMapping 是端口映射。
type PortMapping struct {
	Target    int
	Published string
	Protocol  string
}

// String 返回 "host:container/proto" 形式的可读文本。
func (p PortMapping) String() string {
	proto := p.Protocol
	if proto == "" {
		proto = "tcp"
	}
	if p.Published == "" || p.Published == "none" {
		return fmt.Sprintf("%d/%s", p.Target, proto)
	}
	return fmt.Sprintf("%s:%d/%s", p.Published, p.Target, proto)
}

// VolumeMapping 是挂载。
type VolumeMapping struct {
	Type     string
	Source   string
	Target   string
	ReadOnly bool
}

// String 返回 "source:target" 形式的可读文本。
func (v VolumeMapping) String() string {
	src := v.Source
	if src == "" {
		src = "anonymous"
	}
	s := src + ":" + v.Target
	if v.ReadOnly {
		s += ":ro"
	}
	return s
}

// HealthcheckConfig 是健康检查配置。
type HealthcheckConfig struct {
	Test        []string
	Interval    string
	Timeout     string
	Retries     int
	StartPeriod string
	Disable     bool
}

// Project 是界面中使用的一个 compose 项目。
type Project struct {
	// Dir 是项目目录（包含 compose 文件的目录）。
	Dir string
	// Name 是项目名。
	Name string
	// Config 是解析后的配置。
	Config ProjectConfig
	// Err 非空表示这个项目加载失败（目录不存在、YAML 语法错误等），
	// 此时 Config 为空，界面会展示错误并允许用户移除它。
	Err string
}

// Healthy 表示项目配置加载成功。
func (p Project) Healthy() bool { return p.Err == "" }

// ServiceNames 返回排序后的服务名列表。
func (p Project) ServiceNames() []string {
	names := make([]string, 0, len(p.Config.Services))
	for n := range p.Config.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ComposeFile 返回主 compose 文件路径（第一个），找不到时返回空串。
func (p Project) ComposeFile() string {
	if len(p.Config.ComposeFiles) > 0 {
		return p.Config.ComposeFiles[0]
	}
	return ""
}

// projectView 把 compose-go 的项目投影成界面类型。
func projectView(p *types.Project, dir, envFile string, warnings []string) Project {
	cfg := ProjectConfig{
		Name:         p.Name,
		Directory:    p.WorkingDir,
		ComposeFiles: append([]string(nil), p.ComposeFiles...),
		Services:     make(map[string]ServiceConfig, len(p.Services)),
		Warnings:     warnings,
	}
	if envFile != "" {
		ef := envFile
		cfg.EnvFile = &ef
	}
	for name, s := range p.Services {
		cfg.Services[name] = serviceView(p, s)
	}
	return Project{Dir: p.WorkingDir, Name: p.Name, Config: cfg}
}

func serviceView(p *types.Project, s types.ServiceConfig) ServiceConfig {
	v := ServiceConfig{
		Name:      s.Name,
		Image:     s.Image,
		Container: s.ContainerName,
		Env:       map[string]*string{},
		Labels:    map[string]string{},
		Profiles:  append([]string(nil), s.Profiles...),
		Replicas:  s.GetScale(),
	}
	if s.Build != nil {
		v.Build = &BuildConfig{Context: s.Build.Context, Dockerfile: s.Build.Dockerfile}
	}
	if s.Command != nil {
		v.Command = []string(s.Command)
	}
	if s.Entrypoint != nil {
		v.Entrypoint = []string(s.Entrypoint)
	}
	for k, val := range s.Environment {
		v.Env[k] = val
	}
	for k, val := range s.Labels {
		v.Labels[k] = val
	}
	for _, pc := range s.Ports {
		v.Ports = append(v.Ports, PortMapping{Target: int(pc.Target), Published: publishedWithIP(pc), Protocol: pc.Protocol})
	}
	for _, m := range s.Volumes {
		src := m.Source
		if m.Type == types.VolumeTypeVolume {
			if vol, ok := p.Volumes[m.Source]; ok {
				src = vol.Name
			}
		}
		v.Volumes = append(v.Volumes, VolumeMapping{Type: m.Type, Source: src, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	for _, n := range serviceNetworks(p, s) {
		v.Networks = append(v.Networks, n.name)
	}
	v.DependsOn = sortedKeys(s.DependsOn)
	if s.Restart != "" {
		r := s.Restart
		v.Restart = &r
	}
	if s.User != "" {
		u := s.User
		v.User = &u
	}
	if s.WorkingDir != "" {
		w := s.WorkingDir
		v.WorkingDir = &w
	}
	if h := s.HealthCheck; h != nil {
		hv := &HealthcheckConfig{Test: append([]string(nil), h.Test...), Disable: h.Disable}
		if h.Interval != nil {
			hv.Interval = durationArg(time.Duration(*h.Interval))
		}
		if h.Timeout != nil {
			hv.Timeout = durationArg(time.Duration(*h.Timeout))
		}
		if h.StartPeriod != nil {
			hv.StartPeriod = durationArg(time.Duration(*h.StartPeriod))
		}
		if h.Retries != nil {
			hv.Retries = int(*h.Retries)
		}
		v.Health = hv
	}
	return v
}

// publishedWithIP 返回界面显示的宿主端口：带上宿主地址（如 127.0.0.1:8080）。
func publishedWithIP(pc types.ServicePortConfig) string {
	if pc.HostIP == "" {
		return pc.Published
	}
	host := pc.HostIP
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + pc.Published
}

// PSEntry 是 `ps` 输出的一行。
type PSEntry struct {
	ID      string
	Name    string
	Service string
	Image   string
	Command string
	Created string
	Status  string
	Ports   string
	Running bool
}
