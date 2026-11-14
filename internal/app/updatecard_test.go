package app

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/selfupdate"
	"github.com/arno/wslc-manager/locales"
)

// scriptedStatus 是假更新器 Status 的一次预设回答。
type scriptedStatus struct {
	st   selfupdate.Status
	wait chan struct{} // 非 nil 时，先等它被关闭才返回
}

// fakeUpdater 是 selfupdate.Controller 的假实现：
// 不联网、不弹窗，只记录界面对它做了什么，并能主动发出「状态变了」的通知。
type fakeUpdater struct {
	mu     sync.Mutex
	status selfupdate.Status
	// script 非空时，Status 依次取用其中的回答，用尽后回到 status。
	script []scriptedStatus

	checks      int
	autoCheck   []bool
	autoInstall []bool

	subs map[int]func()
	next int

	inflight sync.WaitGroup // 还没返回的 Status 调用
}

func newFakeUpdater(st selfupdate.Status) *fakeUpdater { return &fakeUpdater{status: st} }

func (f *fakeUpdater) Status() selfupdate.Status {
	f.inflight.Add(1)
	defer f.inflight.Done()

	f.mu.Lock()
	st, wait := f.status, chan struct{}(nil)
	if len(f.script) > 0 {
		st, wait = f.script[0].st, f.script[0].wait
		f.script = f.script[1:]
	}
	f.mu.Unlock()

	if wait != nil {
		<-wait
	}
	return st
}

func (f *fakeUpdater) CheckNow() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
}

func (f *fakeUpdater) SetAutoCheck(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoCheck = append(f.autoCheck, on)
}

func (f *fakeUpdater) SetAutoInstall(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoInstall = append(f.autoInstall, on)
}

func (f *fakeUpdater) OnChange(fn func()) (off func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subs == nil {
		f.subs = map[int]func(){}
	}
	id := f.next
	f.next++
	f.subs[id] = fn
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.subs, id)
	}
}

// set 换掉 Status 的回答。
func (f *fakeUpdater) set(st selfupdate.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = st
}

// fire 通知所有订阅者「状态变了」，就像插件检查完一次之后那样。
func (f *fakeUpdater) fire() {
	f.mu.Lock()
	fns := make([]func(), 0, len(f.subs))
	for _, fn := range f.subs {
		fns = append(fns, fn)
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (f *fakeUpdater) subscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

func (f *fakeUpdater) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

func (f *fakeUpdater) autoCheckCalls() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.autoCheck...)
}

func (f *fakeUpdater) autoInstallCalls() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.autoInstall...)
}

// snapshot 在视图锁下读出更新状态快照。
func snapshot(a *App) selfupdate.Status {
	var st selfupdate.Status
	a.ViewLocked(func() { st = a.upd })
	return st
}

// waitSnapshot 等快照满足条件（刷新是后台进行的），超时就失败。
func waitSnapshot(t *testing.T, a *App, cond func(selfupdate.Status) bool) selfupdate.Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := snapshot(a)
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待更新状态超时，当前快照 %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// updateApp 造一个系统页打开、更新状态已知的应用。
func updateApp(t *testing.T, st selfupdate.Status) (*App, *fakeUpdater) {
	t.Helper()
	a := newTestApp(t)
	sampleData(a)
	a.page = PageSystem
	f := newFakeUpdater(st)
	a.selfUpdate = f
	a.upd = st
	return a, f
}

var checkedAt = time.Date(2026, 11, 14, 10, 30, 0, 0, time.Local)

