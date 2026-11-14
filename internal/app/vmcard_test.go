package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/wslvm"
	"github.com/arno/wslc-manager/locales"
)

// ---- 测试用的假宿主机 ----

const hostSession = "wslc-cli-Alice"

// fakeHost 模拟宿主机上的虚拟机进程：观测读它，启动和关闭改它。
//
// 它只模拟「宿主机上看得到什么」，不模拟 wslc。各个字段对应真实世界里的一种情形，
// 例如 linger 是 terminate 返回之后虚拟机进程还要多留一会儿才退出——
// 「关闭之后立刻刷新就把虚拟机又叫醒了」这个缺陷就是从这里来的。
type fakeHost struct {
	mu          sync.Mutex
	running     bool
	lingerUntil time.Time

	linger       time.Duration // 关闭之后进程多留多久
	probeErr     error         // 非空：观测失败
	startErr     error
	stopErr      error
	stuck        bool          // 关闭命令成功，但进程一直不退
	neverAppears bool          // 启动成功，但宿主机上看不到进程（进程名对不上）
	blip         time.Duration // 启动时进程只出现这么久，随后又消失（空闲超时设得很短）

	starts, stops int
}

func (h *fakeHost) probe() (wslvm.Snapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := wslvm.Snapshot{Session: hostSession, Process: "vmmem" + hostSession}
	if h.probeErr != nil {
		return s, h.probeErr
	}
	if h.running || time.Now().Before(h.lingerUntil) {
		s.State, s.PID, s.Memory = wslvm.Running, 4242, 474<<20
	} else {
		s.State = wslvm.Stopped
	}
	return s, nil
}

func (h *fakeHost) setRunning(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = on
}

func (h *fakeHost) start(ctx context.Context) error {
	h.mu.Lock()
	h.starts++
	err, blip, never := h.startErr, h.blip, h.neverAppears
	h.mu.Unlock()
	if err != nil {
		return err
	}
	switch {
	case blip > 0:
		h.setRunning(true)
		time.Sleep(blip)
		h.setRunning(false)
	case !never:
		h.setRunning(true)
	}
	return nil
}

func (h *fakeHost) stop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stops++
	if h.stopErr != nil {
		return h.stopErr
	}
	if !h.stuck {
		h.running = false
		h.lingerUntil = time.Now().Add(h.linger)
	}
	return nil
}

func (h *fakeHost) counts() (starts, stops int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.starts, h.stops
}

// install 把假宿主机接到应用上。
func (h *fakeHost) install(a *App) {
	a.vmx.probe, a.vmx.start, a.vmx.stop = h.probe, h.start, h.stop
}

// ---- 公共小工具 ----

// waitUntil 等条件成立（动作和刷新都在后台），超时就失败。
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时：%s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// appIdle 报告应用没有在刷新、也没有虚拟机动作在进行。
//
// 注意它不能用来判断「动作已全部结束」：runAsync 在清掉忙磌标记之后、发起刷新之前，
// 有一小段两者都为假的间隙，那时提示还没加、刷新还没开始。
// 要等动作结束，请用 waitToast（断言提示时）或 waitActionDone（断言刷新结果时）。
func appIdle(a *App) bool {
	loading := false
	a.ViewLocked(func() { loading = a.loading })
	return !loading && !a.vmBusy()
}

// waitToast 等某条提示出现。动作结束时一定会给一条提示（成功或失败），
// 所以它是「动作结束了」最可靠的信号。
func waitToast(t *testing.T, a *App, want string) {
	t.Helper()
	waitUntil(t, "提示 "+want, func() bool { return hasToast(a, want) })
}

// waitActionDone 等动作结束，并且动作结束时触发的那次刷新也结束。
// 只用于带 wslc 的应用（没有 wslc 时 Refresh 直接返回，lastRefresh 不会变）。
func waitActionDone(t *testing.T, a *App, before time.Time) {
	t.Helper()
	waitUntil(t, "动作及其后的刷新都结束", func() bool {
		var refreshed bool
		a.ViewLocked(func() { refreshed = a.lastRefresh.After(before) })
		return refreshed && appIdle(a)
	})
}

