package app

import (
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/selfupdate"
	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/internal/wslvm"
	"github.com/arno/wslc-manager/locales"
)

// 演示窗口的尺寸（DIP），与应用的默认窗口宽度一致。
const demoW, demoH = 1180, 620

// useLocale 让应用词条在这个测试里读指定的语言，测试结束后恢复。
//
// 必须恢复：全局语言来源是整个包共用的，不恢复会让同包后面的测试全跑在英文下
// （见 TestViewRendersWithLocales 里的同款说明）。
func useLocale(t *testing.T, locale string) {
	t.Helper()
	i18n.SetLocaleFunc(func() string { return locale })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
}

// demoData 返回一份带数据的场景，各页面都有东西可看。
func demoData(page Page) DemoScene {
	s := NewDemoScene()
	s.Page = page
	s.Containers = []wslc.Container{
		{ID: "c1", Names: "demo-web-1", Image: "nginx:alpine", State: "running", Health: "healthy",
			Ports: "0.0.0.0:8080->80/tcp", Labels: "com.wslc-compose.project=demo,com.wslc-compose.service=web"},
		{ID: "c2", Names: "demo-db-1", Image: "postgres:17", State: "exited",
			Labels: "com.wslc-compose.project=demo,com.wslc-compose.service=db"},
	}
	s.Images = []wslc.Image{{ID: "sha256:aaaa11112222", Repository: "nginx", Tag: "alpine", Size: "48MB", CreatedSince: "2 weeks ago", Containers: "1"}}
	s.Volumes = []wslc.Volume{{Name: "demo_data", Driver: "guest", Mountpoint: "/var/lib/docker/volumes/demo_data/_data"}}
	s.Networks = []wslc.Network{{ID: "n1", Name: "demo_default", Driver: "bridge"}}
	s.Projects = []compose.Project{{
		Dir: `D:\dev\demo`, Name: "demo",
		Config: compose.ProjectConfig{
			Name: "demo", Directory: `D:\dev\demo`, ComposeFiles: []string{`D:\dev\demo\compose.yaml`},
			Services: map[string]compose.ServiceConfig{
				"web": {Name: "web", Image: "nginx:alpine", Replicas: 1, DependsOn: []string{"db"}},
				"db":  {Name: "db", Image: "postgres:17", Replicas: 1},
			},
		},
	}}
	s.VM = wslvm.Snapshot{State: wslvm.Running, Session: "wslc-cli-demo", Process: "vmmemwslc-cli-demo", PID: 4242, Memory: 1413 << 20}
	// 时间写死：测试结果不能随运行的时刻变。
	s.Update = selfupdate.Status{Version: "0.2.0", Supported: true, Available: "0.3.0",
		LastCheck: time.Date(2026, 1, 1, 9, 41, 0, 0, time.Local)}
	s.WSLCVersion = "wslc 3.0.5.0"
	return s
}

// renderDemo 在给定尺寸的画布里画两帧（第二帧让「要靠上一帧布局才量得到」的东西到位），返回 Tester。
func renderDemo(d *Demo, s DemoScene, canvasW, canvasH int) *ui.Tester {
	tt := ui.NewTester(func(c *ui.Context) { d.View(c, s) }, canvasW, canvasH)
	tt.Frame()
	tt.Frame()
	return tt
}

// renderDemoIn 和 renderDemo 一样，但把「上下文语言」也设成 locale。
//
// 应用自己的词条走全局的 i18n.Locale()（useLocale 管它），而 MyGo 自带控件的文字
// （比如分隔条的无障碍标签）走画布的上下文语言。渲染宣传片时后者由 Composition.Locale 给，
// 这里的 SetLocale 就是同一个机制：两个都设了，测试里看到的才和视频里一样。
func renderDemoIn(locale string, d *Demo, s DemoScene, canvasW, canvasH int) *ui.Tester {
	tt := ui.NewTester(func(c *ui.Context) { d.View(c, s) }, canvasW, canvasH)
	tt.SetLocale(locale)
	tt.Frame()
	tt.Frame()
	return tt
}

// TestDemoNeverTouchesABackend 验证演示实例是脱离后端的。
//
// 它存在的理由：宣传素材里不能出现使用者本机的容器、路径，渲染时也不能去叫醒真实的虚拟机。
// 只要 wslc、Compose 引擎、设置文件、窗口这几个入口都是空的，视图就无处可去。
func TestDemoNeverTouchesABackend(t *testing.T) {
	d := NewDemo(demoW, demoH)
	for _, page := range []Page{PageContainers, PageImages, PageVolumes, PageNetworks, PageCompose, PageSystem} {
		renderDemo(d, demoData(page), demoW, demoH)
	}
	a := d.a
	if a.wslc != nil || a.compose != nil || a.store != nil || a.win != nil {
		t.Errorf("演示实例不该有后端：wslc=%v compose=%v store=%v win=%v", a.wslc, a.compose, a.store, a.win)
	}
	// 就算有人触发了刷新，也必须什么都不做（Refresh 在 wslc 为空时直接返回）。
	a.Refresh()
	if a.loading {
		t.Error("演示实例里 Refresh 不该进入加载状态")
	}
}