// TestUpdateCardShowsEachState 验证更新卡片在每种状态下的文案。
//
// 防的是这些误导：没成功检查过就声称「已是最新」；不支持自更新的构建
// 只字不提原因；有新版本时按钮还叫「检查更新」，看不出该点它去更新。
func TestUpdateCardShowsEachState(t *testing.T) {
	cases := []struct {
		name    string
		st      selfupdate.Status
		want    []string // 必须出现的文字
		wantNot []string // 不能出现的文字
	}{
		{
			name: "已是最新",
			st:   selfupdate.Status{Version: "0.2.0", Supported: true, LastCheck: checkedAt, AutoCheck: true},
			want: []string{
				locales.UpdateTitle(), "0.2.0", locales.UpdateUpToDate(),
				locales.UpdateLastCheck(), "2026-11-14 10:30", locales.UpdateCheckNow(),
			},
			wantNot: []string{locales.UpdateUnsupported(), locales.UpdateNeverChecked()},
		},
		{
			name: "有新版本",
			st:   selfupdate.Status{Version: "0.2.0", Supported: true, LastCheck: checkedAt, Available: "0.3.0"},
			want: []string{locales.UpdateAvailable("0.3.0"), locales.UpdateViewAvailable("0.3.0")},
			// 有新版本时主按钮已经换了文案，不该还留着「检查更新」。
			wantNot: []string{locales.UpdateUpToDate(), locales.UpdateCheckNow()},
		},
		{
			name:    "不支持自更新",
			st:      selfupdate.Status{Version: "0.2.0", Supported: false},
			want:    []string{locales.UpdateUnsupported(), locales.UpdateUnsupportedHint(), locales.UpdateCheckNow()},
			wantNot: []string{locales.UpdateUpToDate()},
		},
		{
			name: "从未成功检查",
			st:   selfupdate.Status{Version: "0.2.0", Supported: true},
			want: []string{locales.UpdateNeverChecked(), locales.UpdateCheckNow()},
			// 从没检查过，既不能说「已是最新」，也没有「上次检查」可显示。
			wantNot: []string{locales.UpdateUpToDate(), locales.UpdateLastCheck()},
		},
		{
			name: "没有版本号显示 dev",
			st:   selfupdate.Status{Supported: true, LastCheck: checkedAt},
			want: []string{"dev"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := updateApp(t, tc.st)
			tt := ui.NewTester(a.View, 1180, 760)
			tt.Frame()
			for _, w := range tc.want {
				if !tt.HasText(w) {
					t.Errorf("更新卡片缺少 %q", w)
				}
			}
			for _, w := range tc.wantNot {
				if tt.HasText(w) {
					t.Errorf("更新卡片不该出现 %q", w)
				}
			}
		})
	}
}

// TestUpdateCardButtonStartsCheck 验证点击按钮会让插件立即检查。
//
// 有新版本时主按钮同样走 CheckNow：插件的窗口里有更新内容和「立即安装」，
// 应用这边不另做一套安装界面。
func TestUpdateCardButtonStartsCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		st    selfupdate.Status
		label string
	}{
		{"检查更新", selfupdate.Status{Supported: true, LastCheck: checkedAt}, locales.UpdateCheckNow()},
		{"查看新版本", selfupdate.Status{Supported: true, Available: "0.3.0"}, locales.UpdateViewAvailable("0.3.0")},
		{"不支持时也能点", selfupdate.Status{Supported: false}, locales.UpdateCheckNow()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f := updateApp(t, tc.st)
			tt := ui.NewTester(a.View, 1180, 760)
			tt.Frame()

			if err := tt.Click(tc.label); err != nil {
				t.Fatalf("点击 %q 失败: %v", tc.label, err)
			}
			tt.Frame()
			if n := f.checkCount(); n != 1 {
				t.Errorf("应恰好触发 1 次检查，实际 %d 次", n)
			}
		})
	}
}