// waitToastUI 在界面上等某条提示：驱动界面一帧一帧地渲染，直到提示文字出现。
//
// 有活的 Tester 时不能用 waitToast：每渲染一帧，flushToasts 就会把待弹出的提示取走、
// 变成界面上的提示元素。动作的协程恰好在 Click 收尾的渲染期间加了提示的话，
// pendingToasts 里就再也看不到它了。
//
// 渲染要在 ViewLocked 里进行：生产里应用状态只在主线程读写，测试里 Tester 的渲染不在主线程、
// 也不在 viewMu 下，不加锁就会和动作协程里的 update 构成数据竞争（-race 会报）。
func waitToastUI(t *testing.T, a *App, tt *ui.Tester, want string) {
	t.Helper()
	waitUntil(t, "界面上出现提示 "+want, func() bool {
		found := false
		a.ViewLocked(func() {
			tt.Frame()
			found = tt.HasText(want)
		})
		return found
	})
}

func lastRefreshOf(a *App) time.Time {
	var ts time.Time
	a.ViewLocked(func() { ts = a.lastRefresh })
	return ts
}

func hasToast(a *App, want string) bool {
	found := false
	a.ViewLocked(func() {
		for _, m := range a.pendingToasts {
			if strings.Contains(m, want) {
				found = true
			}
		}
	})
	return found
}

func lastError(a *App) string {
	var s string
	a.ViewLocked(func() { s = a.lastError })
	return s
}

func logHas(a *App, want string) int {
	n := 0
	for _, e := range a.logs.Snapshot() {
		if strings.Contains(e.Message, want) {
			n++
		}
	}
	return n
}

func vmSnap(state wslvm.State) wslvm.Snapshot {
	s := wslvm.Snapshot{State: state, Session: hostSession, Process: "vmmem" + hostSession}
	if state == wslvm.Running {
		s.PID, s.Memory = 4242, 474<<20
	}
	return s
}

// vmPage 造一个打开系统页、虚拟机状态已知的应用。
func vmPage(t *testing.T, state wslvm.State) (*App, *ui.Tester) {
	t.Helper()
	a := newTestApp(t)
	sampleData(a)
	a.page = PageSystem
	a.vmx.snap = vmSnap(state)
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()
	return a, tt
}

func has(tt *ui.Tester, text string) bool {
	_, ok := tt.Find(text)
	return ok
}

func vmButtonDisabled(t *testing.T, tt *ui.Tester, label string) bool {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleButton, label)
	if !ok {
		t.Fatalf("找不到按钮 %q", label)
	}
	return info.Disabled
}

// ---- 卡片 ----

// TestVMCardShowsEachState 验证虚拟机卡片在每种状态下的文字。
func TestVMCardShowsEachState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   wslvm.State
		want    []string
		wantNot []string
	}{
		{"运行中", wslvm.Running,
			[]string{locales.VmTitle(), locales.VmRunning(), "474 MB", hostSession, locales.VmHint()},
			[]string{locales.VmStopped(), locales.VmUnknown()}},
		{"已停止", wslvm.Stopped,
			[]string{locales.VmTitle(), locales.VmStopped(), hostSession},
			[]string{locales.VmRunning(), locales.VmUnknown(), "474 MB"}},
		// 观测不了的时候，卡片得老实说「无法判断」，既不能说运行中也不能说已停止。
		{"无法判断", wslvm.Unknown,
			[]string{locales.VmTitle(), locales.VmUnknown()},
			[]string{locales.VmRunning(), locales.VmStopped()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, tt := vmPage(t, tc.state)
			for _, w := range tc.want {
				if !has(tt, w) && !tt.HasText(w) {
					t.Errorf("卡片缺少 %q", w)
				}
			}
			for _, w := range tc.wantNot {
				if has(tt, w) {
					t.Errorf("卡片不该出现 %q", w)
				}
			}
		})
	}
}

// TestVMCardButtons 验证启动/关闭按钮什么时候可点。
//
// 已停止时「关闭」没有意义，运行时「启动」没有意义，进行中两个都不能点（否则连点两下会重复发命令）。
// 观测不了的时候两个都开着：不知道状态，就别替用户决定哪个不能点。
func TestVMCardButtons(t *testing.T) {
	for _, tc := range []struct {
		name           string
		state          wslvm.State
		busyKey        string
		startOff, stop bool // 期望「启动」「关闭」是否被禁用
	}{
		{"运行中", wslvm.Running, "", true, false},
		{"已停止", wslvm.Stopped, "", false, true},
		{"无法判断", wslvm.Unknown, "", false, false},
		{"正在启动", wslvm.Stopped, vmStartKey, true, true},
		{"正在关闭", wslvm.Running, vmStopKey, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			sampleData(a)
			a.page = PageSystem
			a.vmx.snap = vmSnap(tc.state)
			if tc.busyKey != "" {
				a.setBusy(tc.busyKey, true)
			}
			tt := ui.NewTester(a.View, 1180, 760)
			tt.Frame()
			if got := vmButtonDisabled(t, tt, locales.VmStart()); got != tc.startOff {
				t.Errorf("「启动」禁用=%v，期望 %v", got, tc.startOff)
			}
			if got := vmButtonDisabled(t, tt, locales.VmStop()); got != tc.stop {
				t.Errorf("「关闭」禁用=%v，期望 %v", got, tc.stop)
			}
		})
	}
}