// TestDemoRendersEveryPageInBothLanguages 验证每个页面在中英两种语言下都能用真实视图画出来。
func TestDemoRendersEveryPageInBothLanguages(t *testing.T) {
	for _, locale := range []string{"zh-Hans", "en"} {
		m := locales.In(locale)
		pages := []struct {
			name string
			page Page
			want []string // 这一页该看到的文字
		}{
			{"containers", PageContainers, []string{"demo-web-1", m.NavContainers()}},
			{"images", PageImages, []string{"nginx", m.NavImages()}},
			{"volumes", PageVolumes, []string{"demo_data", m.NavVolumes()}},
			{"networks", PageNetworks, []string{"demo_default", m.NavNetworks()}},
			{"compose", PageCompose, []string{"demo", m.NavCompose()}},
			{"system", PageSystem, []string{"wslc-cli-demo", m.VmTitle()}},
		}
		for _, p := range pages {
			t.Run(locale+"/"+p.name, func(t *testing.T) {
				useLocale(t, locale)
				tt := renderDemoIn(locale, NewDemo(demoW, demoH), demoData(p.page), demoW, demoH)
				for _, want := range p.want {
					if !tt.HasText(want) {
						t.Errorf("没有看到 %q，现有文字: %q", want, tt.Texts())
					}
				}
			})
		}
	}
}

// TestDemoContainerStatusFollowsLanguage 是「状态列写死中文」的端到端回归：
// 英文界面下容器表的状态列必须是英文。（单元层面的映射在 statelabel_test.go。）
func TestDemoContainerStatusFollowsLanguage(t *testing.T) {
	useLocale(t, "en")
	tt := renderDemo(NewDemo(demoW, demoH), demoData(PageContainers), demoW, demoH)
	for _, want := range []string{"Running (healthy)", "Stopped"} {
		if !tt.HasText(want) {
			t.Errorf("英文界面应显示 %q，现有文字: %q", want, tt.Texts())
		}
	}
	for _, bad := range []string{"运行中", "已停止"} {
		if tt.HasText(bad) {
			t.Errorf("英文界面不该出现中文状态 %q", bad)
		}
	}
}

// TestEnglishInterfaceHasNoChinese 保障「英文界面是真正的英文」：所有页面、选中状态、虚拟机各状态、
// 更新卡片各状态、空状态、输出面板——渲染出来的每一段文字都不能含汉字。
//
// 为什么需要它：英文版宣传片要求界面从头到尾都是英文。此前容器表的状态列写死了中文，
// 没有任何测试能抓到它——词表的测试只看词条，看不到代码里直接写死的字符串。
// 这条直接数真实视图里渲染出的文字，漏网的只有不显示的提示文字（tooltip）。
func TestEnglishInterfaceHasNoChinese(t *testing.T) {
	useLocale(t, "en")
	han := regexp.MustCompile(`\p{Han}`)

	var scenes []struct {
		name string
		s    DemoScene
	}
	add := func(name string, s DemoScene) {
		scenes = append(scenes, struct {
			name string
			s    DemoScene
		}{name, s})
	}

	pages := map[string]Page{
		"containers": PageContainers, "images": PageImages, "volumes": PageVolumes,
		"networks": PageNetworks, "compose": PageCompose, "system": PageSystem,
	}
	for name, p := range pages {
		add(name+"/with data", demoData(p))
		// 没有数据：各页的空状态文案。
		empty := NewDemoScene()
		empty.Page = p
		empty.VM = demoData(p).VM
		add(name+"/empty", empty)
	}

	// 选中行：按钮的可用状态、详情面板都会变。
	for row := 0; row < 2; row++ {
		s := demoData(PageContainers)
		s.ContainerRow = row
		add("containers/selected", s)
	}
	img := demoData(PageImages)
	img.ImageRow = 0
	add("images/selected", img)
	cp := demoData(PageCompose)
	cp.ProjectRow, cp.ServiceRow = 0, 0
	add("compose/selected", cp)
	cp.ComposeBusyDir = `D:\dev\demo`
	add("compose/busy", cp)

	// 输出面板的框架（复制、关闭、行数、运行中），内容是调用方给的。
	for _, busy := range []bool{true, false} {
		s := demoData(PageContainers)
		s.Output = &DemoOutput{Title: "Logs \u00b7 x", Text: "a\nb", Busy: busy}
		add("output panel", s)
	}

	// 虚拟机卡片的所有状态，包括观测不到（零值）。
	for _, vm := range []struct {
		snap   wslvm.Snapshot
		action DemoVMAction
	}{
		{wslvm.Snapshot{State: wslvm.Running, Session: "s", Memory: 1413 << 20}, DemoVMIdle},
		{wslvm.Snapshot{State: wslvm.Running, Session: "s", Memory: 1413 << 20}, DemoVMStopping},
		{wslvm.Snapshot{State: wslvm.Stopped, Session: "s"}, DemoVMIdle},
		{wslvm.Snapshot{State: wslvm.Stopped, Session: "s"}, DemoVMStarting},
		{wslvm.Snapshot{}, DemoVMIdle},
	} {
		s := demoData(PageSystem)
		s.VM, s.VMAction = vm.snap, vm.action
		add("system/vm", s)
		// 虚拟机确定停止时，资源页显示「虚拟机未运行」的提示，而不是数据。
		c := demoData(PageContainers)
		c.VM, c.VMAction = vm.snap, vm.action
		add("containers/vm", c)
	}

	// 更新卡片的四类状态：不支持、有新版、从未检查、已是最新。
	checked := time.Date(2026, 1, 1, 9, 41, 0, 0, time.Local)
	for _, up := range []selfupdate.Status{
		{Version: "dev"},
		{Version: "0.2.0", Supported: true, Available: "0.3.0", LastCheck: checked},
		{Version: "0.2.0", Supported: true},
		{Version: "0.2.0", Supported: true, LastCheck: checked, AutoCheck: true, AutoInstall: true},
	} {
		s := demoData(PageSystem)
		s.Update = up
		add("system/update", s)
	}

	for _, sc := range scenes {
		tt := renderDemoIn("en", NewDemo(demoW, demoH), sc.s, demoW, demoH)
		for _, text := range tt.Texts() {
			if han.MatchString(text) {
				t.Errorf("%s：英文界面里出现了汉字 %q", sc.name, text)
			}
		}
	}
}

