package wslc

import (
	"strconv"
	"strings"
)

// Container 是一个 WSL 容器，对应 `wslc list -a --format json` 的一行。
//
// 字段名与 wslc 输出保持一致（Go 侧用习惯命名，JSON tag 保留原始键名）。
type Container struct {
	ID         string    `json:"ID"`
	Names      string    `json:"Names"`
	Image      string    `json:"Image"`
	Command    string    `json:"Command"`
	CreatedAt  string    `json:"CreatedAt"`
	RunningFor string    `json:"RunningFor"`
	Status     string    `json:"Status"`
	State      string    `json:"State"`
	Ports      string    `json:"Ports"`
	Networks   string    `json:"Networks"`
	Mounts     string    `json:"Mounts"`
	LocalVol   string    `json:"LocalVolumes"`
	Size       string    `json:"Size"`
	Health     string    `json:"HealthStatus"`
	Platform   *Platform `json:"Platform,omitempty"`
	// Labels 是逗号分隔的 key=value 串，wslc 不把它拆成对象。
	Labels string `json:"Labels"`
}

// Platform 是容器所属镜像的平台信息。
type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

// IsRunning 判断容器是否处于运行状态。
func (c Container) IsRunning() bool { return strings.EqualFold(c.State, "running") }

// IsPaused 判断容器是否被暂停。
func (c Container) IsPaused() bool { return strings.EqualFold(c.State, "paused") }

// StateLabel 返回给用户看的状态文案（中文）。
func (c Container) StateLabel() string {
	s := strings.ToLower(c.State)
	switch s {
	case "running":
		if c.Health == "unhealthy" {
			return "运行中（不健康）"
		}
		if c.Health == "healthy" {
			return "运行中（健康）"
		}
		return "运行中"
	case "exited":
		return "已停止"
	case "created":
		return "已创建"
	case "paused":
		return "已暂停"
	case "restarting":
		return "重启中"
	case "":
		return "未知"
	default:
		return c.State
	}
}

// Label 读取容器标签。wslc 把标签编码成 "k1=v1,k2=v2" 的单个字符串，
// 但值本身可能包含逗号（例如 compose 的 metadata JSON），所以这里做
// 简单的贪婪匹配：只在遇到下一个 "token=" 形态时才分割。
func (c Container) Label(key string) string {
	return ParseLabels(c.Labels)[key]
}

// ParseLabels 解析 wslc 的标签串为 map。
//
// 规则：以 "=" 左侧形如标识符（字母、数字、点、下划线、斜杠、连字符）的位置
// 作为新的键起点。这样 "com.foo.a=1,com.foo.meta={...x,y...}" 能正确切分。
func ParseLabels(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	type kv struct{ k, v string }
	var items []kv

	start := 0
	// 从第二个字符开始找潜在的键起点
	for i := 1; i < len(s); i++ {
		if s[i] != '=' {
			continue
		}
		// 向左回溯找这个 = 所属的键起点
		j := i - 1
		for j >= start && isLabelKeyChar(s[j]) {
			j--
		}
		keyStart := j + 1
		// 键起点必须紧跟在逗号之后（或就是开头）
		if keyStart == start || (keyStart > start && s[keyStart-1] == ',') {
			if keyStart > start {
				end := keyStart - 1 // 前一项结束在逗号
				items = append(items, splitKV(s[start:end]))
			}
			start = keyStart
		}
	}
	if start < len(s) {
		items = append(items, splitKV(s[start:]))
	}

	for _, it := range items {
		if it.k != "" {
			out[it.k] = it.v
		}
	}
	return out
}

func splitKV(s string) struct{ k, v string } {
	s = strings.TrimSpace(s)
	k, v, found := strings.Cut(s, "=")
	if !found {
		return struct{ k, v string }{k: s}
	}
	return struct{ k, v string }{k: strings.TrimSpace(k), v: v}
}

func isLabelKeyChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '.' || b == '_' || b == '-' || b == '/':
		return true
	}
	return false
}