// TestUpdateCardLayout 用数字验证卡片布局，不靠肉眼。
//
// 更新卡片放在系统页最上面，最容易出的版式问题：卡片被推出窗口、
// 按钮高度为 0（两个元素抢空间）、状态行里标签和文字没对齐。
func TestUpdateCardLayout(t *testing.T) {
	const W, H = 1180, 760
	a, _ := updateApp(t, selfupdate.Status{Version: "0.2.0", Supported: true, LastCheck: checkedAt})
	tt := ui.NewTester(a.View, W, H)
	tt.Frame()

	inside := func(name string, r ui.Rect) {
		t.Helper()
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("%s 尺寸为 %.1f×%.1f，没有被布局出来", name, r.W, r.H)
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > W || r.Y+r.H > H {
			t.Errorf("%s 超出窗口：%+v", name, r)
		}
	}

	title, ok := tt.Find(locales.UpdateTitle())
	if !ok {
		t.Fatal("找不到卡片标题")
	}
	inside("卡片标题", title)

	btn, ok := tt.Find(locales.UpdateCheckNow())
	if !ok {
		t.Fatal("找不到「检查更新」按钮")
	}
	inside("检查更新按钮", btn)

	// 状态行：标签和说明的竖向中线要对齐（AlignItems(Center)），差超过 1.5 就是错位。
	label, ok1 := tt.Find(locales.UpdateStatus())
	value, ok2 := tt.Find(locales.UpdateUpToDate())
	if !ok1 || !ok2 {
		t.Fatalf("找不到状态行：标签=%v 说明=%v", ok1, ok2)
	}
	inside("状态标签", label)
	inside("状态说明", value)
	if d := (label.Y + label.H/2) - (value.Y + value.H/2); d < -1.5 || d > 1.5 {
		t.Errorf("状态标签与说明竖向没对齐：中线相差 %.1f", d)
	}

	// 卡片自上而下的顺序：标题 < 状态行 < 按钮。
	if !(title.Y < label.Y && label.Y < btn.Y) {
		t.Errorf("卡片内顺序不对：标题 y=%.1f 状态 y=%.1f 按钮 y=%.1f", title.Y, label.Y, btn.Y)
	}
}

// TestSidebarBadgeOnlyWhenUpdateAvailable 验证侧边栏「系统信息」上的徽标。
//
// 徽标是发现新版本的提示：没有新版本时不能出现，否则用户会白跑一趟。
func TestSidebarBadgeOnlyWhenUpdateAvailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   selfupdate.Status
		want bool
	}{
		{"没有新版本", selfupdate.Status{Supported: true, LastCheck: checkedAt}, false},
		{"有新版本", selfupdate.Status{Supported: true, Available: "0.3.0"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := updateApp(t, tc.st)
			a.page = PageContainers // 徽标在侧边栏，任何页面都看得到
			tt := ui.NewTester(a.View, 1180, 760)
			tt.Frame()

			badge, found := tt.Find(locales.UpdateBadge())
			if found != tc.want {
				t.Fatalf("徽标出现=%v，期望 %v", found, tc.want)
			}
			if found {
				nav, ok := tt.Find(locales.NavSystem())
				if !ok {
					t.Fatal("找不到「系统信息」导航项")
				}
				// 徽标和导航文字在同一行：竖向中线相差不能大。
				if d := (badge.Y + badge.H/2) - (nav.Y + nav.H/2); d < -6 || d > 6 {
					t.Errorf("徽标没落在「系统信息」这一行上：中线相差 %.1f", d)
				}
			}
		})
	}
}

