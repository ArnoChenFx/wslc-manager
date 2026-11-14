//go:build windows

package app

// 这个文件测「虚拟机门控」：用假 wslc 记录应用发出了哪些命令，
// 断言虚拟机没运行时，应用不会发任何会把它叫醒的命令。

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arno/wslc-manager/internal/wslc/wslctest"
	"github.com/arno/wslc-manager/internal/wslvm"
	"github.com/arno/wslc-manager/locales"
)

// gateReplies 是假 wslc 的预设回答：一个运行中的容器，以及会话管理器那边的信息。
func gateReplies() []wslctest.Reply {
	return []wslctest.Reply{
		{Prefix: "system session list", Stdout: "ID        创建者 PID   显示名称\r\n1         57024     " + hostSession + "\r\n"},
		{Prefix: "info", Stdout: "客户端:\r\nWSL 版本: 3.0.5.0\r\n内核版本: 6.18.58.1-1\r\n\r\n服务器:\r\n会话管理器版本: 3.0.5\r\n"},
		{Prefix: "--version", Stdout: "wslc 3.0.5.0\r\n"},
		{Prefix: "list", Stdout: `{"ID":"b8f0e4f2626c","Names":"ocd","Image":"open-compute/ocd:v0","State":"running","Status":"Up 6 hours"}` + "\r\n"},
	}
}

// gateApp 造一个接了假 wslc 和假宿主机的应用。
func gateApp(t *testing.T, h *fakeHost) (*App, *wslctest.Fake) {
	t.Helper()
	f := wslctest.New(t, gateReplies()...)
	a := newTestApp(t)
	a.wslc = f.Client
	h.install(a)
	return a, f
}

// vmBoundCalls 返回发给 wslc 的、会把虚拟机叫醒的那些命令。
//
// 反过来列：只有 info、system session list、--version 这三类只问会话管理器、
// 不碰虚拟机（已对照 WSL 源码核实），其余一律算「会叫醒虚拟机」。
// 这样以后谁在后台刷新里加了新的 wslc 调用，这个测试会自动把它抓出来，
// 逼着加的人先想清楚它会不会叫醒虚拟机。
func vmBoundCalls(f *wslctest.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		l := strings.ToLower(c)
		if strings.HasPrefix(l, "info") || strings.HasPrefix(l, "system session list") || strings.HasPrefix(l, "--version") {
			continue
		}
		out = append(out, c)
	}
	return out
}

func refreshAndWait(t *testing.T, a *App) {
	t.Helper()
	a.Refresh()
	waitUntil(t, "刷新结束", func() bool { return appIdle(a) })
}

func stateOf(a *App) wslvm.State {
	var s wslvm.State
	a.ViewLocked(func() { s = a.vmx.snap.State })
	return s
}

