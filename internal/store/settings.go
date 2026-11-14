// Package store 负责持久化应用设置与「已知项目」列表。
//
// 设置文件放在 %APPDATA%\wslc-manager\settings.json，
// 用简单的 JSON 而不是注册表，方便用户手动查看和修。
package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Settings 是应用的全部持久化设置。
type Settings struct {
	// Projects 是用户添加/已发现的项目目录（绝对路径）。
	Projects []string `json:"projects"`
	// ScanRoots 是自动扫描 compose 项目的根目录列表。
	ScanRoots []string `json:"scanRoots"`
	// ScanDepth 是自动扫描的最大递归深度（0 表示不递归）。
	ScanDepth int `json:"scanDepth"`
	// PollSeconds 是自动刷新间隔（秒），0 表示关闭自动刷新。
	PollSeconds int `json:"pollSeconds"`
	// HideStopped 控制容器列表是否默认隐藏已停止的容器。
	HideStopped bool `json:"hideStopped"`
	// SidebarWidth 记住侧边栏宽度。
	SidebarWidth float32 `json:"sidebarWidth"`
	// Accent 是强调色标识（magenta / violet / blue / teal / amber / green）。
	// 空表示用出厂默认。
	Accent string `json:"accent"`
	// Language 是界面语言（BCP 47 标签，如 zh-Hans / en）。
	// 空表示还没选过，跟随系统。
	Language string `json:"language"`
	// WindowWidth / WindowHeight 记住上次窗口尺寸。
	WindowWidth  float64 `json:"windowWidth"`
	WindowHeight float64 `json:"windowHeight"`
}

// DefaultSettings 返回初始设置。
func DefaultSettings() Settings {
	home, _ := os.UserHomeDir()
	return Settings{
		Projects:     []string{},
		ScanRoots:    defaultScanRoots(home),
		ScanDepth:    3,
		PollSeconds:  5,
		HideStopped:  false,
		SidebarWidth: 220,
	}
}

// defaultScanRoots 给出一些常见的代码/项目目录作为自动扫描起点。
//
// 只加入真实存在的目录，避免设置里出现一堆无效路径。
func defaultScanRoots(home string) []string {
	candidates := []string{
		filepath.Join(home, "Projects"),
		filepath.Join(home, "projects"),
		filepath.Join(home, "code"),
		filepath.Join(home, "Code"),
		filepath.Join(home, "source", "repos"),
		filepath.Join(home, "dev"),
		filepath.Join(home, "workspace"),
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

// Store 是设置文件的读写封装，可安全并发使用。
type Store struct {
	path string

	mu sync.Mutex
}

// New 创建 Store，path 为空时使用默认位置。
func New(path string) *Store {
	if path == "" {
		path = DefaultPath()
	}
	return &Store{path: path}
}

// DefaultPath 返回默认设置文件路径。
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir, _ = os.UserHomeDir()
		dir = filepath.Join(dir, ".config")
	}
	return filepath.Join(dir, "wslc-manager", "settings.json")
}

// Path 返回设置文件路径。
func (s *Store) Path() string { return s.path }

// Load 读取设置。文件不存在时返回默认设置而不是错误。
func (s *Store) Load() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	def := DefaultSettings()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return def, nil
		}
		return def, err
	}
	// 以默认值打底，文件里缺少的字段保持默认。
	got := def
	if err := json.Unmarshal(data, &got); err != nil {
		return def, err
	}
	// 清理无效路径并去重。
	got.Projects = cleanPaths(got.Projects)
	got.ScanRoots = cleanPaths(got.ScanRoots)
	if got.ScanDepth <= 0 {
		got.ScanDepth = 3
	}
	if got.SidebarWidth <= 0 {
		got.SidebarWidth = 220
	}
	return got, nil
}

// Save 写入设置（自动创建目录，原子替换文件）。
func (s *Store) Save(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	v.Projects = cleanPaths(v.Projects)
	v.ScanRoots = cleanPaths(v.ScanRoots)

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// cleanPaths 去掉空串、去重，并尽量转成绝对路径。
func cleanPaths(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		// Windows 文件系统忽略大小写，因此去重键统一转小写，
		// 避免同一目录因大小写不同而被当成两个项目。
		key := strings.ToLower(filepath.ToSlash(filepath.Clean(abs)))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, abs)
	}
	return out
}
