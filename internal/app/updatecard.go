package app

// 这个文件是「应用更新」：系统页上的更新卡片，以及它背后的状态快照。
//
// 设计取舍：
//   - 检查、下载、签名校验、替换文件都由 MyGo 的更新插件完成，
//     发现新版本时弹出的窗口（带更新内容和进度条）也是它画的。
//     这里只做三件事：读状态、画卡片、把点击转给插件；
//   - 视图只读 a.upd 这份快照，不直接问插件（原因见 App.upd 的注释）；
//   - 快照在启动时、插件通知变化时刷新；刷新放后台，结果经 update 回主线程。

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/selfupdate"
	"github.com/arno/wslc-manager/locales"
)

// WatchUpdates 取一次更新状态快照，并订阅之后的变化。
//
// 必须在主窗口创建之后调用：插件的后台检查也是应用就绪后才开始的，
// 此时订阅不会漏掉第一次检查的结果。重复调用是安全的，旧的订阅会先被取消。
func (a *App) WatchUpdates() {
	if a.updOff != nil {
		a.updOff()
	}
	a.updOff = a.selfUpdate.OnChange(a.refreshUpdateStatus)
	a.refreshUpdateStatus()
}

// refreshUpdateStatus 在后台读取更新状态，再回主线程写入快照。
//
// 读状态要探测安装目录是否可写（磁盘 IO），不能占着主线程。
//
// 连续触发几次刷新时，后台读取的完成顺序没有保证；给每次刷新编号，
// 只接受最新一次的结果，晚到的旧结果直接丢弃，否则它会把较新的状态盖回去。
func (a *App) refreshUpdateStatus() {
	a.update(func() {
		a.updSeq++
		seq := a.updSeq
		go func() {
			st := a.selfUpdate.Status()
			a.update(func() {
				if seq == a.updSeq {
					a.upd = st
				}
			})
		}()
	})
}

// checkForUpdates 按用户的意思立即检查更新。
//
// 结果不在这里处理：更新窗口会马上弹出，检查中、已是最新、失败、
// 有新版本都在那个窗口里说明；窗口里的选择会经 OnChange 回到快照。
func (a *App) checkForUpdates() {
	a.logInfo("action", "检查更新")
	a.selfUpdate.CheckNow()
}

// setAutoCheckUpdates 打开或关闭后台自动检查。
func (a *App) setAutoCheckUpdates(on bool) {
	a.logInfo("action", "自动检查更新: %v", on)
	a.selfUpdate.SetAutoCheck(on)
}

// setAutoInstallUpdates 打开或关闭「自动下载并安装更新」。
func (a *App) setAutoInstallUpdates(on bool) {
	a.logInfo("action", "自动安装更新: %v", on)
	a.selfUpdate.SetAutoInstall(on)
}

// updateState 是更新状态归成的几类。卡片和诊断报告共用，保证两处文案一致。
type updateState int

const (
	// updateUnsupported：这个构建不能自己更新（开发版本、没带更新配置，
	// 或程序所在目录写不进去）。
	updateUnsupported updateState = iota
	// updateAvailable：最近一次检查发现了新版本。
	updateAvailable
	// updateNever：从来没有成功检查过。此时不能声称「已是最新」。
	updateNever
	// updateCurrent：检查过，且没有新版本。
	updateCurrent
)

// classifyUpdate 把状态快照归类。不支持自更新排在最前：
// 这样的构建不会检查，别的字段都不可信。
func classifyUpdate(st selfupdate.Status) updateState {
	switch {
	case !st.Supported:
		return updateUnsupported
	case st.Available != "":
		return updateAvailable
	case st.LastCheck.IsZero():
		return updateNever
	default:
		return updateCurrent
	}
}

// updateSummary 返回状态的一句话说明。
func updateSummary(st selfupdate.Status) string {
	switch classifyUpdate(st) {
	case updateUnsupported:
		return locales.UpdateUnsupported()
	case updateAvailable:
		return locales.UpdateAvailable(st.Available)
	case updateNever:
		return locales.UpdateNeverChecked()
	default:
		return locales.UpdateUpToDate()
	}
}

// updateCard 渲染「应用更新」卡片：当前版本、更新状态、手动检查入口。
func (a *App) updateCard(c *ui.Context) {
	t := c.Theme()
	st := a.upd
	state := classifyUpdate(st)

	a.infoCard(c, locales.UpdateTitle(), func() {
		a.infoRow(c, locales.SystemVersion(), versionOrDev(st.Version))

		// 状态行：圆点 + 一句话，颜色表达状态。
		//
		// 有新版本用 Warning，和依赖环境卡片里「有新版本可用」同色；
		// 不用强调色，因为强调色是用户自选的，浅色强调色上做文字对比度不够。
		color := t.TextMuted
		switch state {
		case updateAvailable:
			color = t.Warning
		case updateCurrent:
			color = t.Success
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.UpdateStatus()).FontSize(12).TextColor(t.TextMuted).Width(140)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Grow(1).Children(func() {
				ui.Icon(c, icon("dot")).FontSize(11).TextColor(color)
				ui.Text(c, updateSummary(st)).FontSize(12).TextColor(color)
			})
		})

		// 从没检查过就没有「上次检查」可显示，状态行已经说了「尚未检查」。
		if !st.LastCheck.IsZero() {
			a.infoRow(c, locales.UpdateLastCheck(), st.LastCheck.Local().Format("2006-01-02 15:04"))
		}

		if state == updateUnsupported {
			ui.Text(c, locales.UpdateUnsupportedHint()).FontSize(11).TextColor(t.TextMuted)
		}

		// 有新版本时按钮变成主按钮并带上版本号；点击后更新窗口会显示更新内容。
		// 不支持自更新时按钮仍然可点：窗口会说明为什么不能更新，
		// 比一个灰掉的按钮更容易让人明白。
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			var btn ui.Element
			if state == updateAvailable {
				btn = ui.PrimaryButton(c, locales.UpdateViewAvailable(st.Available))
			} else {
				btn = ui.Button(c, locales.UpdateCheckNow())
			}
			if btn.Tooltip(locales.UpdateCheckTooltip()).Clicked() {
				a.checkForUpdates()
			}
		})
	})
}

// updateFieldHint 返回设置里自动更新开关下面的说明。
//
// 不支持自更新的构建里开关是灰的，这时说明写成原因，
// 而不是照常讲「会怎样」——那些行为根本不会发生。
func (a *App) updateFieldHint(hint string) string {
	if !a.upd.Supported {
		return locales.UpdateUnsupported()
	}
	return hint
}

// versionOrDev 把空版本号显示成 "dev"。
//
// go run / go test 这类没有打包的运行没有版本号，空着显示成「—」
// 看不出原因，写成 dev 一眼就知道这是开发环境。
func versionOrDev(v string) string {
	if strings.TrimSpace(v) == "" {
		return "dev"
	}
	return v
}

// versionSource 返回程序当前的版本号。放成变量，是为了让测试能换成固定值。
var versionSource = func() string { return mygo.App.Version() }

// appVersion 返回诊断报告里显示的程序版本。
//
// 版本号来自 mygo.json 的 version，mygo build 把它写进可执行文件，
// 发布流程又会把它改成 tag 的版本。以前这里是写死的常量，
// 发布新版之后诊断报告还在报旧版本号，排查问题时会被误导。
func appVersion() string { return versionOrDev(versionSource()) }
