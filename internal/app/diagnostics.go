package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/arno/wslc-manager/locales"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// 诊断报告：把「出问题时我需要知道的一切」打包成一份文件。
//
// 目的很具体：用户反馈「某页布局不对 / 点了没反应」时，
// 我一个人看不到他的屏幕，只能来回猜。有了这份报告，
// 布局坐标、控件树、最近命令与错误、环境版本一次性拿到，
// 定位一次就够，不用反复追问。
//
// 报告是 Markdown（末尾附一段 JSON 供程序解析），因为：
//   - 人可以直接读，贴到 issue 里也好看；
//   - 结构化部分用代码块包住，机器照样能提取。

// diagUINode 是 UI 树里的一个元素（来自 MyGo 的 automation 接口）。
type diagUINode struct {
	ID     string `json:"id"`
	Parent string `json:"parent,omitempty"`
	Role   string `json:"role"`
	Label  string `json:"label,omitempty"`
	// Key 是应用给元素的 key，类型不固定（字符串、数字等），
	// 因此用 any，渲染时再统一成文本。
	Key    any            `json:"key,omitempty"`
	Bounds *diagUIRect    `json:"bounds,omitempty"`
	States []string       `json:"states,omitempty"`
	Extra  map[string]any `json:"-"`
}

// diagUIRect 是元素在窗口里的矩形（DIP）。
type diagUIRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// diagUITree 是 automation 协议里 tree 方法的返回。
type diagUITree struct {
	Window int          `json:"window"`
	Nodes  []diagUINode `json:"nodes"`
}

// DiagOptions 控制报告内容。
type DiagOptions struct {
	// IncludeUI 为真时尝试通过 automation socket 抓取界面元素树。
	IncludeUI bool
	// SocketPath 是 automation socket 路径（空则从环境变量推断）。
	SocketPath string
	// IncludeLog 为真时包含运行日志。
	IncludeLog bool
	// LogTail 限制日志行数（0 表示全部）。
	LogTail int
}

// DefaultDiagOptions 返回默认的报告内容设置：全都包含。
func DefaultDiagOptions() DiagOptions {
	return DiagOptions{
		IncludeUI:  true,
		SocketPath: os.Getenv("MYGO_AUTOMATION"),
		IncludeLog: true,
		LogTail:    0,
	}
}