// TestRefreshDoesNotWakeStoppedVM 是整个功能最重要的回归测试。
//
// 缺陷：容器、镜像、卷、网络的查询要和虚拟机里的 Docker 引擎对话，虚拟机没运行时，
// 发出去的第一条命令就会把它建起来。而每个动作结束都会刷新一次——
// 用户点「关闭虚拟机」，刷新紧跟着一条 list，虚拟机立刻又起来了，「关闭」等于没做；
// 虚拟机因空闲自己关掉后，下一次刷新也会把它叫醒。
//
// 这里让宿主机报告「没在运行」，然后刷新：不得发出任何会叫醒虚拟机的命令，
// 但只读会话管理器的信息（版本、info、会话列表）仍要刷新，系统页不能因此变空。
func TestRefreshDoesNotWakeStoppedVM(t *testing.T) {
	a, f := gateApp(t, &fakeHost{})
	// 先放一份旧数据，确认虚拟机停了之后它们会被清掉，而不是继续显示过期的内容。
	sampleData(a)

	refreshAndWait(t, a)

	if calls := vmBoundCalls(f); len(calls) != 0 {
		t.Errorf("虚拟机没在运行，却发出了会叫醒它的命令: %q", calls)
	}
	for _, prefix := range []string{"info", "system session list", "--version"} {
		if f.Count(prefix) != 1 {
			t.Errorf("%q 应当仍然刷新一次（它们不会叫醒虚拟机），实际 %d 次: %q", prefix, f.Count(prefix), f.Calls())
		}
	}

	a.ViewLocked(func() {
		if len(a.containers)+len(a.images)+len(a.volumes)+len(a.networks) != 0 {
			t.Errorf("虚拟机没运行，来自它的数据应当清空: 容器 %d 镜像 %d 卷 %d 网络 %d",
				len(a.containers), len(a.images), len(a.volumes), len(a.networks))
		}
		if len(a.sessions) != 1 || a.sessions[0].DisplayName != hostSession {
			t.Errorf("会话列表应来自 system session list: %+v", a.sessions)
		}
		if a.info.WSLVersion != "3.0.5.0" || a.wslcVer == "" {
			t.Errorf("版本信息应当仍然刷新: info=%+v wslcVer=%q", a.info, a.wslcVer)
		}
		if a.lastError != "" {
			t.Errorf("虚拟机没运行不是错误，lastError=%q", a.lastError)
		}
		if a.lastRefresh.IsZero() {
			t.Error("lastRefresh 应当更新，状态栏才不会一直显示「尚未刷新」")
		}
	})
	if stateOf(a) != wslvm.Stopped {
		t.Error("刷新应当顺手把「已停止」发布给界面，不用等下一次轮询")
	}
}

// 对照：虚拟机在运行时必须照常查询，免得门控拦过了头。
func TestRefreshQueriesWhenVMRunning(t *testing.T) {
	a, f := gateApp(t, &fakeHost{running: true})
	refreshAndWait(t, a)

	for _, prefix := range []string{"list", "images", "volume list", "network list"} {
		if f.Count(prefix) == 0 {
			t.Errorf("虚拟机在运行，应当查询 %q，实际调用: %q", prefix, f.Calls())
		}
	}
	a.ViewLocked(func() {
		if len(a.containers) != 1 || a.containers[0].Names != "ocd" {
			t.Errorf("容器没有读进来: %+v", a.containers)
		}
	})
	if stateOf(a) != wslvm.Running {
		t.Error("应当发布「运行中」")
	}
}

// TestRefreshProceedsWhenVMStateUnknown 验证观测不了时照常查询。
//
// 门控的前提是「确定没在运行」。观测失败（进程表读不了、不是 Windows……）时，
// 若也当成已停止，就会把一个其实在运行的虚拟机拦在界面之外。
func TestRefreshProceedsWhenVMStateUnknown(t *testing.T) {
	a, f := gateApp(t, &fakeHost{probeErr: errors.New("access denied")})
	refreshAndWait(t, a)
	if f.Count("list") == 0 {
		t.Errorf("观测不了时应照常查询，实际调用: %q", f.Calls())
	}
	if stateOf(a) != wslvm.Unknown {
		t.Errorf("状态应为 Unknown，实际 %v", stateOf(a))
	}
}

// TestRefreshWhileStoppedClearsStaleData 验证门控路径自己也会清掉过期数据。
//
// 不能全指望「状态变化时清一次」。场景：一次刷新在虚拟机还在运行时开始，
// 虚拟机停了之后它才把结果写进来——过期的容器列表就留在了界面上（系统页的资源概览会显示「容器 N」）。
// 那时状态早已是「已停止」并已发布，再来一次刷新不会触发「状态变化」，
// 能把它们清掉的只有刷新自己。
func TestRefreshWhileStoppedClearsStaleData(t *testing.T) {
	a, _ := gateApp(t, &fakeHost{})
	stopped := vmSnap(wslvm.Stopped)
	a.vmx.snap, a.vmx.published = stopped, stopped // 「状态变化」那条路不会再触发
	sampleData(a)                                  // 过期的数据
	if len(a.containers) == 0 {
		t.Fatal("前提不成立：样例数据里应有容器")
	}

	refreshAndWait(t, a)

	a.ViewLocked(func() {
		if len(a.containers)+len(a.images)+len(a.volumes)+len(a.networks) != 0 {
			t.Errorf("过期数据没有被清掉: 容器 %d 镜像 %d 卷 %d 网络 %d",
				len(a.containers), len(a.images), len(a.volumes), len(a.networks))
		}
	})
}