// 进行中的状态文字：启动与关闭要能分开看出来。
func TestVMCardShowsProgress(t *testing.T) {
	for key, want := range map[string]string{vmStartKey: locales.VmStarting(), vmStopKey: locales.VmStopping()} {
		a := newTestApp(t)
		sampleData(a)
		a.page = PageSystem
		a.vmx.snap = vmSnap(wslvm.Stopped)
		a.setBusy(key, true)
		tt := ui.NewTester(a.View, 1180, 760)
		tt.Frame()
		if !has(tt, want) {
			t.Errorf("%s 进行中，卡片应显示 %q", key, want)
		}
	}
}

// TestVMCardLayout 用数字验证卡片布局，不靠肉眼。
//
// 虚拟机卡片排在系统页最上面，最容易出的版式问题：把下面的卡片挤出窗口、
// 按钮高度为 0（两个元素抢空间）、状态行里标签和文字没对齐、卡片压到下一张卡片上。
func TestVMCardLayout(t *testing.T) {
	const W, H = 1180, 760
	_, tt := vmPage(t, wslvm.Running)

	rect := func(text string) ui.Rect {
		t.Helper()
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("找不到 %q", text)
		}
		if r.W <= 0 || r.H <= 0 {
			t.Fatalf("%q 尺寸为 %.1f×%.1f，没有被布局出来", text, r.W, r.H)
		}
		return r
	}
	title := rect(locales.VmTitle())
	status := rect(locales.VmStatus())
	state := rect(locales.VmRunning())
	session := rect(locales.VmSession())
	memory := rect(locales.VmMemory())
	mem := rect("474 MB")
	hint := rect(locales.VmHint())
	next := rect(locales.UpdateTitle()) // 紧挨着的下一张卡片

	btns := map[string]ui.Rect{}
	for _, label := range []string{locales.VmStart(), locales.VmStop()} {
		info, ok := tt.AccessByRole(ui.RoleButton, label)
		if !ok || info.Bounds.H <= 0 || info.Bounds.W <= 0 {
			t.Fatalf("按钮 %q 没有被布局出来: %+v", label, info.Bounds)
		}
		btns[label] = info.Bounds
	}

	// 从上到下：标题 < 状态 < 会话 < 内存 < 按钮 < 提示 < 下一张卡片。
	order := []struct {
		name string
		y    float32
	}{
		{"标题", title.Y}, {"状态行", status.Y}, {"会话行", session.Y}, {"内存行", memory.Y},
		{"按钮", btns[locales.VmStart()].Y}, {"提示", hint.Y}, {"下一张卡片的标题", next.Y},
	}
	for i := 1; i < len(order); i++ {
		if order[i-1].y >= order[i].y {
			t.Errorf("%s(y=%.1f) 应当在 %s(y=%.1f) 之上", order[i-1].name, order[i-1].y, order[i].name, order[i].y)
		}
	}
	if hint.Y+hint.H >= next.Y {
		t.Errorf("提示的底边 %.1f 压到了下一张卡片的标题 %.1f", hint.Y+hint.H, next.Y)
	}

	// 两个按钮在同一行、互不重叠。
	s, p := btns[locales.VmStart()], btns[locales.VmStop()]
	if d := s.Y - p.Y; d < -1.5 || d > 1.5 {
		t.Errorf("两个按钮不在同一行: y=%.1f / %.1f", s.Y, p.Y)
	}
	if s.X+s.W > p.X && p.X+p.W > s.X {
		t.Errorf("两个按钮重叠了: %+v %+v", s, p)
	}

	// 状态行的标签和文字、内存行的标签和数字，竖向中线要对齐。
	for name, pair := range map[string][2]ui.Rect{"状态行": {status, state}, "内存行": {memory, mem}} {
		if d := (pair[0].Y + pair[0].H/2) - (pair[1].Y + pair[1].H/2); d < -2 || d > 2 {
			t.Errorf("%s 的标签与内容竖向没对齐：中线相差 %.1f", name, d)
		}
	}
	for name, r := range map[string]ui.Rect{"标题": title, "按钮": s, "提示": hint} {
		if r.X < 0 || r.Y < 0 || r.X+r.W > W || r.Y+r.H > H {
			t.Errorf("%s 超出窗口: %+v", name, r)
		}
	}
}