// TestSettingsUpdateSwitches 验证设置对话框里的两个自动更新开关。
func TestSettingsUpdateSwitches(t *testing.T) {
	open := func(t *testing.T, st selfupdate.Status) (*App, *fakeUpdater, *ui.Tester) {
		a, f := updateApp(t, st)
		a.settingsOpen = true
		tt := ui.NewTester(a.View, 1180, 760)
		tt.Frame()
		return a, f, tt
	}
	center := func(r ui.Rect) (float32, float32) { return r.X + r.W/2, r.Y + r.H/2 }

	t.Run("切换后转给插件", func(t *testing.T) {
		a, f, tt := open(t, selfupdate.Status{Supported: true, AutoCheck: true, AutoInstall: false})

		check, ok := tt.AccessByRole(ui.RoleSwitch, locales.SettingsAutoCheckUpdates())
		if !ok || !check.Checked {
			t.Fatalf("「自动检查更新」开关应存在且处于打开：found=%v %+v", ok, check)
		}
		install, ok := tt.AccessByRole(ui.RoleSwitch, locales.SettingsAutoInstallUpdates())
		if !ok || install.Checked {
			t.Fatalf("「自动安装更新」开关应存在且处于关闭：found=%v %+v", ok, install)
		}

		tt.ClickAt(center(check.Bounds))
		tt.Frame()
		if got := f.autoCheckCalls(); len(got) != 1 || got[0] != false {
			t.Errorf("关掉「自动检查」应调用 SetAutoCheck(false) 一次，实际 %v", got)
		}
		if snapshot(a).AutoCheck {
			t.Error("点击之后快照里的 AutoCheck 应已变为 false")
		}

		tt.ClickAt(center(install.Bounds))
		tt.Frame()
		if got := f.autoInstallCalls(); len(got) != 1 || got[0] != true {
			t.Errorf("打开「自动安装」应调用 SetAutoInstall(true) 一次，实际 %v", got)
		}
		// 两个开关互不影响。
		if got := f.autoCheckCalls(); len(got) != 1 {
			t.Errorf("点第二个开关不该再触发 SetAutoCheck，实际 %v", got)
		}
	})

	t.Run("不支持自更新时置灰并说明原因", func(t *testing.T) {
		_, f, tt := open(t, selfupdate.Status{Supported: false, AutoCheck: true})

		check, ok := tt.AccessByRole(ui.RoleSwitch, locales.SettingsAutoCheckUpdates())
		if !ok {
			t.Fatal("找不到「自动检查更新」开关")
		}
		if !check.Disabled {
			t.Error("不支持自更新的构建里，开关应该置灰")
		}
		tt.ClickAt(center(check.Bounds))
		tt.Frame()
		if got := f.autoCheckCalls(); len(got) != 0 {
			t.Errorf("置灰的开关不该调用插件，实际 %v", got)
		}
		if !tt.HasText(locales.UpdateUnsupported()) {
			t.Error("置灰时开关下面应写明原因")
		}
		if tt.HasText(locales.SettingsAutoCheckUpdatesHint()) {
			t.Error("置灰时不该照常讲「会怎样」，那些行为根本不会发生")
		}
	})
}

// TestWatchUpdatesFollowsChanges 验证订阅：启动取一次快照，之后跟着插件的通知走。
func TestWatchUpdatesFollowsChanges(t *testing.T) {
	a := newTestApp(t)
	f := newFakeUpdater(selfupdate.Status{Version: "0.2.0", Supported: true})
	a.selfUpdate = f

	a.WatchUpdates()
	waitSnapshot(t, a, func(st selfupdate.Status) bool { return st.Version == "0.2.0" && st.Supported })

	// 插件检查到新版本，通知应用。
	f.set(selfupdate.Status{Version: "0.2.0", Supported: true, Available: "0.3.0", LastCheck: checkedAt})
	f.fire()
	st := waitSnapshot(t, a, func(st selfupdate.Status) bool { return st.Available == "0.3.0" })
	if !st.LastCheck.Equal(checkedAt) {
		t.Errorf("快照里的上次检查时间不对: %v", st.LastCheck)
	}

	// 重复调用不能重复订阅，否则一次变化会触发两次刷新。
	a.WatchUpdates()
	if n := f.subscribers(); n != 1 {
		t.Errorf("重复 WatchUpdates 后应只有 1 个订阅，实际 %d", n)
	}
}