// TestStopDoesNotReviveVM 验证「关闭」之后紧跟的刷新不会把虚拟机叫醒。
//
// 缺陷：wslc 的 terminate 返回时，虚拟机进程往往还要一两秒才退出。
// runAsync 在动作结束时立刻刷新，那一刻宿主机上还看得到进程，门控就放行了，
// 刷新发出的 list 把刚关掉的虚拟机又建了起来。
// 所以关闭动作必须等进程真的消失之后才算结束。
func TestStopDoesNotReviveVM(t *testing.T) {
	h := &fakeHost{running: true, linger: 400 * time.Millisecond}
	a, f := gateApp(t, h)
	sampleData(a)

	// 必须等到「动作结束时触发的那次刷新」也结束再数命令：在刷新开始之前数，
	// 「没有发出任何命令」就是空话，缺陷装回去也抓不到。
	before := lastRefreshOf(a)
	start := time.Now()
	a.stopVM()
	waitActionDone(t, a, before)

	if calls := vmBoundCalls(f); len(calls) != 0 {
		t.Errorf("关闭之后的刷新叫醒了虚拟机: %q", calls)
	}
	if elapsed := time.Since(start); elapsed < 350*time.Millisecond {
		t.Errorf("关闭动作只用了 %v，没有等虚拟机进程退出（它会多留 400ms）", elapsed)
	}
	if stateOf(a) != wslvm.Stopped {
		t.Errorf("关闭之后应显示已停止，实际 %v", stateOf(a))
	}
	if !hasToast(a, locales.VmStoppedToast()) {
		t.Error("关闭成功后应有提示")
	}
	// 注意不能看 a.lastError：动作结束后紧跟着一次刷新，刷新成功会把它清空，
	// 用户真正看到的失败信号是「操作失败」提示。
	if hasToast(a, "操作失败") {
		t.Error("成功的关闭不该弹出失败提示")
	}
}

// 终止命令失败，但虚拟机其实已经停了（刚好因空闲自己关了、会话已经不在）：
// 用户要的结果达到了，不该弹一个「操作失败」。
func TestStopTreatsAlreadyStoppedAsSuccess(t *testing.T) {
	h := &fakeHost{stopErr: errors.New("no default session")}
	a, _ := gateApp(t, h)
	a.stopVM()
	// 等「成功」的提示出现作为动作结束的信号：不这样等的话，下面「没有失败提示」的断言会空过。
	waitToast(t, a, locales.VmStoppedToast())
	if hasToast(a, "操作失败") {
		t.Error("虚拟机本来就停着，不该弹出失败提示")
	}
}

// 终止命令失败、而虚拟机确实还在跑：必须把失败告诉用户。
func TestStopFailureSurfacesWhileStillRunning(t *testing.T) {
	h := &fakeHost{running: true, stopErr: errors.New("access is denied")}
	a, _ := gateApp(t, h)
	before := lastRefreshOf(a)
	a.stopVM()
	waitActionDone(t, a, before)
	if !hasToast(a, "操作失败") || !hasToast(a, "access is denied") {
		t.Error("失败应当以提示告诉用户，并带上 wslc 的原话")
	}
	if stateOf(a) != wslvm.Running {
		t.Errorf("虚拟机仍在运行，状态应为 Running，实际 %v", stateOf(a))
	}
}

// 命令说成功了，但进程一直不退：不能无限等，也不能假装成功。
func TestStopReportsProcessThatNeverExits(t *testing.T) {
	oldWait, oldStep := vmStopWait, vmStepWait
	vmStopWait, vmStepWait = 250*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { vmStopWait, vmStepWait = oldWait, oldStep })

	h := &fakeHost{running: true, stuck: true}
	a, _ := gateApp(t, h)
	before := lastRefreshOf(a)
	a.stopVM()
	waitActionDone(t, a, before)
	if !hasToast(a, "操作失败") || !hasToast(a, "仍在运行") {
		t.Error("进程一直不退，应以提示告诉用户")
	}
}

