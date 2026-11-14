// Package envsetup 检测并引导安装本应用依赖的两个外部工具：
//
//   - wslc：WSL 自带的容器 CLI（随 WSL 发布，位于 %ProgramFiles%\WSL\wslc.exe）。
//     它不是独立安装的，因此本包**不**自己下载二进制，而是引导用户用
//     winget 安装或更新 WSL。
//   - wslc-compose：PyPI 上的第三方 Python 包，负责 Compose 语义。
//     本包用 uv / pipx 安装与升级，不把它内置进来——重写一份 Go 实现
//     会和上游行为产生差异，反而更难维护。
//
// 设计原则：检测是只读的、快的；安装是显式的、用户点的；所有会改系统的命令
// 都返回命令行，由上层决定是否执行，便于测试与展示。
package envsetup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Composer 是 wslc-compose 在 PyPI 上的包名。
const Composer = "wslc-compose"

// PyPIURL 是查询最新版本的接口地址。
const PyPIURL = "https://pypi.org/pypi/" + Composer + "/json"

// Status 是一次环境体检的结果。
type Status struct {
	// WSLCPath 是找到的 wslc 可执行文件路径，空表示没找到。
	WSLCPath string
	// WSLVersion 是 WSL 的版本号（来自 wsl.exe --version），取不到为空。
	WSLVersion string
	// ComposePath 是找到的 wslc-compose 路径，空表示没找到。
	ComposePath string
	// ComposeVersion 是本机 wslc-compose 的版本，例如 "0.6.0"。
	ComposeVersion string
	// Latest 是 PyPI 上的最新版本，取不到为空。
	Latest string
	// Upgrader 是本机可用的安装器（"uv" / "pipx" / ""）。
	Upgrader string
	// Winget 是 winget 可执行文件路径，空表示没有 winget。
	Winget string
	// CheckedAt 是本次检测时间。
	CheckedAt time.Time
	// Err 记录检测过程中的非致命错误（例如 PyPI 不可达）。
	Err string
}

// WSLCOK 表示 wslc 可用。
func (s Status) WSLCOK() bool { return s.WSLCPath != "" }

// ComposeOK 表示 wslc-compose 可用。
func (s Status) ComposeOK() bool { return s.ComposePath != "" }

// ComposeOutdated 表示本机 wslc-compose 落后于 PyPI 最新版。
//
// 版本号只有一边拿得到时返回 false：宁可不说「有更新」。
func (s Status) ComposeOutdated() bool {
	if s.ComposeVersion == "" || s.Latest == "" {
		return false
	}
	return compareVersions(s.ComposeVersion, s.Latest) < 0
}

// CanInstallCompose 表示本机有可用的安装器，能一键装 wslc-compose。
func (s Status) CanInstallCompose() bool { return s.Upgrader != "" }

// CanInstallWSL 表示本机有 winget，能引导安装/更新 WSL。
func (s Status) CanInstallWSL() bool { return s.Winget != "" }

// Detect 体检当前环境。它只读，不修改任何东西。
//
// 网络查询（PyPI）失败不算错误：结果里 Latest 为空，Err 带说明，
// 界面仍然能显示本机版本。
func Detect(ctx context.Context) Status {
	st := Status{CheckedAt: time.Now()}

	st.WSLCPath = findWSLCPath()
	if st.WSLCPath != "" {
		st.WSLVersion = wslVersion(ctx)
	}

	// wslc-compose 优先用 PATH，其次查 uv / pipx 的安装目录。
	if p, err := exec.LookPath(Composer); err == nil {
		st.ComposePath = p
	} else {
		st.ComposePath = findComposeInToolDirs()
	}
	if st.ComposePath != "" {
		st.ComposeVersion = composeVersion(ctx, st.ComposePath)
	}

	st.Upgrader = findUpgrader()
	st.Winget = findWinget()

	if v, err := latestOnPyPI(ctx); err != nil {
		st.Err = err.Error()
	} else {
		st.Latest = v
	}
	return st
}