// TestRefreshUpdateStatusDropsStaleResult 验证晚到的旧结果不会盖掉新状态。
//
// 回归测试：后台读状态的完成顺序没有保证。连续两次刷新时，先发起的那次
// 如果后完成，就会把较旧的状态写回快照，界面上「有没有新版本」与插件
// 实际状态相反。现在每次刷新带编号，只接受最新一次的结果。
func TestRefreshUpdateStatusDropsStaleResult(t *testing.T) {
	a := newTestApp(t)
	f := newFakeUpdater(selfupdate.Status{})
	a.selfUpdate = f

	gate := make(chan struct{})
	old := selfupdate.Status{Version: "0.2.0", Supported: true}
	fresh := selfupdate.Status{Version: "0.2.0", Supported: true, Available: "0.3.0"}
	f.script = []scriptedStatus{{st: old, wait: gate}, {st: fresh}}

	a.refreshUpdateStatus() // 第一次：读到旧状态，但被挡住，迟迟不返回
	a.refreshUpdateStatus() // 第二次：立刻读到新状态
	waitSnapshot(t, a, func(st selfupdate.Status) bool { return st.Available == "0.3.0" })

	close(gate)       // 现在放行第一次，它带着旧状态姗姗来迟
	f.inflight.Wait() // 等两次读取都返回
	time.Sleep(50 * time.Millisecond)

	if got := snapshot(a); got.Available != "0.3.0" {
		t.Errorf("晚到的旧结果把新状态盖掉了：%+v", got)
	}
}

// TestDiagnosticReportShowsRealVersion 验证诊断报告用真实版本号。
//
// 回归测试：报告里的版本号曾经是写死的常量 "0.1.0"。发布流程只会改 mygo.json，
// 新版发出去之后报告还在报 0.1.0，排查用户问题时会被误导。
func TestDiagnosticReportShowsRealVersion(t *testing.T) {
	old := versionSource
	t.Cleanup(func() { versionSource = old })

	for _, tc := range []struct{ src, want string }{
		{"9.8.7", "9.8.7"},
		{"", "dev"}, // 没打包的运行没有版本号
	} {
		versionSource = func() string { return tc.src }

		a := newTestApp(t)
		a.upd = selfupdate.Status{Supported: true, Available: "9.9.9"}
		report, err := a.BuildDiagnostic(DiagOptions{})
		if err != nil {
			t.Fatalf("生成报告失败: %v", err)
		}
		line := "**" + locales.SystemVersion() + "**: " + tc.want
		if !strings.Contains(report, line) {
			t.Errorf("版本源=%q：报告里应有 %q", tc.src, line)
		}
		if tc.src == "9.8.7" && strings.Contains(report, "0.1.0") {
			t.Error("报告里不该再出现写死的 0.1.0")
		}
		if !strings.Contains(report, locales.UpdateAvailable("9.9.9")) {
			t.Error("报告里应写明更新状态")
		}
		if got := a.diagPayload(DiagOptions{})["version"]; got != tc.want {
			t.Errorf("结构化部分的 version = %v，期望 %q", got, tc.want)
		}
	}
}

// TestClassifyUpdate 验证状态归类的优先级。
func TestClassifyUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   selfupdate.Status
		want updateState
	}{
		{"不支持排第一", selfupdate.Status{Supported: false, Available: "1.0.0", LastCheck: checkedAt}, updateUnsupported},
		{"有新版本", selfupdate.Status{Supported: true, Available: "1.0.0", LastCheck: checkedAt}, updateAvailable},
		{"从未检查", selfupdate.Status{Supported: true}, updateNever},
		{"已是最新", selfupdate.Status{Supported: true, LastCheck: checkedAt}, updateCurrent},
	} {
		if got := classifyUpdate(tc.st); got != tc.want {
			t.Errorf("%s: 得到 %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestVersionOrDev 验证空版本号显示成 dev。
func TestVersionOrDev(t *testing.T) {
	for in, want := range map[string]string{"": "dev", "  ": "dev", "0.2.0": "0.2.0"} {
		if got := versionOrDev(in); got != want {
			t.Errorf("versionOrDev(%q) = %q，期望 %q", in, got, want)
		}
	}
}
