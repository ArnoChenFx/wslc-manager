package app

// 这个文件是「虚拟机」：系统页上的虚拟机卡片、启动/关闭动作、虚拟机没运行时资源页的提示，
// 以及让后台刷新不去叫醒虚拟机的门控。
//
// 设计取舍：
//   - wslc 的容器、镜像、卷、网络都在会话的虚拟机里。要和虚拟机里的 Docker 引擎对话的命令
//     （list、images……）会在虚拟机没运行时把它拉起来。所以「用户把它关了」要站得住，
//     后台刷新就不能再发那类命令（见 refreshGated）；
//   - 状态只问操作系统（有没有 vmmem 进程，见 internal/wslvm），不调用 wslc：
//     拿容器类命令来「看看它在不在」，一看就把它叫醒了；
//   - 观测不了、或者观测和事实对不上时，一律当 Unknown 并照常行事：宁可多问一次 wslc，
//     也不能把一个正在运行的虚拟机当成已停止、把整个界面拦住；
//   - 视图只读 a.vmx.snap 这份快照。轮询和动作都在后台，结果经 update 回主线程。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/internal/wslvm"
	"github.com/arno/wslc-manager/locales"
)

const (
	// 动作的忙碌标记。启动和关闭分开：界面要分别显示「正在启动」「正在关闭」。
	vmStartKey = "vm-start"
	vmStopKey  = "vm-stop"

	// vmPollInterval 是轮询虚拟机状态的间隔。观测只是读一次进程表、不碰 wslc，很便宜。
	vmPollInterval = 2 * time.Second
)

// 关闭之后等虚拟机进程消失的参数。做成变量是为了让测试能改短，正常运行时不会变。
var (
	// vmStopWait 是 terminate 返回之后，等虚拟机进程真正消失的上限。
	vmStopWait = 20 * time.Second
	// vmStepWait 是等待过程中两次观测的间隔。
	vmStepWait = 150 * time.Millisecond
)

// vmState 是应用里和虚拟机有关的全部状态，以及可以替换的依赖。
//
// 收成一个结构体、在 App 里只占一个字段：app.go 是大家都在改的热点文件，
// 改动越小越好。默认依赖（真实的探测器、真实的 wslc 命令）也在这里按需补上，
// 不用去动 New 和 Init。
type vmState struct {
	// snap 是最近一次观测，视图只读它。只在主线程读写。
	snap wslvm.Snapshot

	// probe、start、stop 是可以替换的依赖，测试用。nil 表示用真实实现。
	probe func() (wslvm.Snapshot, error)
	start func(ctx context.Context) error
	stop  func(ctx context.Context) error
	// interval 是轮询间隔，nil 值表示用 vmPollInterval，测试里改小。
	interval time.Duration

	// broken 表示观测和事实对不上，之后一律当 Unknown，不再据此拦任何操作。
	// 后台协程也会读它，所以用原子量。
	broken atomic.Bool

	prober *wslvm.Prober
	once   sync.Once

	// mu 保护下面三项。
	mu sync.Mutex
	// published 是最近一次交给界面的快照，用来去掉重复的发布。
	published wslvm.Snapshot
	// lastErr 是上一次观测失败的原因，用来让同一个错误只记一次日志。
	lastErr string
	// cancel 停止轮询。
	cancel context.CancelFunc
}

// vmProber 返回观测函数，默认是观测当前身份的默认会话的真实探测器。
func (a *App) vmProber() func() (wslvm.Snapshot, error) {
	if a.vmx.probe != nil {
		return a.vmx.probe
	}
	a.vmx.once.Do(func() { a.vmx.prober = wslvm.NewProber() })
	return a.vmx.prober.Probe
}