// 正常启动：状态变成运行中，并且随后刷新出数据。
func TestStartThenRefreshLoadsData(t *testing.T) {
	h := &fakeHost{}
	a, f := gateApp(t, h)
	a.vmx.snap = vmSnap(wslvm.Stopped)

	before := lastRefreshOf(a)
	a.startVM()
	waitActionDone(t, a, before)

	if stateOf(a) != wslvm.Running {
		t.Errorf("启动之后应显示运行中，实际 %v", stateOf(a))
	}
	if a.vmx.broken.Load() {
		t.Error("正常启动不该被判成「观测对不上」")
	}
	if f.Count("list") == 0 {
		t.Errorf("启动之后应刷新出数据，实际调用: %q", f.Calls())
	}
	a.ViewLocked(func() {
		if len(a.containers) != 1 {
			t.Errorf("启动之后容器列表应读进来: %+v", a.containers)
		}
	})
}

// TestStartDisablesDetectionWhenProcessNeverAppears 验证观测对不上时，门控会自己停用。
//
// 缺陷场景：「按进程名找虚拟机」在某台机器上不灵（wslc 将来改了命名、会话不是默认的……）。
// 那样宿主机上永远「找不到进程」，应用就一直当它已停止：资源页被「虚拟机未运行」挡住，
// 用户点多少次「启动」都没用，整个应用等于废了。
// 所以：wslc 说启动成功了、宿主机上却始终见不到进程，就认定观测不灵，改回不依赖它的老样子。
func TestStartDisablesDetectionWhenProcessNeverAppears(t *testing.T) {
	h := &fakeHost{neverAppears: true}
	a, f := gateApp(t, h)
	a.vmx.snap = vmSnap(wslvm.Stopped)
	a.page = PageContainers

	before := lastRefreshOf(a)
	a.startVM()
	waitActionDone(t, a, before)

	if !a.vmx.broken.Load() {
		t.Fatal("宿主机上始终没有进程，应当停用状态判断")
	}
	if stateOf(a) != wslvm.Unknown {
		t.Errorf("停用之后状态应为 Unknown，实际 %v", stateOf(a))
	}
	a.ViewLocked(func() {
		if a.vmPaneApplies() {
			t.Error("停用之后不该再用「虚拟机未运行」挡住页面")
		}
	})
	if f.Count("list") == 0 {
		t.Error("停用之后刷新应当照常查询")
	}
	if !hasToast(a, locales.VmDetectBroken()) {
		t.Error("应当告诉用户状态判断被停用了")
	}
	if logHas(a, "停用虚拟机状态判断") != 1 {
		t.Error("应当记一条日志")
	}
	// 之后即使宿主机报告「已停止」，也不能再据此拦截。
	if got := a.observeVM(); got.State != wslvm.Unknown {
		t.Errorf("停用之后观测应一律是 Unknown，实际 %v", got.State)
	}
}

// 虚拟机只出现了很短一会儿就又被关了（session.idleTimeout 设得很短），不能因此判成「观测对不上」。
func TestStartToleratesVMThatIdlesOutAtOnce(t *testing.T) {
	h := &fakeHost{blip: 400 * time.Millisecond}
	a, _ := gateApp(t, h)
	a.startVM()
	waitToast(t, a, locales.VmStartedToast())
	if a.vmx.broken.Load() {
		t.Error("命令返回前见过进程，说明观测是灵的，不该停用")
	}
}

