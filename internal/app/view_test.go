package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/envsetup"
	"github.com/arno/wslc-manager/internal/store"
	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/locales"
)

// newTestApp 构造一个不依赖真实 wslc 的应用实例：
// 数据快照直接注入，store 指向临时目录。
func newTestApp(t *testing.T) *App {
	t.Helper()
	a := New()
	a.store = store.New(t.TempDir() + "/settings.json")
	a.settings = store.DefaultSettings()
	a.settings.Projects = nil
	a.settings.ScanRoots = nil

	// 固定依赖环境状态，别让测试去探本机。
	//
	// 两个原因：
	//   1. 检测会启动后台命令（wsl.exe / uv），测试不该碰真实环境；
	//   2. 检测结果异步回填会改变系统页卡片高度，
	//      测量类断言会因此随机失败（CI 上就真的踩到过：
	//      同一份代码本地过、CI 报「最后一行高度为 0」）。
	a.envChecked = true
	a.env = envsetup.Status{
		WSLCPath:       `C:\Program Files\WSL\wslc.exe`,
		WSLVersion:     "3.0.5.0",
		ComposePath:    `C:\tools\wslc-compose.exe`,
		ComposeVersion: "0.6.0",
		Latest:         "0.6.0",
		Upgrader:       "uv",
		CheckedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	return a
}

// sampleData 填入一组固定的样例数据。
func sampleData(a *App) {
	a.containers = []wslc.Container{
		{
			ID: "c1", Names: "demo-web-1", Image: "nginx:alpine",
			State: "running", Status: "Up 2 minutes",
			Ports:     "0.0.0.0:8080->80/tcp",
			Labels:    "com.wslc-compose.project=demo,com.wslc-compose.service=web",
			Health:    "healthy",
			CreatedAt: "2026-01-01 00:00:00 +0800",
		},
		{
			ID: "c2", Names: "legacy", Image: "redis:7",
			State: "exited", Status: "Exited (1)",
		},
	}
	a.images = []wslc.Image{
		{ID: "sha256:aaaa11112222", Repository: "nginx", Tag: "alpine",
			Size: "48MB", CreatedSince: "2 weeks ago", Containers: "1"},
		{ID: "sha256:bbbb33334444", Repository: "<none>", Tag: "<none>",
			Size: "100MB", CreatedSince: "3 weeks ago", Containers: "0"},
	}
	a.volumes = []wslc.Volume{
		{Name: "demo_data", Driver: "guest", Mountpoint: "/var/lib/docker/volumes/demo_data/_data"},
	}
	a.networks = []wslc.Network{
		{ID: "n1", Name: "bridge", Driver: "bridge"},
		{ID: "n2", Name: "demo_default", Driver: "bridge", CreatedAt: "2026-01-01"},
	}
	a.sessions = []wslc.Session{{ID: "1", PID: "57024", DisplayName: "wslc-cli-test"}}
	a.wslcVer = "wslc test-1"
	a.composeVer = "wslc-compose 0.6.0"
	a.projects = []compose.Project{
		{
			Dir:  `E:\Project\demo`,
			Name: "demo",
			Config: compose.ProjectConfig{
				Name:         "demo",
				Directory:    `E:\Project\demo`,
				ComposeFiles: []string{`E:\Project\demo\compose.yaml`},
				Services: map[string]compose.ServiceConfig{
					"web": {
						Name:      "web",
						Image:     "nginx:alpine",
						Ports:     []compose.PortMapping{{Target: 80, Published: "8080", Protocol: "tcp"}},
						DependsOn: []string{"cache"},
						Replicas:  1,
					},
					"cache": {
						Name:  "cache",
						Image: "redis:7-alpine",
						Volumes: []compose.VolumeMapping{
							{Type: "volume", Source: "demo_data", Target: "/data"},
						},
						Replicas: 1,
					},
				},
			},
		},
		{
			Dir:  `E:\Project\broken`,
			Name: "broken",
			Err:  "compose 文件语法错误",
		},
	}
}

// frame 在给定尺寸下渲染一帧，返回 Tester 供断言使用。
func frame(t *testing.T, a *App) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()
	return tt
}