// vmStartFn 返回「启动虚拟机」的实现，默认是 wslc 的 StartSession。
func (a *App) vmStartFn() func(ctx context.Context) error {
	if a.vmx.start != nil {
		return a.vmx.start
	}
	return func(ctx context.Context) error {
		if a.wslc == nil {
			return fmt.Errorf("wslc 不可用")
		}
		return a.wslc.StartSession(ctx)
	}
}

// vmStopFn 返回「关闭虚拟机」的实现，默认是 wslc 的 TerminateSession。
func (a *App) vmStopFn() func(ctx context.Context) error {
	if a.vmx.stop != nil {
		return a.vmx.stop
	}
	return func(ctx context.Context) error {
		if a.wslc == nil {
			return fmt.Errorf("wslc 不可用")
		}
		return a.wslc.TerminateSession(ctx)
	}
}

// observeVM 观测一次虚拟机，并换算成「应用该怎么对待它」。
//
// 失败返回 Unknown：不知道，不等于没在跑。可以在任意协程调用。
func (a *App) observeVM() wslvm.Snapshot {
	snap, err := a.vmProber()()

	a.vmx.mu.Lock()
	first := false
	if err != nil {
		first = a.vmx.lastErr != err.Error()
		a.vmx.lastErr = err.Error()
	} else {
		a.vmx.lastErr = ""
	}
	a.vmx.mu.Unlock()
	if err != nil {
		snap.State = wslvm.Unknown
		if first {
			a.logWarn("vm", "观测虚拟机失败，不据此拦截任何操作: %v", err)
		}
	}

	// 内存取整到 MiB：它每一刻都在抖，不取整的话快照永远「变了」，
	// 轮询就会每 2 秒白白重绘一次。界面本来也只显示到 MB。
	snap.Memory &^= (1 << 20) - 1

	if a.vmx.broken.Load() {
		snap.State, snap.PID, snap.Memory = wslvm.Unknown, 0, 0
	}
	return snap
}

// setVMSnapshot 把一次观测发布给界面。相同的快照不重复发布。可以在任意协程调用。
func (a *App) setVMSnapshot(s wslvm.Snapshot) {
	a.vmx.mu.Lock()
	same := a.vmx.published == s
	a.vmx.published = s
	a.vmx.mu.Unlock()
	if same {
		return
	}
	a.update(func() {
		prev := a.vmx.snap
		a.vmx.snap = s
		a.onVMChanged(prev, s)
	})
}

// onVMChanged 在主线程处理虚拟机状态的变化。
func (a *App) onVMChanged(prev, next wslvm.Snapshot) {
	switch {
	case next.State == wslvm.Stopped && prev.State != wslvm.Stopped:
		// 虚拟机停了：用户关的、空闲自己关的、别处关的都一样。
		// 界面里的容器、镜像等都来自它，已经作废，别让它们看起来还有效。
		a.clearVMData()
	case next.State == wslvm.Running && prev.State == wslvm.Stopped:
		// 虚拟机被别处（命令行、别的程序）启动了：把数据读进来。
		// 我们自己发起的启动和关闭，动作结束时会自己刷新，不重复。
		if !a.vmBusy() {
			a.Refresh()
		}
	}
}

// clearVMData 清空来自虚拟机的数据。
func (a *App) clearVMData() {
	a.containers, a.images, a.volumes, a.networks = nil, nil, nil, nil
	a.clampSelections()
}

// WatchVM 开始轮询虚拟机状态。必须在主窗口创建之后调用，重复调用安全。
//
// 轮询放后台协程，只在状态变了才发布，所以平时不会引起重绘。
func (a *App) WatchVM() {
	a.StopWatchingVM()
	ctx, cancel := context.WithCancel(context.Background())
	a.vmx.mu.Lock()
	a.vmx.cancel = cancel
	a.vmx.mu.Unlock()

	interval := a.vmx.interval
	if interval <= 0 {
		interval = vmPollInterval
	}
	go func() {
		a.setVMSnapshot(a.observeVM())
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.setVMSnapshot(a.observeVM())
			}
		}
	}()
}