// Image 是一个 WSL 镜像，对应 `wslc images --format json` 的一行。
type Image struct {
	ID           string `json:"ID"`
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	Digest       string `json:"Digest"`
	CreatedAt    string `json:"CreatedAt"`
	CreatedSince string `json:"CreatedSince"`
	Size         string `json:"Size"`
	SharedSize   string `json:"SharedSize"`
	UniqueSize   string `json:"UniqueSize"`
	Containers   string `json:"Containers"`
}

// Ref 返回 repository:tag 形式的完整引用；悬空镜像返回 image ID。
func (i Image) Ref() string {
	repo, tag := i.Repository, i.Tag
	if repo == "" || repo == "<none>" {
		return i.ID
	}
	if tag == "" || tag == "<none>" {
		return repo
	}
	return repo + ":" + tag
}

// Dangling 判断是否为悬空（untagged）镜像。
func (i Image) Dangling() bool {
	return i.Repository == "<none>" || i.Repository == ""
}

// Volume 是一个命名卷，对应 `wslc volume list --format json` 的一行。
type Volume struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
	Scope      string `json:"Scope"`
	Labels     string `json:"Labels"`
	Size       string `json:"Size"`
	Status     string `json:"Status"`
	Group      string `json:"Group"`
	Links      string `json:"Links"`
}

// Network 是一个网络，对应 `wslc network list --format json` 的一行。
type Network struct {
	ID        string `json:"ID"`
	Name      string `json:"Name"`
	Driver    string `json:"Driver"`
	Scope     string `json:"Scope"`
	CreatedAt string `json:"CreatedAt"`
	Internal  string `json:"Internal"`
	IPv4      string `json:"IPv4"`
	IPv6      string `json:"IPv6"`
	Labels    string `json:"Labels"`
}

// IsBuiltin 判断是否为 wslc 自带的内置网络（bridge/host/none）。
func (n Network) IsBuiltin() bool {
	switch n.Name {
	case "bridge", "host", "none":
		return true
	}
	return false
}

// Session 是 wslc 会话，对应 `wslc system session list` 的一行。
//
// 会话按 Windows 用户 + 提权状态区分：普通终端和管理员终端看到的是
// 两套彼此独立的容器/镜像/网络/卷。
type Session struct {
	// ID 是会话编号。
	ID string
	// PID 是创建该会话的进程 ID。
	PID string
	// DisplayName 是会话显示名，例如 wslc-cli-Alice。
	DisplayName string
}

// Info 是 `wslc info` 的解析结果。
//
// wslc info 输出的是人类可读文本而非 JSON，这里按「键: 值」逐行提取
// 需要的字段，其余内容保留在 Raw 里。
type Info struct {
	WSLVersion    string
	KernelVersion string
	Direct3D      string
	DXCore        string
	WindowsVer    string
	SettingsFile  string
	SessionMgr    string
	SessionCount  string
	Raw           string
}

// ParseInfo 从 `wslc info` 的原始输出里提取字段。
func ParseInfo(raw string) Info {
	info := Info{Raw: raw}
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "WSL 版本", "WSL Version":
			info.WSLVersion = v
		case "内核版本", "Kernel Version":
			info.KernelVersion = v
		case "Direct3D 版本", "Direct3D Version":
			info.Direct3D = v
		case "DXCore 版本", "DXCore Version":
			info.DXCore = v
		case "Windows 版本", "Windows Version":
			info.WindowsVer = v
		case "设置文件", "Settings File":
			info.SettingsFile = v
		case "会话管理器版本", "Session Manager Version":
			info.SessionMgr = v
		case "会话", "Sessions":
			info.SessionCount = v
		}
	}
	return info
}

// itoa 是一个小工具，避免在多个文件里重复引入 strconv。
func itoa(n int) string { return strconv.Itoa(n) }

// atoiOr 把字符串转成 int，失败时返回默认值。
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}