// BuildDiagnostic 生成诊断报告正文。
//
// 它只在主线程调用（读取页面状态），且会做一次文件与子进程 IO，
// 因此调用方通常把它放到后台协程里，只把结果送回主线程。
func (a *App) BuildDiagnostic(opt DiagOptions) (string, error) {
	var b strings.Builder

	now := time.Now()
	fmt.Fprintf(&b, "# WSLC Manager 诊断报告\n\n")
	fmt.Fprintf(&b, "生成时间: %s\n\n", now.Format("2006-01-02 15:04:05 -0700"))

	// ---- 环境 ----
	b.WriteString("## 环境\n\n")
	kv := func(k, v string) {
		if strings.TrimSpace(v) == "" {
			v = "—"
		}
		fmt.Fprintf(&b, "- **%s**: %s\n", k, v)
	}
	kv(locales.SystemVersion(), appVersion)
	kv(locales.ComposeBuild(), runtime.GOOS+"/"+runtime.GOARCH+" "+runtime.Version())
	kv("wslc", a.wslcVer)
	kv("wslc-compose", a.composeVer)
	if a.wslc != nil {
		kv(locales.SystemWslcPath(), a.wslc.Binary())
	}
	if a.compose != nil {
		kv(locales.SystemComposePath(), a.compose.Binary())
	}
	kv(locales.SystemWslVersion(), a.info.WSLVersion)
	kv(locales.SystemKernelVersion(), a.info.KernelVersion)
	kv(locales.SystemWindowsVersion(), a.info.WindowsVer)
	kv(locales.SystemSessionManager(), a.info.SessionMgr)
	kv(locales.SystemWslcSettings(), a.info.SettingsFile)
	if a.wslcErr != "" {
		kv("wslc 错误", a.wslcErr)
	}
	if a.composeErr != "" {
		kv("wslc-compose 错误", a.composeErr)
	}
	b.WriteString("\n")

	// ---- 会话 ----
	if len(a.sessions) > 0 {
		b.WriteString("## 会话\n\n")
		fmt.Fprintf(&b, "| ID | PID | 显示名称 |\n|---|---|---|\n")
		for _, s := range a.sessions {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", s.ID, s.PID, s.DisplayName)
		}
		b.WriteString("\n")
	}

	// ---- 数据概览 ----
	b.WriteString("## 数据概览\n\n")
	running := 0
	for _, c := range a.containers {
		if c.IsRunning() {
			running++
		}
	}
	fmt.Fprintf(&b, "| 资源 | 数量 |\n|---|---|\n")
	fmt.Fprintf(&b, "| 容器 | %d（运行中 %d） |\n", len(a.containers), running)
	fmt.Fprintf(&b, "| 镜像 | %d |\n", len(a.images))
	fmt.Fprintf(&b, "| 卷 | %d |\n", len(a.volumes))
	fmt.Fprintf(&b, "| 网络 | %d |\n", len(a.networks))
	fmt.Fprintf(&b, "| Compose 项目 | %d |\n", len(a.projects))
	fmt.Fprintf(&b, "\n最近刷新: %s\n\n", fmtTime(a.lastRefresh))

	// ---- 当前界面状态 ----
	b.WriteString("## 当前界面状态\n\n")
	fmt.Fprintf(&b, "- 当前页: **%s**（%d）\n", pages[a.page].title(), int(a.page))
	fmt.Fprintf(&b, "- 搜索关键字: %q\n", a.query)
	fmt.Fprintf(&b, "- 隐藏已停止: %v\n", a.hideStopped)
	fmt.Fprintf(&b, "- 侧边栏宽度: %.1f\n", a.sidebarW)
	fmt.Fprintf(&b, "- 选中下标: 容器 %d / 镜像 %d / 卷 %d / 网络 %d / 项目 %d / 服务 %d\n",
		a.containerSel, a.imageSel, a.volumeSel, a.networkSel, a.projectSel, a.serviceSel)
	fmt.Fprintf(&b, "- 输出面板: 打开=%v 忙碌=%v 标题=%q\n",
		a.outputOpen, a.outputBusy, a.outputTitle)
	fmt.Fprintf(&b, "- 加载中: %v\n", a.loading)
	if a.lastError != "" {
		fmt.Fprintf(&b, "- 最近错误: %s\n", a.lastError)
	}
	if busy := a.busyKeys(); len(busy) > 0 {
		fmt.Fprintf(&b, "- 进行中的操作: %s\n", strings.Join(busy, ", "))
	}
	b.WriteString("\n")

	// ---- 设置 ----
	b.WriteString("## 设置\n\n")
	if a.store != nil {
		fmt.Fprintf(&b, "- 设置文件: %s\n", a.store.Path())
	}
	fmt.Fprintf(&b, "- 扫描深度: %d\n", a.settings.ScanDepth)
	fmt.Fprintf(&b, "- 自动刷新: %d 秒\n", a.settings.PollSeconds)
	if len(a.settings.ScanRoots) > 0 {
		fmt.Fprintf(&b, "- 扫描根目录:\n")
		for _, r := range a.settings.ScanRoots {
			fmt.Fprintf(&b, "  - `%s`\n", r)
		}
	}
	if len(a.settings.Projects) > 0 {
		fmt.Fprintf(&b, "- 已添加项目:\n")
		for _, r := range a.settings.Projects {
			fmt.Fprintf(&b, "  - `%s`\n", r)
		}
	}
	b.WriteString("\n")

	// ---- Compose 项目解析结果 ----
	if len(a.projects) > 0 {
		b.WriteString("## Compose 项目\n\n")
		for _, p := range a.projects {
			if !p.Healthy() {
				fmt.Fprintf(&b, "- `%s` **加载失败**: %s\n", p.Dir, p.Err)
				continue
			}
			fmt.Fprintf(&b, "- `%s` 名称=%s 服务=%d 文件=%s\n",
				p.Dir, p.Name, len(p.Config.Services), strings.Join(p.Config.ComposeFiles, ", "))
		}
		b.WriteString("\n")
	}

	// ---- 界面元素树 ----
	if opt.IncludeUI {
		b.WriteString("## 界面元素树\n\n")
		tree, err := a.fetchUITree(opt.SocketPath)
		if err != nil {
			fmt.Fprintf(&b, "（未能抓取：%s）\n\n", err.Error())
			fmt.Fprintf(&b,
				"提示：界面树需要 e2e 构建并设置 MYGO_AUTOMATION。\n"+
					"用 `scripts\\diag.ps1` 可自动完成「构建 → 启动 → 抓树」。\n\n")
		} else {
			fmt.Fprintf(&b, "共 %d 个元素。坐标单位 DIP，原点在窗口内容区左上角。\n\n", len(tree.Nodes))
			b.WriteString("```\n")
			for _, n := range tree.Nodes {
				b.WriteString(formatUINode(n))
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
	}

	// ---- 运行日志 ----
	if opt.IncludeLog && a.logs != nil {
		entries := a.logs.Snapshot()
		if opt.LogTail > 0 && len(entries) > opt.LogTail {
			entries = entries[len(entries)-opt.LogTail:]
		}
		fmt.Fprintf(&b, "## 运行日志（最近 %d 条）\n\n", len(entries))
		if len(entries) == 0 {
			b.WriteString("（无）\n\n")
		} else {
			b.WriteString("```\n")
			for _, e := range entries {
				b.WriteString(e.Line())
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
	}

	// ---- 机器可读部分 ----
	b.WriteString("## 结构化数据\n\n")
	b.WriteString("```json\n")
	payload := a.diagPayload(opt)
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintf(&b, "{\"error\":%q}\n", err.Error())
	}
	b.WriteString("```\n")

	return b.String(), nil
}

// appVersion 是报告里显示的版本号。
const appVersion = "0.1.0"

// diagPayload 生成报告末尾的结构化部分。
func (a *App) diagPayload(opt DiagOptions) map[string]any {
	m := map[string]any{
		"generatedAt": time.Now().Format(time.RFC3339),
		"version":     appVersion,
		"page":        pages[a.page].title(),
		"pageIndex":   int(a.page),
		"counts": map[string]int{
			"containers": len(a.containers),
			"images":     len(a.images),
			"volumes":    len(a.volumes),
			"networks":   len(a.networks),
			"projects":   len(a.projects),
		},
		"wslc":        a.wslcVer,
		"wslcCompose": a.composeVer,
	}
	if a.lastError != "" {
		m["lastError"] = a.lastError
	}
	if opt.IncludeUI {
		if tree, err := a.fetchUITree(opt.SocketPath); err == nil {
			m["uiTree"] = tree
		}
	}
	return m
}

// busyKeys 返回当前正在进行的操作键，按字典序排列。
func (a *App) busyKeys() []string {
	a.busyMu.Lock()
	defer a.busyMu.Unlock()
	var out []string
	for k, on := range a.busy {
		if on {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// fetchUITree 通过 automation socket 抓取界面元素树。
//
// 走 mygo-automation 命令行的 snapshot --json，而不是自己实现 socket 协议：
// 协议解析是那个工具的职责，这里只要能拿到数据。
func (a *App) fetchUITree(socketPath string) (diagUITree, error) {
	return FetchUITree(socketPath)
}

// FormatUITree 把 automation 的 snapshot JSON 渲染成缩进树文本。
//
// 单独导出这个函数，是为了让 scripts/diag.ps1 能把 JSON 交给程序处理：
// PowerShell 5.1 的 ConvertFrom-Json 处理不了超长单行 JSON，
// 而 Go 这边解析没有任何限制。
func FormatUITree(jsonData []byte) (string, error) {
	// 容忍 UTF-8 BOM：PowerShell 5.1 的 Out-File 总会写入它，
	// 而 encoding/json 不接受。
	jsonData = bytes.TrimPrefix(jsonData, []byte{0xEF, 0xBB, 0xBF})
	jsonData = bytes.TrimSpace(jsonData)

	var tree diagUITree
	if err := json.Unmarshal(jsonData, &tree); err != nil {
		return "", fmt.Errorf("解析 UI 树失败: %w", err)
	}
	byID := make(map[string]*diagUINode, len(tree.Nodes))
	for i := range tree.Nodes {
		byID[tree.Nodes[i].ID] = &tree.Nodes[i]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个元素（坐标单位 DIP，原点为内容区左上角）\n\n", len(tree.Nodes))
	for i := range tree.Nodes {
		n := &tree.Nodes[i]
		depth := 0
		for p := n.Parent; p != "" && depth < 20; depth++ {
			parent, ok := byID[p]
			if !ok {
				break
			}
			p = parent.Parent
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(formatUINode(*n))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// FetchUITree 通过 automation socket 抓取界面元素树。
//
// 它直接运行 mygo-automation 并用 Go 读取其标准输出，不经过任何 shell，
// 因此不会出现 PowerShell 截断长行或添加 BOM 的问题。
func FetchUITree(socketPath string) (diagUITree, error) {
	if strings.TrimSpace(socketPath) == "" {
		return diagUITree{}, fmt.Errorf("未设置 MYGO_AUTOMATION（界面树需要 e2e 构建）")
	}
	bin, err := findAutomationBinary()
	if err != nil {
		return diagUITree{}, err
	}

	// 用独立 session 名，避免打扰用户自己的 automation 会话。
	const session = "wslc-diag-oneshot"

	if out, err := exec.Command(bin, "open", "--socket", socketPath, "--session", session).CombinedOutput(); err != nil {
		return diagUITree{}, fmt.Errorf("打开 automation 会话失败: %s", firstLine(string(out)))
	}
	defer func() {
		_ = exec.Command(bin, "close", "--session", session).Run()
	}()

	raw, err := exec.Command(bin, "snapshot", "--json", "--session", session).Output()
	if err != nil {
		return diagUITree{}, fmt.Errorf("抓取快照失败: %v", err)
	}

	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	var tree diagUITree
	if err := json.Unmarshal(raw, &tree); err != nil {
		return diagUITree{}, fmt.Errorf("解析快照 JSON 失败: %v", err)
	}
	return tree, nil
}

// RenderUITree 把元素树渲染成带缩进和坐标的可读文本。
func RenderUITree(tree diagUITree) string {
	byID := make(map[string]*diagUINode, len(tree.Nodes))
	for i := range tree.Nodes {
		byID[tree.Nodes[i].ID] = &tree.Nodes[i]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个元素（坐标单位 DIP，原点为内容区左上角）\n\n", len(tree.Nodes))
	for i := range tree.Nodes {
		n := &tree.Nodes[i]
		depth := 0
		for p := n.Parent; p != "" && depth < 20; depth++ {
			parent, ok := byID[p]
			if !ok {
				break
			}
			p = parent.Parent
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(formatUINode(*n))
		b.WriteByte('\n')
	}
	return b.String()
}

// findAutomationBinary 定位 mygo-automation。
func findAutomationBinary() (string, error) {
	if p, err := exec.LookPath("mygo-automation"); err == nil {
		return p, nil
	}
	for _, name := range []string{"mygo-automation.exe", "mygo-automation"} {
		p := filepath.Join(gopathBin(), name)
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("找不到 mygo-automation（先运行：go install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation）")
}

// formatUINode 把一个 UI 元素渲染成一行紧凑文本。
//
// 格式：role "label" key=... [x,y wxh] states
// 坐标保留一位小数，便于人眼核对对齐。
func formatUINode(n diagUINode) string {
	parts := []string{n.Role}
	if n.Label != "" {
		parts = append(parts, fmt.Sprintf("%q", n.Label))
	}
	if n.Key != nil {
		if ks := fmt.Sprint(n.Key); ks != "" {
			parts = append(parts, "key="+ks)
		}
	}
	if n.Bounds != nil {
		parts = append(parts, fmt.Sprintf("[%g,%g %gx%g]",
			round1(n.Bounds.X), round1(n.Bounds.Y),
			round1(n.Bounds.Width), round1(n.Bounds.Height)))
	}
	if len(n.States) > 0 {
		parts = append(parts, strings.Join(n.States, ","))
	}
	return strings.Join(parts, " ")
}

// round1 保留一位小数。
func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

// gopathBin 返回 GOPATH/bin 目录。
func gopathBin() string {
	if gp := os.Getenv("GOPATH"); gp != "" {
		return filepath.Join(gp, "bin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "bin"
	}
	return filepath.Join(home, "go", "bin")
}

// fileExists 判断路径是否为存在的文件。
func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// fmtTime 格式化时间，零值显示为「尚未」。
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return locales.CommonNever()
	}
	return t.Format("2006-01-02 15:04:05")
}

// SaveDiagnostic 生成报告并写入文件，返回文件路径。
//
// path 为空时写到桌面（找不到桌面则用临时目录），文件名带时间戳。
func (a *App) SaveDiagnostic(opt DiagOptions, path string) (string, error) {
	text, err := a.BuildDiagnostic(opt)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		dir := desktopDir()
		if dir == "" {
			dir = os.TempDir()
		}
		name := fmt.Sprintf("wslc-manager-diag-%s.md", time.Now().Format("20060102-150405"))
		path = filepath.Join(dir, name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// desktopDir 返回用户桌面目录，找不到返回空串。
func desktopDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"Desktop", "桌面"} {
		for _, base := range []string{home, filepath.Join(home, "OneDrive")} {
			p := filepath.Join(base, name)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return p
			}
		}
	}
	return ""
}