// TestWatchVMFollowsHost 验证轮询：虚拟机被别处启动、关闭时，界面跟着变。
func TestWatchVMFollowsHost(t *testing.T) {
	h := &fakeHost{}
	a, f := gateApp(t, h)
	a.vmx.interval = 5 * time.Millisecond
	sampleData(a)
	a.WatchVM()
	t.Cleanup(a.StopWatchingVM)

	waitUntil(t, "观测到已停止", func() bool { return stateOf(a) == wslvm.Stopped })
	a.ViewLocked(func() {
		if len(a.containers) != 0 {
			t.Error("观测到虚拟机已停止，来自它的数据应当清空")
		}
	})

	// 命令行里把虚拟机启动了：应用要自己把数据读进来，不用用户去点刷新。
	h.setRunning(true)
	waitUntil(t, "观测到运行中并读入数据", func() bool {
		n := 0
		a.ViewLocked(func() { n = len(a.containers) })
		return stateOf(a) == wslvm.Running && n == 1
	})

	// 又被关了：数据作废，且轮询本身不会发任何会叫醒虚拟机的命令。
	callsBefore := len(f.Calls())
	h.setRunning(false)
	waitUntil(t, "观测到已停止并清空数据", func() bool {
		n := 1
		a.ViewLocked(func() { n = len(a.containers) })
		return stateOf(a) == wslvm.Stopped && n == 0
	})
	time.Sleep(60 * time.Millisecond) // 让轮询再多跑几轮
	for _, c := range f.Calls()[callsBefore:] {
		if !strings.HasPrefix(c, "info") && !strings.HasPrefix(c, "system session list") && !strings.HasPrefix(c, "--version") {
			t.Errorf("虚拟机停了以后，轮询不该发出命令 %q", c)
		}
	}
}

// 我们自己的启动/关闭进行中，轮询看到状态变化时不要再重复刷新：动作结束时会自己刷新。
func TestWatchVMDoesNotRefreshDuringOwnAction(t *testing.T) {
	h := &fakeHost{}
	a, f := gateApp(t, h)
	a.vmx.interval = 5 * time.Millisecond
	a.WatchVM()
	t.Cleanup(a.StopWatchingVM)
	waitUntil(t, "观测到已停止", func() bool { return stateOf(a) == wslvm.Stopped })

	a.setBusy(vmStartKey, true)
	t.Cleanup(func() { a.setBusy(vmStartKey, false) })
	h.setRunning(true)
	waitUntil(t, "观测到运行中", func() bool { return stateOf(a) == wslvm.Running })
	time.Sleep(60 * time.Millisecond)
	if n := f.Count("list"); n != 0 {
		t.Errorf("动作进行中，轮询不该自己刷新，却发了 %d 次 list", n)
	}
}

// TestObserveVM 验证观测的几条约定：失败是 Unknown 且只记一次日志、内存取整、停用后一律 Unknown。
func TestObserveVM(t *testing.T) {
	t.Run("失败是Unknown并且同一个错误只记一次", func(t *testing.T) {
		a, _ := gateApp(t, &fakeHost{probeErr: errors.New("boom")})
		for i := 0; i < 3; i++ {
			if got := a.observeVM(); got.State != wslvm.Unknown {
				t.Fatalf("State = %v", got.State)
			}
		}
		if n := logHas(a, "观测虚拟机失败"); n != 1 {
			t.Errorf("同一个错误连续出现应只记一次日志，实际 %d 条", n)
		}
	})
	t.Run("内存取整到MiB", func(t *testing.T) {
		a := newTestApp(t)
		a.vmx.probe = func() (wslvm.Snapshot, error) {
			return wslvm.Snapshot{State: wslvm.Running, Memory: 474<<20 + 123456}, nil
		}
		if got := a.observeVM().Memory; got != 474<<20 {
			t.Errorf("Memory = %d，期望取整到 %d（否则它每一刻都在变，轮询会白白重绘）", got, 474<<20)
		}
	})
	t.Run("停用后一律Unknown", func(t *testing.T) {
		a, _ := gateApp(t, &fakeHost{running: true})
		a.vmx.broken.Store(true)
		if got := a.observeVM(); got.State != wslvm.Unknown || got.PID != 0 || got.Memory != 0 {
			t.Errorf("停用后观测应为 Unknown 且不带 PID/内存: %+v", got)
		}
	})
}
