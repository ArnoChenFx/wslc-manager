// Package envsetup 检测本应用依赖的外部环境，并引导安装 / 更新它。
//
// 现在只剩一个外部依赖：wslc——WSL 自带的容器 CLI，随 WSL 发布，位于
// %ProgramFiles%\WSL\wslc.exe。它不是独立安装的，所以本包**不**自己下载二进制，
// 而是引导用户用 winget 安装或更新 WSL。
//
// Compose 编排内置在 internal/compose 里，不需要检测也不需要安装。
//
// 设计原则：检测是只读的、快的；安装是显式的、用户点的；所有会改系统的命令
// 都返回命令行，由上层决定是否执行，便于测试与展示。
package envsetup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Status 是一次环境体检的结果。
type Status struct {
	// WSLCPath 是找到的 wslc 可执行文件路径，空表示没找到。
	WSLCPath string
	// WSLVersion 是 WSL 的版本号（来自 wsl.exe --version），取不到为空。
	WSLVersion string
	// Winget 是 winget 可执行文件路径，空表示没有 winget。
	Winget string
	// CheckedAt 是本次检测时间。
	CheckedAt time.Time
}

// WSLCOK 表示 wslc 可用。
func (s Status) WSLCOK() bool { return s.WSLCPath != "" }

// CanInstallWSL 表示本机有 winget，能引导安装/更新 WSL。
func (s Status) CanInstallWSL() bool { return s.Winget != "" }

// Detect 体检当前环境。它只读，不修改任何东西，也不访问网络，
// 通常在几十毫秒内完成（耗时主要在 `wsl.exe --version`）。
func Detect(ctx context.Context) Status {
	st := Status{CheckedAt: time.Now()}
	st.WSLCPath = findWSLCPath()
	if st.WSLCPath != "" {
		st.WSLVersion = wslVersion(ctx)
	}
	st.Winget = findWinget()
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