// ---- 关闭前的确认 ----

// TestVMStopAsksBeforeStopping 验证关闭要先确认，并且确认框说清楚会终止哪些容器。
//
// 关闭虚拟机会终止里面所有运行中的容器——用户的服务可能正跑着，
// 一次手滑的点击不能直接把它们全杀了。
func TestVMStopAsksBeforeStopping(t *testing.T) {
	a, tt := vmPage(t, wslvm.Running)
	h := &fakeHost{running: true}
	h.install(a)

	// sampleData 里有一个运行中的容器 demo-web-1 和一个已退出的 legacy。
	if err := tt.Click(locales.VmStop()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if want := locales.VmStopConfirmBodyRunning(1, "demo-web-1"); !tt.HasText(want) {
		t.Fatalf("确认框应写明要终止的容器：%q", want)
	}
	if tt.HasText("legacy") {
		t.Error("确认框不该把已退出的容器也列进去")
	}
	if _, stops := h.counts(); stops != 0 {
		t.Fatal("还没确认就关闭了")
	}

	// 取消：什么都不发生。
	if err := tt.Click(locales.CommonCancel()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, stops := h.counts(); stops != 0 {
		t.Fatal("点了取消却关闭了")
	}

	// 再来一次，这次确认。
	if err := tt.Click(locales.VmStop()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// 从这一下点击起，动作的协程会和界面渲染并行地改应用状态，点击也要在 ViewLocked 里。
	var clickErr error
	a.ViewLocked(func() { clickErr = tt.Click(locales.CommonConfirm()) })
	if clickErr != nil {
		t.Fatal(clickErr)
	}
	waitToastUI(t, a, tt, locales.VmStoppedToast())
	if _, stops := h.counts(); stops != 1 {
		t.Errorf("确认之后应恰好关闭一次，实际 %d 次", stops)
	}
}

func TestVMStopConfirmWithoutRunningContainers(t *testing.T) {
	a, tt := vmPage(t, wslvm.Running)
	for i := range a.containers {
		a.containers[i].State = "exited"
	}
	if err := tt.Click(locales.VmStop()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(locales.VmStopConfirmBody()) {
		t.Errorf("没有运行中的容器时，确认框应说明这一点：%q", locales.VmStopConfirmBody())
	}
}

func TestBriefNames(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"a"}, "a"},
		{[]string{"a", "b", "c"}, "a, b, c"},
		{[]string{"a", "b", "c", "d", "e"}, "a, b, c …"},
	} {
		if got := briefNames(tc.names, 3); got != tc.want {
			t.Errorf("briefNames(%v) = %q，期望 %q", tc.names, got, tc.want)
		}
	}
}

// ---- 资源页在虚拟机没运行时的提示 ----

// TestVMStoppedPane 验证数据住在虚拟机里的几个页面，在虚拟机确定没运行时给出提示和启动入口。
//
// 防的是两个相反的错误：一是照常显示一个空列表（用户以为自己没有容器和镜像，其实只是虚拟机关着）；
// 二是把不该拦的页面也拦了（系统页要靠它启动虚拟机，Compose 页只读本地文件）。
func TestVMStoppedPane(t *testing.T) {
	for _, tc := range []struct {
		page     Page
		state    wslvm.State
		wantPane bool
	}{
		{PageContainers, wslvm.Stopped, true},
		{PageImages, wslvm.Stopped, true},
		{PageVolumes, wslvm.Stopped, true},
		{PageNetworks, wslvm.Stopped, true},
		{PageCompose, wslvm.Stopped, false},
		{PageSystem, wslvm.Stopped, false},
		// 运行中、或者观测不了，都不能拦：不知道，不等于没在跑。
		{PageContainers, wslvm.Running, false},
		{PageContainers, wslvm.Unknown, false},
		{PageImages, wslvm.Unknown, false},
	} {
		a := newTestApp(t)
		sampleData(a)
		a.page = tc.page
		a.vmx.snap = vmSnap(tc.state)
		tt := ui.NewTester(a.View, 1180, 760)
		tt.Frame()
		if got := has(tt, locales.VmPaneTitle()); got != tc.wantPane {
			t.Errorf("页面 %d、状态 %v：提示出现=%v，期望 %v", tc.page, tc.state, got, tc.wantPane)
		}
	}
}

func TestVMStoppedPaneStartsTheVM(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageImages
	a.vmx.snap = vmSnap(wslvm.Stopped)
	h := &fakeHost{}
	h.install(a)
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	var clickErr error
	a.ViewLocked(func() { clickErr = tt.Click(locales.VmStart()) })
	if clickErr != nil {
		t.Fatalf("停止页里应有「启动」按钮: %v", clickErr)
	}
	waitToastUI(t, a, tt, locales.VmStartedToast()) // 启动成功后应有提示
	if starts, _ := h.counts(); starts != 1 {
		t.Errorf("应恰好启动一次，实际 %d 次", starts)
	}
}

func TestVMStoppedPaneShowsProgress(t *testing.T) {
	a := newTestApp(t)
	a.page = PageContainers
	a.vmx.snap = vmSnap(wslvm.Stopped)
	a.setBusy(vmStartKey, true)
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()
	if !has(tt, locales.VmStarting()) {
		t.Error("启动中应显示进度文字")
	}
	if has(tt, locales.VmStart()) {
		t.Error("启动中不该再给「启动」按钮，否则能连点")
	}
}

// TestVMStoppedPaneHidesCountBadge 验证虚拟机没运行时，标题旁不显示数量徽标。
//
// 防的是误导：停着的时候列表是空的，徽标会写「0」，读起来是「没有容器」，
// 实际是「不知道」——容器都在虚拟机里。真实窗口里就看到过标题旁挂着一个 0。
// 对照组：观测不了（Unknown）时列表为空是真的没数据，徽标照常显示。
func TestVMStoppedPaneHidesCountBadge(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     wslvm.State
		wantBadge bool
	}{
		{"已停止", wslvm.Stopped, false},
		{"无法判断", wslvm.Unknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			a.page = PageContainers
			a.containers = nil
			a.vmx.snap = vmSnap(tc.state)
			tt := ui.NewTester(a.View, 1180, 760)
			tt.Frame()
			if got := has(tt, "0"); got != tc.wantBadge {
				t.Errorf("数量徽标出现=%v，期望 %v", got, tc.wantBadge)
			}
		})
	}
}