func TestViewRendersDefaultPage(t *testing.T) {
	a := newTestApp(t)
	tt := frame(t, a)

	if !tt.HasText("容器") {
		t.Errorf("应显示「容器」页标题, texts=%q", tt.Texts())
	}
	if !tt.HasText("没有容器") {
		t.Errorf("空数据时应显示空状态, texts=%q", tt.Texts())
	}
}

func TestViewRendersContainers(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers

	tt := frame(t, a)

	for _, want := range []string{"demo-web-1", "legacy", "nginx:alpine"} {
		if !tt.HasText(want) {
			t.Errorf("缺少文本 %q, texts=%q", want, tt.Texts())
		}
	}
	// 运行中的容器应显示「运行中（健康）」
	if !tt.HasText("运行中（健康）") {
		t.Errorf("缺少容器状态, texts=%q", tt.Texts())
	}
	if !tt.HasText("已停止") {
		t.Errorf("缺少已停止状态, texts=%q", tt.Texts())
	}
}

func TestViewAllPagesRender(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)

	// 依次切换每个页面并渲染，确保没有 panic 且关键文本存在。
	cases := []struct {
		page  Page
		texts []string
	}{
		{PageContainers, []string{"容器"}},
		{PageImages, []string{"镜像", "nginx", "alpine"}},
		{PageVolumes, []string{"卷", "demo_data"}},
		{PageNetworks, []string{"网络", "demo_default"}},
		{PageCompose, []string{"compose", "demo"}},
		{PageSystem, []string{"环境", "会话"}},
	}

	for _, tc := range cases {
		a.page = tc.page
		tt := ui.NewTester(a.View, 1180, 760)
		tt.Frame()
		for _, want := range tc.texts {
			if !tt.HasText(want) {
				t.Errorf("页面 %v 缺少文本 %q, texts=%q", tc.page, want, tt.Texts())
			}
		}
	}
}

func TestViewFiltersContainers(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers
	a.query = "legacy"

	tt := frame(t, a)

	if !tt.HasText("legacy") {
		t.Errorf("过滤后应显示 legacy, texts=%q", tt.Texts())
	}
	if tt.HasText("demo-web-1") {
		t.Errorf("过滤后不应显示 demo-web-1, texts=%q", tt.Texts())
	}
}

func TestViewHideStopped(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers
	a.hideStopped = true

	tt := frame(t, a)

	if tt.HasText("legacy") {
		t.Errorf("隐藏已停止后不应显示 legacy, texts=%q", tt.Texts())
	}
	if !tt.HasText("demo-web-1") {
		t.Errorf("应仍显示运行中的容器, texts=%q", tt.Texts())
	}
}

func TestFilteredContainersCount(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)

	if got := len(a.filteredContainers()); got != 2 {
		t.Errorf("默认应显示 2 个容器, got %d", got)
	}
	a.hideStopped = true
	if got := len(a.filteredContainers()); got != 1 {
		t.Errorf("隐藏已停止后应为 1, got %d", got)
	}
	a.hideStopped = false
	a.query = "redis"
	if got := len(a.filteredContainers()); got != 1 {
		t.Errorf("按镜像名过滤应为 1, got %d", got)
	}
}

func TestViewBrokenProjectShowsError(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageCompose
	a.projectSel = 1 // broken

	tt := frame(t, a)

	if !tt.HasText("项目配置加载失败：") {
		t.Errorf("应显示加载失败提示, texts=%q", tt.Texts())
	}
	if !tt.HasText("compose 文件语法错误") {
		t.Errorf("应显示具体错误, texts=%q", tt.Texts())
	}
}

func TestViewProjectServices(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageCompose
	a.projectSel = 0 // demo

	tt := frame(t, a)

	for _, want := range []string{"web", "cache", "nginx:alpine", "8080:80/tcp"} {
		if !tt.HasText(want) {
			t.Errorf("项目详情缺少 %q, texts=%q", want, tt.Texts())
		}
	}
}