// StopWatchingVM 停止轮询。
func (a *App) StopWatchingVM() {
	a.vmx.mu.Lock()
	cancel := a.vmx.cancel
	a.vmx.cancel = nil
	a.vmx.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// refreshGated 在虚拟机确定没在运行时，代替 Refresh 完成一次刷新，返回 true 表示已处理，
// 调用方应当直接返回。观测不了（Unknown）、或虚拟机在运行时返回 false，由 Refresh 照常查询。
//
// 为什么要拦：容器、镜像、卷、网络的查询要和虚拟机里的 Docker 引擎对话，
// 发出去就会把虚拟机拉起来。用户刚把它关掉、或它刚因空闲自己关掉，
// 一次刷新（每个动作结束都会触发一次）就让它醒了，「关闭」等于没做。
//
// 虚拟机没运行时只读不会拉起它的东西：版本、wslc info、会话列表都只问会话管理器服务。
func (a *App) refreshGated(ctx context.Context) bool {
	snap := a.observeVM()
	a.setVMSnapshot(snap)
	if snap.State != wslvm.Stopped {
		return false
	}

	var (
		info       wslc.Info
		sessions   []wslc.Session
		wslcVer    string
		composeVer string
	)
	if v, err := a.wslc.Info(ctx); err == nil {
		info = v
	}
	if v, err := a.wslc.Version(ctx); err == nil {
		wslcVer = v
	}
	if v, err := a.wslc.ListSessions(ctx); err == nil {
		sessions = v
	}
	if a.compose != nil {
		if v, err := a.compose.Version(ctx); err == nil {
			composeVer = v
		}
	}

	a.logInfo("refresh", "虚拟机没在运行，跳过容器/镜像/卷/网络的查询（查询会把它拉起来）")
	a.update(func() {
		a.clearVMData()
		a.sessions, a.info, a.wslcVer, a.composeVer = sessions, info, wslcVer, composeVer
		a.loading = false
		a.lastRefresh = time.Now()
		a.lastError = ""
	})
	// 项目列表只读本地的 compose 文件，不碰 wslc，照常刷新。
	a.refreshProjects()
	return true
}

// vmBusy 报告启动或关闭是否正在进行。
func (a *App) vmBusy() bool {
	return a.isBusy(vmStartKey) || a.isBusy(vmStopKey)
}

// startVM 启动虚拟机：放后台，结束后提示并刷新数据。
func (a *App) startVM() {
	if a.vmBusy() {
		return
	}
	a.runAsync(vmStartKey, locales.VmStartedToast(), func(ctx context.Context) (string, error) {
		return "", a.startVMBlocking(ctx)
	})
}

// startVMBlocking 启动虚拟机并校验「按进程名找虚拟机」这个办法在这台机器上灵不灵。
func (a *App) startVMBlocking(ctx context.Context) error {
	// 启动期间每 100 毫秒看一次有没有出现过虚拟机进程。
	// 不能只在命令返回后看一眼：session.idleTimeout 设得很短的话，
	// 虚拟机可能在命令返回之前就又被关了，那样就会误判成「没起来过」。
	var saw atomic.Bool
	stopWatch := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			if s, err := a.vmProber()(); err == nil && s.State == wslvm.Running {
				saw.Store(true)
			}
			select {
			case <-stopWatch:
				return
			case <-t.C:
			}
		}
	}()
	err := a.vmStartFn()(ctx)
	close(stopWatch)
	<-watched
	if err != nil {
		return err
	}

	// wslc 说启动成功了，虚拟机就一定起来过。若从头到尾没在宿主机上见到它的进程，
	// 说明「按进程名找虚拟机」在这台机器上不灵（命名规则变了、会话不是默认的……）。
	// 这时「已停止」是假的：停用状态判断，让界面回到不依赖它的老样子，
	// 否则资源页会一直被「虚拟机未运行」挡住，用户点多少次「启动」都没用。
	if s, perr := a.vmProber()(); perr == nil && s.State == wslvm.Stopped && !saw.Load() {
		a.vmx.broken.Store(true)
		a.logWarn("vm", "启动成功，但宿主机上始终没有 %s 进程，停用虚拟机状态判断", s.Process)
		a.setVMSnapshot(a.observeVM())
		a.toast(locales.VmDetectBroken())
	}
	return nil
}