// textsContain 报告 texts 里有没有一段恰好等于 want 的文字。
//
// 不用 Tester.HasText：它按包含匹配，「Created」里就含有「Create」，没法断言「不是 Create」。
func textsContain(texts []string, want string) bool {
	for _, s := range texts {
		if s == want {
			return true
		}
	}
	return false
}

// TestEnglishImagesPageUsesActionAndColumnLabels 是镜像页两处英文文案的回归测试，
// 都是渲染英文版宣传片时看画面才发现的：
//   - 工具栏的「拉取镜像…」按钮被译成进行时的 "Pulling image…"；
//   - 表格的「创建时间」列头用了动词词条，显示成 "Create"（应为 "Created"）。
func TestEnglishImagesPageUsesActionAndColumnLabels(t *testing.T) {
	useLocale(t, "en")
	texts := renderDemoIn("en", NewDemo(demoW, demoH), demoData(PageImages), demoW, demoH).Texts()
	for _, want := range []string{"Pull image…", "Created"} {
		if !textsContain(texts, want) {
			t.Errorf("镜像页应有 %q，现有文字: %q", want, texts)
		}
	}
	for _, bad := range []string{"Pulling image…", "Create"} {
		if textsContain(texts, bad) {
			t.Errorf("镜像页不该有 %q", bad)
		}
	}
}

// TestDemoSceneDeterminesTheFrame 验证画面只取决于场景，与前面画过什么无关。
//
// 这是渲染成视频的前提：视频会先把所有帧构建一遍（混音），预览拖动时还会再构建，
// 同一帧每次必须得到同一个结果。这里让同一个演示实例画 A、B、再画 A，
// 两次 A 的文字必须一样——B 的内容（选中行、输出面板、别的页面）不能漏进来。
func TestDemoSceneDeterminesTheFrame(t *testing.T) {
	d := NewDemo(demoW, demoH)

	a := demoData(PageContainers)
	a.ContainerRow = 1

	b := demoData(PageCompose)
	b.ProjectRow = 0
	b.Output = &DemoOutput{Title: "标题", Text: "line one\nline two", Busy: true}
	b.ComposeBusyDir = `D:\dev\demo`

	first := renderDemo(d, a, demoW, demoH).Texts()
	renderDemo(d, b, demoW, demoH)
	second := renderDemo(d, a, demoW, demoH).Texts()

	if !reflect.DeepEqual(first, second) {
		t.Errorf("同一场景两次画出来不一样：\n第一次: %q\n第二次: %q", first, second)
	}
}

