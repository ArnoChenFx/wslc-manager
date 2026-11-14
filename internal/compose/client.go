// Package compose 封装 WSL 容器编排工具 wslc-compose。
//
// 设计取舍：
//   - 解析 compose 文件时**不自己写 YAML 解析器**，而是调用
//     `wslc-compose config` 拿到它已经处理好的「解析后配置」。
//     这样变量插值、`.env`、include、profile 等语义与真正执行 `up`
//     时完全一致，不会出现"界面显示的和服务实际跑的不一样"。
//   - 所有写操作（up/down/start/stop/...）都直接调用 wslc-compose，
//     保持与命令行完全相同的语义。
package compose

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ErrNotFound 表示找不到 wslc-compose 可执行文件。
var ErrNotFound = errors.New("找不到 wslc-compose 可执行文件")

// Client 是 wslc-compose 命令行的封装。
type Client struct {
	bin string

	mu      sync.Mutex
	timeout time.Duration
	// onRun 在每条命令结束后被调用，用于诊断日志。
	onRun func(Result)
}

// SetRunHook 设置命令执行后的回调，用于诊断日志。
func (c *Client) SetRunHook(fn func(Result)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onRun = fn
}

// callHook 在锁外触发执行回调。
func (c *Client) callHook(r Result) {
	c.mu.Lock()
	fn := c.onRun
	c.mu.Unlock()
	if fn != nil {
		fn(r)
	}
}

// New 创建客户端并定位 wslc-compose 可执行文件。
//
// 查找顺序：环境变量 WSLC_COMPOSE_BIN → PATH 中的 wslc-compose。
func New() (*Client, error) {
	bin, err := locate()
	if err != nil {
		return nil, err
	}
	return &Client{bin: bin, timeout: 5 * time.Minute}, nil
}

// NewWithBinary 用指定路径创建客户端，主要用于测试。
func NewWithBinary(bin string) *Client {
	return &Client{bin: bin, timeout: 5 * time.Minute}
}

// Binary 返回 wslc-compose 的路径。
func (c *Client) Binary() string { return c.bin }

// SetTimeout 设置单条命令的超时（up/down 可能较慢，默认 5 分钟）。
func (c *Client) SetTimeout(d time.Duration) { c.timeout = d }