// confirmStopVM 先确认再关闭：关闭会终止里面正在运行的容器。
func (a *App) confirmStopVM() {
	var names []string
	for _, ct := range a.containers {
		if ct.IsRunning() {
			names = append(names, displayName(ct))
		}
	}
	body := locales.VmStopConfirmBody()
	if len(names) > 0 {
		body = locales.VmStopConfirmBodyRunning(len(names), briefNames(names, 3))
	}
	a.confirm(locales.VmStopConfirmTitle(), body, true, a.stopVM)
}

// briefNames 最多列出 max 个名字，多的用省略号。
func briefNames(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + " …"
}

// stopVM 关闭虚拟机：放后台，结束后提示并刷新数据。
func (a *App) stopVM() {
	if a.vmBusy() {
		return
	}
	a.runAsync(vmStopKey, locales.VmStoppedToast(), func(ctx context.Context) (string, error) {
		return "", a.stopVMBlocking(ctx)
	})
}

// stopVMBlocking 终止会话，并等虚拟机进程真的消失。
func (a *App) stopVMBlocking(ctx context.Context) error {
	if err := a.vmStopFn()(ctx); err != nil {
		// 终止命令失败，但虚拟机其实已经停了（比如刚好因空闲自己关了、会话已不存在）：
		// 用户要的结果达到了，不当作失败。
		if s, perr := a.vmProber()(); perr == nil && s.State == wslvm.Stopped && !a.vmx.broken.Load() {
			a.logInfo("vm", "终止命令失败（%v），但虚拟机已经停了", firstLine(err.Error()))
			return nil
		}
		return err
	}

	// terminate 返回时，虚拟机进程可能还要一两秒才退出。runAsync 随后立刻会刷新：
	// 那时若还看到它在跑，就会照常去问容器，把刚关掉的虚拟机又叫醒。所以要等它消失。
	// 观测不灵的话（broken）等不到「消失」，直接返回。
	deadline := time.Now().Add(vmStopWait)
	for {
		s, err := a.vmProber()()
		if err != nil || s.State != wslvm.Running || a.vmx.broken.Load() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("已发出关闭命令，但虚拟机进程 %s 在 %d 秒后仍在运行", s.Process, int(vmStopWait/time.Second))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(vmStepWait):
		}
	}
}

// vmPaneApplies 报告当前页面是否该用「虚拟机未运行」的提示代替内容：
// 只有数据住在虚拟机里的几个资源页，并且确定它没在运行。
func (a *App) vmPaneApplies() bool {
	if a.vmx.snap.State != wslvm.Stopped {
		return false
	}
	switch a.page {
	case PageContainers, PageImages, PageVolumes, PageNetworks:
		return true
	}
	return false
}

// vmStoppedPane 是资源页在虚拟机没运行时显示的提示，带一个启动入口。
func (a *App) vmStoppedPane(c *ui.Context) {
	t := c.Theme()
	starting := a.isBusy(vmStartKey)
	ui.Column(c).Fill().Center().Gap(10).Padding(32).Children(func() {
		ui.Text(c, locales.VmPaneTitle()).FontSize(16).Bold()
		ui.Text(c, locales.VmPaneBody()).TextColor(t.TextMuted).MaxWidth(560).TextAlign(ui.Center)
		if starting {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, locales.VmStarting()).FontSize(12).TextColor(t.TextMuted)
			})
			return
		}
		if ui.PrimaryButton(c, locales.VmStart()).Tooltip(locales.VmStartTooltip()).Clicked() {
			a.startVM()
		}
	})
}