// TestDemoOutputPanel 验证输出面板：标题、正文、忙碌时的行数都按场景显示，场景里没有它就关掉。
func TestDemoOutputPanel(t *testing.T) {
	d := NewDemo(demoW, demoH)
	s := demoData(PageImages)
	s.Output = &DemoOutput{Title: "拉取镜像 · demo", Text: "alpha\nbeta\ngamma", Busy: true}

	tt := renderDemo(d, s, demoW, demoH)
	for _, want := range []string{"拉取镜像 · demo", "alpha", "gamma"} {
		if !tt.HasText(want) {
			t.Errorf("输出面板里没有 %q，现有文字: %q", want, tt.Texts())
		}
	}
	if !d.a.outputBusy || d.a.outputLines != 3 {
		t.Errorf("忙碌 = %v、行数 = %d，应为 true 和 3", d.a.outputBusy, d.a.outputLines)
	}

	// 场景里没有 Output：面板必须关掉，不能把上一帧的内容留下来。
	s.Output = nil
	tt = renderDemo(d, s, demoW, demoH)
	if tt.HasText("alpha") || d.a.outputOpen {
		t.Errorf("场景不带输出面板时它应当关闭；outputOpen=%v", d.a.outputOpen)
	}
}

// TestDemoVirtualMachineStates 验证系统页虚拟机卡片的几种状态都能被场景驱动。
func TestDemoVirtualMachineStates(t *testing.T) {
	useLocale(t, "zh-Hans")
	cases := []struct {
		name   string
		snap   wslvm.Snapshot
		action DemoVMAction
		want   string
	}{
		{"运行中", wslvm.Snapshot{State: wslvm.Running, Session: "s", Memory: 1413 << 20}, DemoVMIdle, locales.VmRunning()},
		{"已停止", wslvm.Snapshot{State: wslvm.Stopped, Session: "s"}, DemoVMIdle, locales.VmStopped()},
		{"正在关闭", wslvm.Snapshot{State: wslvm.Running, Session: "s", Memory: 1413 << 20}, DemoVMStopping, locales.VmStopping()},
		{"正在启动", wslvm.Snapshot{State: wslvm.Stopped, Session: "s"}, DemoVMStarting, locales.VmStarting()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := demoData(PageSystem)
			s.VM, s.VMAction = tc.snap, tc.action
			tt := renderDemo(NewDemo(demoW, demoH), s, demoW, demoH)
			if !tt.HasText(tc.want) {
				t.Errorf("没有看到 %q，现有文字: %q", tc.want, tt.Texts())
			}
		})
	}
}

// TestNewDemoSceneSelectsNothing 验证新场景不选中任何行。
//
// 选中行的零值是第 0 行：直接写 DemoScene{} 的话，每个列表的第一行都会悄悄被选中，
// 按钮的可用状态也跟着变。所以场景必须从 NewDemoScene 起步。
func TestNewDemoSceneSelectsNothing(t *testing.T) {
	s := NewDemoScene()
	for name, row := range map[string]int{
		"container": s.ContainerRow, "image": s.ImageRow, "volume": s.VolumeRow,
		"network": s.NetworkRow, "project": s.ProjectRow, "service": s.ServiceRow,
	} {
		if row != NoRow {
			t.Errorf("%s 的选中行 = %d，应为 NoRow", name, row)
		}
	}
	if s.Page != PageContainers || s.Output != nil {
		t.Errorf("新场景应是容器页且不带输出面板: page=%v output=%v", s.Page, s.Output)
	}
}

// TestDetailWidthUsesWindowSizeNotCanvas 是 detailWidth 的回归测试。
//
// 缺陷现象：宣传片里应用的窗口（比如 960 宽）嵌在更大的画布（比如 1400 宽）中间。
// detailWidth 先问 c.Size()，量到的是整块画布，于是应用以为自己有一千多宽，
// 选了「宽窗口」的布局分支（显示文件名、重启按钮），实际只有 420，
// Compose 项目头部的几个按钮就叠在了一起。
// 现在给了窗口尺寸（sizeOverride）就以它为准，和 clampComposeListWidth 用同一个来源。
func TestDetailWidthUsesWindowSizeNotCanvas(t *testing.T) {
	d := NewDemo(960, 510)
	var got float32
	tt := ui.NewTester(func(c *ui.Context) {
		got = d.a.detailWidth(c)
		d.View(c, demoData(PageCompose))
	}, 1400, 800) // 画布比窗口大得多
	tt.Frame()

	// 窗口宽 - 侧边栏 - 项目列表栏 - 40 的固定余量，用的都是 App 的默认值。
	want := float32(960) - d.a.sidebarW - d.a.composeProjectsWidth - 40
	if got != want {
		t.Errorf("detailWidth = %v，应为 %v（按窗口宽 960 算，不是画布宽 1400）", got, want)
	}
}