func locate() (string, error) {
	if v := strings.TrimSpace(os.Getenv("WSLC_COMPOSE_BIN")); v != "" {
		if st, err := os.Stat(v); err == nil && !st.IsDir() {
			return v, nil
		}
		return "", fmt.Errorf("%w: WSLC_COMPOSE_BIN 指向的 %q 不存在", ErrNotFound, v)
	}
	if p, err := exec.LookPath("wslc-compose"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w：请先安装（例如 pipx install wslc-compose），或用 WSLC_COMPOSE_BIN 指定路径", ErrNotFound)
}

// Result 是一条 wslc-compose 命令的结果。
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Ok 表示命令是否成功。
func (r Result) Ok() bool { return r.ExitCode == 0 }

// Combined 合并使用 stdout 与 stderr，用于展示进度/日志。
func (r Result) Combined() string {
	var b strings.Builder
	b.WriteString(r.Stdout)
	if r.Stderr != "" {
		if b.Len() > 0 && !strings.HasSuffix(r.Stdout, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(r.Stderr)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Error 把失败结果转成 error。
func (r Result) Error() error {
	if r.Ok() {
		return nil
	}
	msg := strings.TrimSpace(r.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(r.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("退出码 %d", r.ExitCode)
	}
	return errors.New(msg)
}

// run 在指定目录执行 wslc-compose。
//
// dir 是 compose 项目目录；wslc-compose 会在该目录（及其父目录）里
// 自动查找 compose 文件，因此传入 -f 不是必须的。但为了让行为确定，
// 调用方会显式传入文件路径。
func (c *Client) run(ctx context.Context, dir string, args ...string) (Result, error) {
	start := time.Now()

	c.mu.Lock()

	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = dir
	hideWindow(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")

	err := cmd.Run()
	res := Result{
		Args:     args,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		res.ExitCode = -1
		if ctx.Err() != nil {
			res.Stderr = strings.TrimSpace(res.Stderr + "\n" + ctx.Err().Error())
		}
		return res, fmt.Errorf("执行 wslc-compose %s 失败: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// RunOK 执行命令并在失败时返回 error。
func (c *Client) RunOK(ctx context.Context, dir string, args ...string) (Result, error) {
	res, err := c.run(ctx, dir, args...)
	if err != nil {
		return res, err
	}
	return res, res.Error()
}

// ProjectConfig 是 `wslc-compose config` 输出的解析后配置。
//
// 只声明界面需要的字段；其余字段会被 YAML 解析器忽略。
type ProjectConfig struct {
	// Name 是项目名（来自 name: 或目录名）。
	Name string `yaml:"name"`
	// Directory 是项目根目录的绝对路径。
	Directory string `yaml:"directory"`
	// ComposeFiles 是实际参与合并的 compose 文件列表。
	ComposeFiles []string `yaml:"compose_files"`
	// EnvFile 是使用的 .env 文件（可能为 null）。
	EnvFile *string `yaml:"env_file"`
	// Services 是服务名 → 服务定义。
	Services map[string]ServiceConfig `yaml:"services"`
}

// ServiceConfig 是单个服务的解析后配置（只取界面会显示的部分）。
type ServiceConfig struct {
	Name       string             `yaml:"name"`
	Image      string             `yaml:"image"`
	Build      *BuildConfig       `yaml:"build"`
	Command    any                `yaml:"command"`
	Entrypoint any                `yaml:"entrypoint"`
	Container  string             `yaml:"container_name"`
	Env        map[string]*string `yaml:"environment"`
	Ports      []PortMapping      `yaml:"ports"`
	Volumes    []VolumeMapping    `yaml:"volumes"`
	Networks   []string           `yaml:"networks"`
	DependsOn  []string           `yaml:"depends_on"`
	Labels     map[string]string  `yaml:"labels"`
	Restart    *string            `yaml:"restart"`
	Profiles   []string           `yaml:"profiles"`
	Replicas   int                `yaml:"replicas"`
	Health     *HealthcheckConfig `yaml:"healthcheck"`
	User       *string            `yaml:"user"`
	WorkingDir *string            `yaml:"working_dir"`
}

// BuildConfig 是构建配置。
type BuildConfig struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}

// PortMapping 是端口映射。
type PortMapping struct {
	Target    int    `yaml:"target"`
	Published string `yaml:"published"`
	Protocol  string `yaml:"protocol"`
}

// String 返回 "host:container/proto" 形式的可读文本。
func (p PortMapping) String() string {
	if p.Published == "" || p.Published == "none" {
		return fmt.Sprintf("%d/%s", p.Target, proto(p.Protocol))
	}
	return fmt.Sprintf("%s:%d/%s", p.Published, p.Target, proto(p.Protocol))
}

func proto(s string) string {
	if s == "" {
		return "tcp"
	}
	return s
}

// VolumeMapping 是挂载。
type VolumeMapping struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`
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
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
	Disable     bool     `yaml:"disable"`
}

// Project 是界面中使用的一个 compose 项目。
type Project struct {
	// Dir 是项目目录（包含 compose 文件的目录）。
	Dir string
	// Name 是项目名（wslc-compose 解析出的）。
	Name string
	// Config 是解析后的完整配置。
	Config ProjectConfig
	// Err 非空表示这个项目加载失败（目录不存在、YAML 语法错误等），
	// 此时 Config 为空，界面会展示错误并允许用户移除它。
	Err string
}

// Healthy 表示项目配置加载成功。
func (p Project) Healthy() bool { return p.Err == "" }

// LoadConfig 在 dir 下执行 `wslc-compose config` 并解析结果。
//
// files 为空时让 wslc-compose 自动探测 compose 文件；否则作为
// 覆盖文件传入。extraEnv 会作为额外的环境变量传入子进程，
// 用于按项目保存的 .env 覆盖项。
func (c *Client) LoadConfig(ctx context.Context, dir string, files []string, projectName string) (ProjectConfig, error) {
	args := []string{}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	if projectName != "" {
		args = append(args, "-p", projectName)
	}
	args = append(args, "config")

	res, err := c.RunOK(ctx, dir, args...)
	if err != nil {
		return ProjectConfig{}, err
	}

	var cfg ProjectConfig
	dec := yaml.NewDecoder(strings.NewReader(res.Stdout))
	if err := dec.Decode(&cfg); err != nil {
		return ProjectConfig{}, fmt.Errorf("解析 wslc-compose config 输出失败: %w", err)
	}
	if cfg.Directory == "" {
		cfg.Directory = dir
	}
	if cfg.Name == "" {
		cfg.Name = filepath.Base(dir)
	}
	return cfg, nil
}

// LoadProject 加载项目目录并返回完整 Project。
func (c *Client) LoadProject(ctx context.Context, dir string) (Project, error) {
	cfg, err := c.LoadConfig(ctx, dir, nil, "")
	if err != nil {
		return Project{}, err
	}
	return Project{Dir: cfg.Directory, Name: cfg.Name, Config: cfg}, nil
}

// ServiceNames 返回排序后的服务名列表。
func (p Project) ServiceNames() []string {
	names := make([]string, 0, len(p.Config.Services))
	for n := range p.Config.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ComposeFile 返回主 compose 文件路径（第一个）。
//
// 找不到时返回空字符串。
func (p Project) ComposeFile() string {
	if len(p.Config.ComposeFiles) > 0 {
		return p.Config.ComposeFiles[0]
	}
	return ""
}

// Action 是可以在项目上执行的编排动作。
type Action string

const (
	ActionUp      Action = "up"
	ActionDown    Action = "down"
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionPull    Action = "pull"
	ActionBuild   Action = "build"
	ActionPS      Action = "ps"
	ActionLogs    Action = "logs"
)

// ActionOptions 控制动作的额外参数。
type ActionOptions struct {
	// Detach 用于 up：-d
	Detach bool
	// Build 用于 up：--build
	Build bool
	// ForceRecreate 用于 up：--force-recreate
	ForceRecreate bool
	// Volumes 用于 down：-v
	Volumes bool
	// Services 限定操作的服务；为空表示全部。
	Services []string
}

// RunAction 在项目目录执行一个编排动作，返回合并后的输出。
func (c *Client) RunAction(ctx context.Context, dir string, action Action, opt ActionOptions) Result {
	args := actionArgs(action, opt)
	res, err := c.run(ctx, dir, args...)
	if err != nil {
		res.Stderr = strings.TrimSpace(res.Stderr + "\n" + err.Error())
	}
	return res
}

// actionArgs 把动作与选项翻译成 wslc-compose 的参数列表。
//
// 抽成纯函数是为了让 RunAction 与 RunActionStream 共用同一套参数逻辑，
// 也便于对参数拼装单独写单元测试。
func actionArgs(action Action, opt ActionOptions) []string {
	args := []string{string(action)}
	switch action {
	case ActionUp:
		if opt.Detach {
			args = append(args, "-d")
		}
		if opt.Build {
			args = append(args, "--build")
		}
		if opt.ForceRecreate {
			args = append(args, "--force-recreate")
		}
	case ActionDown:
		if opt.Volumes {
			args = append(args, "-v")
		}
	default:
		// start/stop/restart/pull/build 没有额外开关。
	}
	return append(args, opt.Services...)
}

// RunActionStream 执行编排动作，并把每一行输出实时交给 onLine。
//
// up / build / pull 都可能跑很久，界面上必须能看到进度，
// 因此这里用流式读取而不是等命令结束。
//
// onLine 在后台 goroutine 里被调用，调用方需自行处理线程安全。
func (c *Client) RunActionStream(ctx context.Context, dir string, action Action, opt ActionOptions, onLine func(string)) (Result, error) {
	return c.runStream(ctx, dir, actionArgs(action, opt), onLine)
}

// DryRun 执行带 --dry-run 的动作，返回将要运行的 wslc 命令。
//
// 用于「预览」：在不真正改动容器的情况下告诉用户会发生什么。
func (c *Client) DryRun(ctx context.Context, dir string, action Action, opt ActionOptions) (string, error) {
	args := []string{"--dry-run", string(action)}
	switch action {
	case ActionUp:
		if opt.Detach {
			args = append(args, "-d")
		}
		if opt.Build {
			args = append(args, "--build")
		}
		if opt.ForceRecreate {
			args = append(args, "--force-recreate")
		}
	case ActionDown:
		if opt.Volumes {
			args = append(args, "-v")
		}
	}
	args = append(args, opt.Services...)

	res, err := c.RunOK(ctx, dir, args...)
	if err != nil {
		return res.Combined(), err
	}
	return res.Combined(), nil
}

// PSEntry 是 `wslc-compose ps` 表格的一行。
type PSEntry struct {
	Name    string
	Service string
	Image   string
	Status  string
	Ports   string
}

// PS 返回项目当前的容器列表表格（解析成结构化数据）。
func (c *Client) PS(ctx context.Context, dir string, all bool) ([]PSEntry, error) {
	args := []string{"ps"}
	if all {
		args = append(args, "-a")
	}
	res, err := c.RunOK(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	return ParsePSTable(res.Stdout), nil
}

// ParsePSTable 解析 `wslc-compose ps` 的输出。
//
// 输出格式（表头行可能因终端宽度变化而列宽不同）：
//
//	NAME               SERVICE  IMAGE         STATUS   PORTS
//	proj-db-1          db       postgres:16   running
func ParsePSTable(out string) []PSEntry {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return nil
	}
	var entries []PSEntry
	for _, line := range lines[1:] {
		cols := splitColumns(line)
		if len(cols) < 4 {
			continue
		}
		e := PSEntry{
			Name:    cols[0],
			Service: cols[1],
			Image:   cols[2],
			Status:  cols[3],
		}
		if len(cols) > 4 {
			e.Ports = strings.Join(cols[4:], " ")
		}
		entries = append(entries, e)
	}
	return entries
}

// splitColumns 按两个及以上空格切分表格行。
func splitColumns(line string) []string {
	var cols []string
	var cur strings.Builder
	spaces := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			cols = append(cols, s)
		}
		cur.Reset()
	}
	for _, r := range line {
		if r == ' ' || r == '\t' {
			spaces++
			if spaces == 1 {
				cur.WriteRune(r)
			}
			continue
		}
		if spaces >= 2 {
			flush()
		}
		spaces = 0
		cur.WriteRune(r)
	}
	flush()
	return cols
}

// Version 读取 wslc-compose 版本。
func (c *Client) Version(ctx context.Context) (string, error) {
	res, err := c.RunOK(ctx, "", "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// ComposeFileNames 是 wslc-compose 会自动探测的文件名（按优先级）。
var ComposeFileNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yml",
	"docker-compose.yaml",
}

// IsComposeDir 判断目录下是否有 compose 文件。
func IsComposeDir(dir string) bool {
	return FindComposeFile(dir) != ""
}

// FindComposeFile 返回目录中第一个匹配的 compose 文件名（完整路径）。
func FindComposeFile(dir string) string {
	for _, name := range ComposeFileNames {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// runStream 在指定目录执行 wslc-compose，并把每一行输出实时交给 onLine。
//
// 与 wslc.Client.RunStream 的思路一致：把 stdout 与 stderr 合并到同一个
// 管道，读一行转一行。wslc-compose 的进度信息主要写在 stdout，
// 但错误在 stderr，两者都转发才不会漏。
func (c *Client) runStream(ctx context.Context, dir string, args []string, onLine func(string)) (Result, error) {
	start := time.Now()

	c.mu.Lock()

	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = dir
	hideWindow(cmd)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		c.mu.Unlock()
		res := Result{Args: args, ExitCode: -1, Duration: time.Since(start)}
		c.callHook(res)
		return res, fmt.Errorf("启动 wslc-compose 失败: %w", err)
	}

	var all strings.Builder
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			all.WriteString(line)
			all.WriteString("\n")
			if onLine != nil {
				onLine(line)
			}
		}
		_ = pr.Close()
	}()

	waitErr := cmd.Wait()
	_ = pw.Close()
	<-readDone

	res := Result{
		Args:     args,
		Stdout:   all.String(),
		ExitCode: 0,
		Duration: time.Since(start),
	}
	c.mu.Unlock()

	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			res.ExitCode = ee.ExitCode()
			c.callHook(res)
			return res, nil
		}
		res.ExitCode = -1
		if ctx.Err() != nil {
			res.Stderr = ctx.Err().Error()
		}
		c.callHook(res)
		return res, fmt.Errorf("执行 wslc-compose %s 失败: %w", strings.Join(args, " "), waitErr)
	}
	c.callHook(res)
	return res, nil
}