// vmCard 渲染系统页上的「虚拟机」卡片：状态、会话、内存，以及启动和关闭。
func (a *App) vmCard(c *ui.Context) {
	t := c.Theme()
	snap := a.vmx.snap
	starting, stopping := a.isBusy(vmStartKey), a.isBusy(vmStopKey)
	busy := starting || stopping

	a.infoCard(c, locales.VmTitle(), func() {
		// 状态行：圆点（或转圈）+ 一句话，颜色表达状态。
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.VmStatus()).FontSize(12).TextColor(t.TextMuted).Width(140)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Grow(1).Children(func() {
				text, color := vmStatusText(snap.State), t.TextMuted
				switch {
				case starting:
					text = locales.VmStarting()
				case stopping:
					text = locales.VmStopping()
				case snap.State == wslvm.Running:
					color = t.Success
				}
				if busy {
					ui.Spinner(c).Size(12, 12)
				} else {
					ui.Icon(c, icon("dot")).FontSize(11).TextColor(color)
				}
				ui.Text(c, text).FontSize(12).TextColor(color)
			})
		})

		a.infoRow(c, locales.VmSession(), snap.Session)

		// 内存行：只有运行时才有数。没运行也留着这一行，免得状态一变整张卡片的高度跟着跳。
		mem := "—"
		if snap.State == wslvm.Running {
			mem = formatMemory(snap.Memory)
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.VmMemory()).FontSize(12).TextColor(t.TextMuted).Width(140)
			memText := ui.Text(c, mem).FontSize(12).Selectable().Grow(1).SingleLine()
			if snap.Process != "" {
				memText.Tooltip(locales.VmMemoryTooltip(snap.Process))
			}
		})

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			// 没运行时「启动」是下一步该做的事，用主按钮；运行时「关闭」是灰色的危险操作。
			// 观测不了（Unknown）时两个都开着：不知道状态，就别替用户决定哪个不能点。
			var start ui.Element
			if snap.State == wslvm.Stopped {
				start = ui.PrimaryButton(c, locales.VmStart())
			} else {
				start = ui.Button(c, locales.VmStart())
			}
			start.Tooltip(locales.VmStartTooltip()).Disabled(busy || snap.State == wslvm.Running)
			if start.Clicked() {
				a.startVM()
			}

			stop := ui.Button(c, locales.VmStop()).Tooltip(locales.VmStopTooltip()).
				Disabled(busy || snap.State == wslvm.Stopped)
			stop.TextColor(t.Danger)
			if stop.Clicked() {
				a.confirmStopVM()
			}
		})

		ui.Text(c, locales.VmHint()).FontSize(11).TextColor(t.TextMuted)
	})
}

// vmStatusText 是虚拟机状态的文字。
func vmStatusText(s wslvm.State) string {
	switch s {
	case wslvm.Running:
		return locales.VmRunning()
	case wslvm.Stopped:
		return locales.VmStopped()
	default:
		return locales.VmUnknown()
	}
}

// vmSummary 是虚拟机状态的一句话摘要，诊断报告用。
func vmSummary(s wslvm.Snapshot) string {
	out := vmStatusText(s.State)
	if s.State == wslvm.Running {
		out += " · " + formatMemory(s.Memory)
		if s.Process != "" {
			out += fmt.Sprintf(" (%s, PID %d)", s.Process, s.PID)
		}
	}
	return out
}

// formatMemory 把字节数写成 MB 或 GB。
//
// 按 1024 进位，和任务管理器一致：用户会拿它去和任务管理器里的 vmmem 对，
// 进位不同的话数字永远对不上。
func formatMemory(b uint64) string {
	if b == 0 {
		return "—"
	}
	if b >= 1<<30 {
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MB", b>>20)
}