// findWSLCPath 按优先级找 wslc.exe。
//
// 顺序：PATH → %ProgramFiles%\WSL\wslc.exe（WSL 的标准安装位置）。
func findWSLCPath() string {
	name := "wslc"
	if runtime.GOOS == "windows" {
		name = "wslc.exe"
	}
	if p, err := exec.LookPath("wslc"); err == nil {
		return p
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, key := range []string{"ProgramFiles", "ProgramW6432"} {
		if dir := os.Getenv(key); dir != "" {
			p := filepath.Join(dir, "WSL", name)
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

// wslVersion 读取 WSL 的版本号。
//
// `wsl --version` 的输出形如：
//
//	WSL 版本: 3.0.5.0
//	内核版本: 6.6.87.2-1
//
// 这里只取第一行的版本号；解析不出来就返回空串，不报错。
func wslVersion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "wsl.exe", "--version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// wsl.exe 输出 UTF-16LE（每字符两字节，低字节在前，含 NUL）。
	// 直接按 UTF-8 解析会得到 "W\x00S\x00L\x00..." 这样的垃圾，
	// 后续按行/冒号解析永远匹配不上，版本号就成了空。
	for _, line := range strings.Split(decodeUTF16LE(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(strings.ToLower(line), "wsl") {
			continue
		}
		// 冒号（中英文）之后的部分是版本号。
		if i := strings.IndexAny(line, ":："); i >= 0 {
			if v := strings.TrimSpace(line[i+len(":"):]); looksLikeVersion(v) {
				return v
			}
		}
	}
	return ""
}

// looksLikeVersion 判断字符串是否是「数字.数字」开头的版本号。
func looksLikeVersion(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.SplitN(s, ".", 2)
	if len(parts) < 2 {
		return false
	}
	_, err := strconv.Atoi(parts[0])
	return err == nil
}

// composeVersion 运行 `wslc-compose --version`，取出其中的版本号。
func composeVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return firstVersion(string(out))
}

// firstVersion 从文本里找出第一个版本号（形如 1.2.3）。
func firstVersion(s string) string {
	for _, field := range strings.Fields(s) {
		field = strings.Trim(field, "vV,.;()[]")
		parts := strings.Split(field, ".")
		if len(parts) < 2 {
			continue
		}
		ok := true
		for _, p := range parts {
			if p == "" {
				ok = false
				break
			}
			if _, err := strconv.Atoi(p); err != nil {
				ok = false
				break
			}
		}
		if ok {
			return field
		}
	}
	return ""
}

// findComposeInToolDirs 在 uv / pipx 的工具目录里找 wslc-compose。
//
// 这两个工具会把可执行文件放进自己的 bin 目录，可能不在 PATH 上。
func findComposeInToolDirs() string {
	names := []string{Composer, Composer + ".exe"}
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "AppData", "Roaming", "Python", "Scripts"),
			filepath.Join(home, ".local", "pipx", "venvs", Composer, "Scripts"),
		)
	}
	if xdg := os.Getenv("XDG_BIN_HOME"); xdg != "" {
		dirs = append(dirs, xdg)
	}
	for _, dir := range dirs {
		for _, name := range names {
			p := filepath.Join(dir, name)
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

// findUpgrader 返回可用的安装器名（"uv" 优先，"pipx" 次之），都没有返回空。
func findUpgrader() string {
	if _, err := exec.LookPath("uv"); err == nil {
		return "uv"
	}
	if _, err := exec.LookPath("pipx"); err == nil {
		return "pipx"
	}
	return ""
}

// findWinget 定位 winget。
func findWinget() string {
	if p, err := exec.LookPath("winget"); err == nil {
		return p
	}
	// winget 在 WindowsApps 下，通常是 WindowsApps 别名，PATH 里不一定有。
	if runtime.GOOS != "windows" {
		return ""
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		p := filepath.Join(local, "Microsoft", "WindowsApps", "winget.exe")
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// latestOnPyPI 查询 PyPI 上的最新版本号。
func latestOnPyPI(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PyPIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询 PyPI 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("查询 PyPI 失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}

	var payload struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("解析 PyPI 响应失败: %w", err)
	}
	return payload.Info.Version, nil
}

// InstallComposeArgs 返回用本机安装器安装 wslc-compose 的命令。
//
// 用固定版本（latest 为空时省略版本约束），以便重复执行是幂等的。
func (s Status) InstallComposeArgs() (bin string, args []string) {
	switch s.Upgrader {
	case "uv":
		spec := Composer
		if s.Latest != "" {
			spec = Composer + "==" + s.Latest
		}
		return "uv", []string{"tool", "install", spec}
	case "pipx":
		spec := Composer
		if s.Latest != "" {
			spec = Composer + "==" + s.Latest
		}
		return "pipx", []string{"install", spec}
	}
	return "", nil
}

// UpgradeComposeArgs 返回升级 wslc-compose 的命令。
func (s Status) UpgradeComposeArgs() (bin string, args []string) {
	switch s.Upgrader {
	case "uv":
		return "uv", []string{"tool", "upgrade", Composer}
	case "pipx":
		return "pipx", []string{"upgrade", Composer}
	}
	return "", nil
}

// InstallWSLArgs 返回用 winget 安装 WSL 的命令。
//
// 用 --accept-* 默认值，避免安装过程卡在交互式协议确认上；
// 这不会替用户跳过安全决定，安装包本身的来源仍是官方源。
func (s Status) InstallWSLArgs() (bin string, args []string) {
	if s.Winget == "" {
		return "", nil
	}
	return s.Winget, []string{
		"install", "--id", "Microsoft.WSL", "--exact", "--silent",
		"--accept-package-agreements", "--accept-source-agreements",
	}
}

// UpgradeWSLArgs 返回用 winget 更新 WSL 的命令。
func (s Status) UpgradeWSLArgs() (bin string, args []string) {
	if s.Winget == "" {
		return "", nil
	}
	return s.Winget, []string{
		"upgrade", "--id", "Microsoft.WSL", "--exact", "--silent",
		"--accept-package-agreements", "--accept-source-agreements",
	}
}

// compareVersions 比较两个点分版本号，返回 -1 / 0 / 1。
//
// 只比较数字段，非数字段按 0 处理：PyPI 上的版本可能有预发布后缀（0.7.0rc1），
// 这种情况会被当成 0.7.0，比 0.6.0 大，方向是对的。
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av = leadingInt(as[i])
		}
		if i < len(bs) {
			bv = leadingInt(bs[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// leadingInt 取出字符串开头的数字，没有则返回 0。
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0
	}
	return n
}

// fileExists 判断路径是否为存在的文件。
func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// decodeUTF16LE 把 wsl.exe 输出的 UTF-16LE 字节流转成 UTF-8 文本。
//
// Windows 上的 wsl.exe 用 UTF-16LE 输出，直接当 UTF-8 读会得到
// 一堆夹杂 NUL 的乱码，后续按行解析永远匹配不上。
// 对非 UTF-16LE 的输入（例如个别版本输出 UTF-8）保持原样返回。
func decodeUTF16LE(b []byte) string {
	if len(b) < 2 {
		return string(b)
	}
	// 判定依据：偶数长度，且奇数位大量为 0（ASCII 区间字符的高字节）。
	nul := 0
	total := 0
	for i := 1; i < len(b); i += 2 {
		total++
		if b[i] == 0 {
			nul++
		}
	}
	if total == 0 || nul*2 < total {
		return string(b)
	}

	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u))
}