func TestViewWslcMissing(t *testing.T) {
	a := newTestApp(t)
	a.wslcErr = "找不到 wslc 可执行文件"

	tt := frame(t, a)

	if !tt.HasText("无法使用 wslc") {
		t.Errorf("wslc 缺失时应提示, texts=%q", tt.Texts())
	}
}

func TestViewComposeMissing(t *testing.T) {
	a := newTestApp(t)
	a.composeErr = "找不到 wslc-compose"
	a.page = PageCompose

	tt := frame(t, a)

	if !tt.HasText("无法使用 wslc-compose") {
		t.Errorf("wslc-compose 缺失时应提示, texts=%q", tt.Texts())
	}
}

func TestViewOutputPanel(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers
	a.outputOpen = true
	a.outputTitle = "日志 · demo-web-1"
	a.outputText = "line one\nline two"

	tt := frame(t, a)

	if !tt.HasText("日志 · demo-web-1") {
		t.Errorf("应显示输出面板标题, texts=%q", tt.Texts())
	}
}

func TestPageIDRoundTrip(t *testing.T) {
	for p := PageContainers; p <= PageSystem; p++ {
		if got := parsePageID(pageID(p)); got != p {
			t.Errorf("pageID 往返失败: %v → %q → %v", p, pageID(p), got)
		}
	}
	if got := parsePageID("garbage"); got != PageContainers {
		t.Errorf("非法 ID 应回落到容器页, got %v", got)
	}
	if got := parsePageID("page-99"); got != PageContainers {
		t.Errorf("越界 ID 应回落到容器页, got %v", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("短文本不应被截断, got %q", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcde…" {
		t.Errorf("长文本应截断并加省略号, got %q", got)
	}
	// 中文按 rune 计数
	if got := truncate("中文测试文本", 2); got != "中文…" {
		t.Errorf("中文截断错误, got %q", got)
	}
}

func TestShortID(t *testing.T) {
	tests := map[string]string{
		"sha256:abcdef1234567890": "abcdef123456",
		"abcdef":                  "abcdef",
		"":                        "",
	}
	for in, want := range tests {
		if got := shortID(in); got != want {
			t.Errorf("shortID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAtoiSafe(t *testing.T) {
	tests := map[string]int{
		"0": 0, "5": 5, "  12  ": 12, "": -1, "abc": -1, "-3": -1, "999999999999": -1,
	}
	for in, want := range tests {
		if got := atoiSafe(in); got != want {
			t.Errorf("atoiSafe(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMergeDirs(t *testing.T) {
	got := mergeDirs(
		[]string{`E:\a`, `E:\b`},
		[]string{`E:\B`, `E:\c`},
	)
	if len(got) != 3 {
		t.Errorf("应去重（忽略大小写）得到 3 项, got %v", got)
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName(wslc.Container{Names: "web-1", ID: "abc"}); got != "web-1" {
		t.Errorf("应优先用名称, got %q", got)
	}
	if got := displayName(wslc.Container{Names: "", ID: "abc"}); got != "abc" {
		t.Errorf("无名称时应回落到 ID, got %q", got)
	}
}

func TestBaseName(t *testing.T) {
	tests := map[string]string{
		`E:\Project\demo`:  "demo",
		`E:\Project\demo\`: "demo",
		`/home/user/demo`:  "demo",
		`demo`:             "demo",
	}
	for in, want := range tests {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServiceDetailText(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)

	text := serviceDetailText(a.projects[0], "web")
	for _, want := range []string{"web", "nginx:alpine", "8080:80/tcp", "cache"} {
		if !containsStr(text, want) {
			t.Errorf("服务详情缺少 %q:\n%s", want, text)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestEmptyStateIsCentered 检查空状态文字在内容区里水平居中。
//
// 这条回归测试针对一个真实缺陷：表格与空状态同时存在时，
// 两者的布局请求互相挤压，导致文字偏位。
func TestEmptyStateIsCentered(t *testing.T) {
	a := newTestApp(t)
	a.page = PageContainers
	// 无数据 → 空状态

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	info, ok := tt.Access("没有容器")
	if !ok {
		t.Fatal("未找到空状态文字")
	}
	hint, ok := tt.Access("从 Compose 项目启动一组服务，或拉取镜像后手动运行")
	if !ok {
		t.Fatal("未找到空状态提示文字")
	}

	// 两行文字必须共用同一条中线（彼此对齐）。
	centerA := info.Bounds.X + info.Bounds.W/2
	centerB := hint.Bounds.X + hint.Bounds.W/2
	if diff := centerA - centerB; diff > 1 || diff < -1 {
		t.Errorf("两行空状态文字未对齐: %.1f vs %.1f", centerA, centerB)
	}

	// 空状态必须位于内容区（侧边栏右侧）之内。
	if info.Bounds.X <= 0 {
		t.Errorf("空状态 X=%.1f 意外落在侧边栏里", info.Bounds.X)
	}
}

// TestToolbarHasSingleRefreshIcon 检查工具栏只有一个刷新按钮。
//
// 回归测试：早先多画了一个独立的刷新图标，右上角出现两个刷新图标。
func TestToolbarHasSingleRefreshIcon(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	count := 0
	for _, info := range tt.AccessAll() {
		if info.Name == "刷新" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("工具栏应只有 1 个刷新控件, got %d", count)
	}
}

// TestStatCaptionMerged 检查统计块的副标题并入第二行。
//
// 回归测试：早先「容器」块因附加「0 运行中」而变成三行，
// 与同一 Row 里其它两行的块数字错位。
func TestStatCaptionMerged(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageSystem

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	texts := tt.Texts()
	joined := strings.Join(texts, "|")

	// 副标题必须并入第二行（用「·」连接），不能自成一行。
	if !strings.Contains(joined, "容器 · 1 运行中") {
		t.Errorf("副标题应并入第二行, texts=%q", texts)
	}
	for _, tx := range texts {
		if tx == "1 运行中" {
			t.Errorf("副标题不应单独成行, texts=%q", texts)
		}
	}
}

// TestSystemPageLastCardFullyVisible 检查系统页滚到底时，
// 「已知限制」卡片的最后一行完整可见，不被视口底边裁掉。
//
// 回归测试：早先滚动到最底部时，最后一行只剩 8px 可见高度，
// 看起来像卡片被 clip 了。原因是滚动内容末尾没有占位的缓冲，
// 行高留白被视口边缘吃掉。
func TestSystemPageLastCardFullyVisible(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageSystem

	const W, H = 1180, 760
	tt := ui.NewTester(a.View, W, H)
	tt.Frame()

	last := "• 当前版本不支持 pause/unpause、unless-stopped / on-failure 重启策略。"

	// 滚到底（初始位置这张卡片在视口外）。
	tt.Scroll(float32(W)/2, float32(H)/2, 0, 10000)
	tt.Frame()

	r, ok := tt.Find(last)
	if !ok {
		t.Fatal("滚到底后找不到最后一行")
	}
	if r.H <= 0 {
		t.Fatalf("最后一行高度为 0，未渲染 (Y=%.1f)", r.Y)
	}
	// 完整行高约 16；明显小于它说明被裁切。
	if r.H < 14 {
		t.Errorf("最后一行被裁切：H=%.1f 小于完整行高", r.H)
	}
	if bottom := r.Y + r.H; bottom > H {
		t.Errorf("最后一行超出窗口底部：bottom=%.1f > %d", bottom, H)
	}
	// 底部要有可见余量：窗口底部还有状态栏，
	// 余量不足时视觉上就是「贴着边被切掉」。
	// 具体留多少由 systempage 的 Spacer 决定（当前 24），
	// 这里只保证它不会被状态栏完全吃掉。
	if gap := float32(H) - (r.Y + r.H); gap < 20 {
		t.Errorf("滚到底后底部余量不足：gap=%.1f < 20", gap)
	}
}

// TestImagePageHasOutputPanel 检查镜像页能显示输出面板。
//
// 回归测试：早先「查看标签」「查看详情」调用 showOutput 打开了输出面板，
// 但镜像页根本不渲染它，点击看起来毫无反应。
func TestImagePageHasOutputPanel(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageImages
	a.outputOpen = true
	a.outputTitle = "镜像 · nginx:alpine"
	a.outputText = "仓库: nginx"

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	if !tt.HasText("镜像 · nginx:alpine") {
		t.Errorf("镜像页应渲染输出面板, texts=%q", tt.Texts())
	}
	if !tt.HasText("仓库: nginx") {
		t.Errorf("输出面板应显示内容, texts=%q", tt.Texts())
	}
}

// TestImageTagsListed 检查「查看标签」会列出同仓库的所有本地标签。
func TestImageTagsListed(t *testing.T) {
	a := newTestApp(t)
	a.images = []wslc.Image{
		{ID: "sha256:aa11", Repository: "nginx", Tag: "alpine", Size: "48MB", CreatedSince: "2 weeks ago"},
		{ID: "sha256:bb22", Repository: "nginx", Tag: "latest", Size: "50MB", CreatedSince: "1 week ago"},
		{ID: "sha256:cc33", Repository: "redis", Tag: "7", Size: "30MB", CreatedSince: "3 weeks ago"},
	}

	text := imageTagsText(a.images, a.images[0])
	for _, want := range []string{"nginx", "alpine", "latest", "本地共有 2 个标签"} {
		if !containsStr(text, want) {
			t.Errorf("标签列表缺少 %q:\n%s", want, text)
		}
	}
	// 不应把其它仓库的标签列进来
	if containsStr(text, "redis") {
		t.Errorf("不应包含其它仓库的标签:\n%s", text)
	}
}

// TestUpdateAllCollectsRefs 检查「一键更新全部」收集的引用去重且跳过悬空镜像。
func TestUpdateAllCollectsRefs(t *testing.T) {
	rows := []wslc.Image{
		{ID: "a", Repository: "nginx", Tag: "alpine"},
		{ID: "b", Repository: "nginx", Tag: "alpine"},  // 重复引用
		{ID: "c", Repository: "<none>", Tag: "<none>"}, // 悬空，应跳过
		{ID: "d", Repository: "redis", Tag: "7"},
	}
	refs := imagePullRefs(rows)
	if len(refs) != 2 {
		t.Fatalf("应收集 2 个去重引用, got %v", refs)
	}
	if refs[0] != "nginx:alpine" || refs[1] != "redis:7" {
		t.Errorf("引用集合错误: %v", refs)
	}
}

// TestClickImageTagsShowsPanel 端到端检查「查看标签」的交互链路：
// 选中一行 → 点击按钮 → 输出面板出现且内容正确。
func TestClickImageTagsShowsPanel(t *testing.T) {
	a := newTestApp(t)
	a.images = []wslc.Image{
		{ID: "sha256:aa11", Repository: "nginx", Tag: "alpine", Size: "48MB", CreatedSince: "2w"},
		{ID: "sha256:bb22", Repository: "nginx", Tag: "latest", Size: "50MB", CreatedSince: "1w"},
	}
	a.page = PageImages
	a.imageSel = 0 // 预先选中第一行

	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()

	// 点「查看标签」
	if err := tt.Click("查看标签"); err != nil {
		t.Fatalf("点击「查看标签」失败: %v", err)
	}
	tt.Frame()

	if !a.outputOpen {
		t.Fatal("点击后应打开输出面板")
	}
	if !strings.Contains(a.outputText, "本地共有 2 个标签") {
		t.Errorf("输出内容不正确:\n%s", a.outputText)
	}
	// 面板在界面上确实渲染出来了
	if !tt.HasText("标签 · nginx") {
		t.Errorf("界面应显示面板标题, texts=%q", tt.Texts())
	}
}

// TestPullShowsProgressImmediately 端到端验证拉取镜像时的实时进度显示：
// 面板立刻打开，并且每一行进度都反映到界面上。
//
// 回归测试：早先要等 wslc pull 整体结束才显示结果，
// 拉大镜像时界面长时间毫无反馈。
func TestPullShowsProgressImmediately(t *testing.T) {
	a := newTestApp(t)
	a.page = PageImages
	a.images = []wslc.Image{{ID: "x", Repository: "nginx", Tag: "alpine"}}

	lines := []string{
		"alpine: Pulling from library/nginx",
		"881fc932939e: Downloading",
		"881fc932939e: Download complete",
	}
	started := make(chan struct{})
	release := make(chan struct{})
	a.pullStream = func(ctx context.Context, ref string, onLine func(string)) (wslc.Result, error) {
		close(started)
		for _, ln := range lines {
			onLine(ln)
		}
		<-release
		return wslc.Result{Args: []string{"pull", ref}, ExitCode: 0}, nil
	}

	go a.pullAndReport("nginx:alpine", "已更新 nginx:alpine")
	<-started

	read := func(fn func()) { a.ViewLocked(fn) }

	var opened bool
	var title string
	read(func() { opened, title = a.outputOpen, a.outputTitle })
	if !opened {
		close(release)
		t.Fatal("拉取开始时未立即打开输出面板")
	}
	if title != "拉取镜像 · nginx:alpine" {
		close(release)
		t.Errorf("面板标题错误: %q", title)
	}

	// 等进度行到达。
	deadline := time.Now().Add(3 * time.Second)
	hasLine := func(sub string) bool {
		var got bool
		read(func() { got = strings.Contains(a.outputText, sub) })
		return got
	}
	for !hasLine("Download complete") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !hasLine("Download complete") {
		var txt string
		read(func() { txt = a.outputText })
		close(release)
		t.Fatalf("进度行未实时反映到面板: %q", txt)
	}

	var busy bool
	read(func() { busy = a.outputBusy })
	if !busy {
		close(release)
		t.Error("进行中状态应为 true")
	}

	close(release)

	// 等结束后应变成非忙碌。
	for time.Now().Before(deadline) {
		var b bool
		read(func() { b = a.outputBusy })
		if !b {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	var stillBusy bool
	read(func() { stillBusy = a.outputBusy })
	if stillBusy {
		t.Error("任务结束后应清除进行中状态")
	}

	var final string
	read(func() { final = a.outputText })
	if !strings.Contains(final, "完成") {
		t.Errorf("结束时应显示完成提示, got %q", final)
	}
}

// 轻量的时间辅助，避免在测试文件里引入 time 包名冲突。
type timePoint = time.Time

func timeNowPlus(d time.Duration) timePoint { return time.Now().Add(d) }
func before(deadline timePoint) bool        { return time.Now().Before(deadline) }

const second = time.Second

// TestComposeActionShowsProgress 端到端验证 compose 操作的反馈：
// 面板立刻打开、进度逐行到达、结束后清除忙碌。
//
// 回归测试：早先 compose 的 up/down/build 用 runAsync 跑完才写结果，
// 而 compose 页根本不渲染输出面板，a.busy 也没有任何界面读取，
// 于是可能跑几十秒的操作看起来完全没反应。
func TestComposeActionShowsProgress(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageCompose
	a.projectSel = 0

	lines := []string{
		"Network demo_default created",
		"Creating demo-cache-1 ...",
		"Creating demo-web-1 ...",
	}
	started := make(chan struct{})
	release := make(chan struct{})

	// 用假实现替换真实 wslc-compose：逐行发进度，等测试放行后返回成功。
	a.composeStream = func(ctx context.Context, dir string, action compose.Action,
		opt compose.ActionOptions, onLine func(string)) (compose.Result, error) {
		close(started)
		for _, ln := range lines {
			onLine(ln)
		}
		<-release
		return compose.Result{Args: []string{string(action)}, ExitCode: 0, Duration: time.Millisecond}, nil
	}

	go a.composeRun(`E:\Project\demo`, compose.ActionUp, compose.ActionOptions{Detach: true}, "已启动 demo")
	<-started

	// 读取共享状态一律通过 ViewLocked，避免与后台协程竞争。
	read := func(fn func()) { a.ViewLocked(fn) }

	var opened bool
	var title string
	read(func() { opened, title = a.outputOpen, a.outputTitle })
	if !opened {
		close(release)
		t.Fatal("compose 操作应立即打开输出面板")
	}
	if title != "启动 · demo" {
		close(release)
		t.Errorf("面板标题错误: %q", title)
	}

	// 等进度行到达（轮询读状态，期间不持锁渲染）。
	deadline := time.Now().Add(3 * time.Second)
	hasLine := func(sub string) bool {
		var got bool
		read(func() { got = strings.Contains(a.outputText, sub) })
		return got
	}
	for !hasLine("Creating demo-web-1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !hasLine("Creating demo-web-1") {
		var txt string
		read(func() { txt = a.outputText })
		close(release)
		t.Fatalf("进度未实时反映: %q", txt)
	}
	if !a.isBusy(`compose-up-E:\Project\demo`) {
		close(release)
		t.Error("进行中应标记 busy")
	}
	if !a.composeBusy(`E:\Project\demo`) {
		close(release)
		t.Error("composeBusy 应报告该目录正在执行")
	}

	close(release)

	// 等结束。
	for a.composeBusy(`E:\Project\demo`) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.composeBusy(`E:\Project\demo`) {
		t.Error("结束后应清除 busy")
	}
	var final string
	read(func() { final = a.outputText })
	if !strings.Contains(final, "完成") {
		t.Errorf("结束时应显示完成, got %q", final)
	}

	// 界面应能渲染出面板（无锁，单独渲染一帧）。
	tt := ui.NewTester(a.View, 1240, 900)
	tt.Frame()
	if !tt.HasText("启动 · demo") {
		t.Errorf("compose 页应渲染输出面板, texts=%q", tt.Texts())
	}
}

// TestProjectListHasHeight 检查 Compose 页的项目列表有实际高度。
//
// 回归测试：曾经列表的 Grow(1) 与并列的 Fill() 空状态互相挤压，
// 列表高度塌陷成 0 —— 界面看起来有内容，但整列都点不中。
func TestProjectListHasHeight(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageCompose
	// 故意让项目为空，走空状态分支
	a.settings.Projects = nil
	a.projects = nil

	tt := ui.NewTester(a.View, 1240, 900)
	tt.Frame()

	info, ok := tt.Access("Compose 项目列表")
	if !ok {
		t.Fatal("找不到项目列表")
	}
	if info.Bounds.H < 100 {
		t.Errorf("项目列表高度异常: %.1f（应为数百 DIP）", info.Bounds.H)
	}
}

// TestLanguageSwitchChangesLabels 验证切换语言后界面文案真的变了。
//
// 回归测试：i18n 最容易出现的「假成功」是——词表加了英文、
// 但视图还在用硬编码中文，或者视图取语言的时机不对（缓存了旧值）。
// 这里用同一份数据渲染两种语言，断言关键文案不同、且都不为空。
func TestLanguageSwitchChangesLabels(t *testing.T) {
	// 不依赖 mygo.App（测试环境没有事件循环），
	// 直接用词表按语言取值来验证「同一份界面数据 → 两种文案」。
	zh := localesIn("zh-Hans")
	en := localesIn("en")

	pairs := []struct {
		name string
		zh   string
		en   string
	}{
		{"侧边栏-容器", zh.NavContainers(), en.NavContainers()},
		{"侧边栏-镜像", zh.NavImages(), en.NavImages()},
		{"容器页空状态", zh.ContainersEmptyBody(), en.ContainersEmptyBody()},
		{"镜像页-拉取", zh.ImagesPull(), en.ImagesPull()},
		{"Compose-添加项目", zh.ComposeAdd(), en.ComposeAdd()},
		{"系统页-已知限制", zh.SystemKnownLimits(), en.SystemKnownLimits()},
	}

	for _, p := range pairs {
		if p.zh == "" {
			t.Errorf("%s 的中文为空", p.name)
		}
		if p.en == "" {
			t.Errorf("%s 的英文为空", p.name)
		}
		if p.zh == p.en {
			t.Errorf("%s 两种语言相同（%q），可能是漏翻", p.name, p.zh)
		}
	}
}

// TestViewRendersWithLocales 确认视图在两个语言下都能正常构建。
//
// 这条防的是「英文文案过长导致布局或访问器出错」这类问题：
// 英文普遍比中文长，行高、列宽、截断都可能因此异常。
func TestViewRendersWithLocales(t *testing.T) {
	for _, locale := range []string{"zh-Hans", "en"} {
		t.Run(locale, func(t *testing.T) {
			a := newTestApp(t)
			sampleData(a)
			a.page = PageContainers

			// 渲染完必须把全局语言来源恢复，
			// 否则同包后续测试会全部跑在英文下（曾经真的污染过：
			// 后面的测试按中文文案查找元素，直接找不到）。
			t.Cleanup(func() { i18n.SetLocaleFunc(nil) })

			tt := ui.NewTester(func(c *ui.Context) {
				a.viewForLocale(c, locale)
			}, 1180, 760)
			tt.Frame()
			tt.Frame()

			// 表格的无障碍标签随语言变化，所以按当前语言的文案去查。
			// 用表格的 role 查，而不是 label：
			// label 会与侧边栏同名的导航项撞名（英文下 Containers 两者都有），
			// 那样断言到的是导航项而不是表格。
			label := locales.In(locale).ContainersTitle()
			info, ok := tt.AccessByRole(ui.RoleTable, label)
			if !ok {
				t.Fatalf("%s：没有找到容器表格（label=%q）", locale, label)
			}
			if info.Bounds.H < 100 {
				t.Errorf("%s：容器表格高度异常 %.1f", locale, info.Bounds.H)
			}
		})
	}
}

// localesIn 返回指定语言的词表访问器，用于在测试里对比两种语言。
func localesIn(locale string) locales.Messages {
	return locales.In(locale)
}

// viewForLocale 在指定语言下渲染视图。
//
// 生产代码里语言由 MyGo 的 App.Locale 驱动。测试环境没有事件循环，
// 所以临时把 i18n 的语言来源指向固定值，渲染完再恢复。
// 这样测试验证的是真实视图，只是换了取语言的入口。
func (a *App) viewForLocale(c *ui.Context, locale string) {
	i18n.SetLocaleFunc(func() string { return locale })
	a.View(c)
}

// TestSystemPageStableWithoutCompose 验证缺少 wslc-compose 时系统页布局稳定。
//
// 回归测试：CI 曾报「最后一行高度为 0」，同一份代码本地却是好的。
// 根因是视图里发起了依赖检测——检测结果异步回填会改变卡片高度，
// 渲染中途布局变化，测量结果就不稳定。CI 机器上没有 wslc-compose，
// 检测走的路径不同、耗时更长，于是必然撞上。
//
// 现在检测移到 Init，newTestApp 显式固定环境状态，视图彻底不碰 IO。
func TestSystemPageStableWithoutCompose(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageSystem

	// 缺少 compose 的场景：卡片内容与完全就绪时不同。
	a.compose = nil
	a.composeErr = "wslc-compose 未找到"
	a.env.ComposePath = ""
	a.env.ComposeVersion = ""
	a.env.Upgrader = ""

	const W, H = 1180, 760
	tt := ui.NewTester(a.View, W, H)
	// 多渲染几帧：任何「首帧正常、后续抖动」的问题都会暴露。
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	tt.Scroll(float32(W)/2, float32(H)/2, 0, 10000)
	tt.Frame()

	last := "• 当前版本不支持 pause/unpause、unless-stopped / on-failure 重启策略。"
	r, ok := tt.Find(last)
	if !ok {
		t.Fatal("滚到底后找不到最后一行")
	}
	if r.H < 14 {
		t.Errorf("最后一行被裁切：H=%.1f", r.H)
	}
}