// ---- 动作的结果 ----

// TestStartFailureSurfaces 验证启动失败会让用户看到，而不是悄悄没反应。
func TestStartFailureSurfaces(t *testing.T) {
	a := newTestApp(t)
	h := &fakeHost{startErr: errors.New("virtualization is not enabled")}
	h.install(a)
	a.startVM()
	waitToast(t, a, "操作失败")
	if !hasToast(a, "virtualization is not enabled") {
		t.Error("失败原因应交给用户")
	}
	// 没有 wslc 时动作结束后不会刷新，所以 lastError 不会被清掉，这里可以检查。
	if got := lastError(a); !strings.Contains(got, "virtualization is not enabled") {
		t.Errorf("lastError=%q", got)
	}
}

// ---- 小函数 ----

func TestFormatMemory(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		in   uint64
		want string
	}{
		{0, "—"},
		{474 * mib, "474 MB"},
		{1023 * mib, "1023 MB"},
		{1024 * mib, "1.00 GB"},
		{1536 * mib, "1.50 GB"},
		{16 << 30, "16.00 GB"},
	} {
		if got := formatMemory(tc.in); got != tc.want {
			t.Errorf("formatMemory(%d) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestVMSummary(t *testing.T) {
	if got := vmSummary(vmSnap(wslvm.Stopped)); got != locales.VmStopped() {
		t.Errorf("已停止 = %q", got)
	}
	if got := vmSummary(vmSnap(wslvm.Unknown)); got != locales.VmUnknown() {
		t.Errorf("无法判断 = %q", got)
	}
	got := vmSummary(vmSnap(wslvm.Running))
	for _, want := range []string{locales.VmRunning(), "474 MB", "vmmem" + hostSession, "4242"} {
		if !strings.Contains(got, want) {
			t.Errorf("运行中的摘要 %q 缺少 %q", got, want)
		}
	}
}

// TestDiagnosticReportShowsVM 验证诊断报告写明虚拟机状态。
//
// 「容器列表是空的」「点了没反应」这类反馈，第一件事就是确认虚拟机有没有在运行。
func TestDiagnosticReportShowsVM(t *testing.T) {
	a := newTestApp(t)
	a.vmx.snap = vmSnap(wslvm.Running)
	report, err := a.BuildDiagnostic(DiagOptions{})
	if err != nil {
		t.Fatal(err)
	}
	line := "**" + locales.VmTitle() + "**: " + vmSummary(a.vmx.snap)
	if !strings.Contains(report, line) {
		t.Errorf("报告里应有 %q", line)
	}
}
