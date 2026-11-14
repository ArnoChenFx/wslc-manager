package app

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// systemPage 显示 wslc 的环境信息、会话与路径。
func (a *App) systemPage(c *ui.Context) {
	t := c.Theme()

	ui.Scroll(c).Grow(1).Children(func() {
		// 内边距放在内部的 Column 上，而不是 Scroll 自身：
		// 放在 Scroll 上的 padding 会随内容滚走，底部留白被滚出视口后，
		// 最后一个卡片看起来就像被切掉了。
		//
		// FillWidth 是关键：纵向 Scroll 只约束子元素的高度，宽度需自己取满；
		// 没宽度时 Column 会被压成极窄，卡片内容只好擞行，
		// 总高度暴涨，最后一张卡片就被推到视口外。
		ui.Column(c).FillWidth().Padding(16, 16, 28, 16).Gap(16).Children(func() {
			// 虚拟机：容器、镜像、卷、网络都在里面，所以最先看到它在不在跑、占多少内存，并能显式启动/关闭。
			a.vmCard(c)

			// 应用自身的版本与更新：有新版本时侧边栏的「新」徽标就指向这里。
			a.updateCard(c)

			// 依赖环境：是否装了 wslc / wslc-compose，可一键安装与升级。
			a.envCard(c)

			// 版本与路径
			a.infoCard(c, locales.CommonEnvironment(), func() {
				a.infoRow(c, "wslc", a.wslcVer)
				a.infoRow(c, "wslc-compose", a.composeVer)
				a.infoRow(c, locales.SystemWslVersion(), a.info.WSLVersion)
				a.infoRow(c, locales.SystemKernelVersion(), a.info.KernelVersion)
				a.infoRow(c, locales.SystemWindowsVersion(), a.info.WindowsVer)
				a.infoRow(c, "Direct3D", a.info.Direct3D)
				a.infoRow(c, locales.SystemSessionManager(), a.info.SessionMgr)
				if a.wslc != nil {
					a.infoRow(c, locales.SystemWslcPath(), a.wslc.Binary())
				}
				if a.compose != nil {
					a.infoRow(c, locales.SystemComposePath(), locales.CommonBuiltin()+" "+compose.Version)
				}
				if a.store != nil {
					a.infoRow(c, locales.SettingsSettingsFile(), a.store.Path())
				}
				if a.info.SettingsFile != "" {
					a.infoRow(c, locales.SystemWslcSettings(), a.info.SettingsFile)
				}
				a.infoRow(c, locales.CommonArchitecture(), runtime.GOOS+"/"+runtime.GOARCH)
			})

			// 会话
			a.infoCard(c, locales.SystemSessions(), func() {
				ui.Text(c, locales.SystemElevation()).
					FontSize(12).TextColor(t.TextMuted)

				if len(a.sessions) == 0 {
					ui.Text(c, locales.SystemSessionsEmpty()).FontSize(12).TextColor(t.TextMuted)
					return
				}
				cols := []ui.TableColumn{
					{Title: "ID", Width: 60},
					{Title: locales.SystemCreatorPID(), Width: 110},
					{Title: locales.SystemDisplayName(), MinWidth: 160},
				}
				ui.Table(c, nil, cols, len(a.sessions), func(row, col int) {
					if row < 0 || row >= len(a.sessions) {
						return
					}
					s := a.sessions[row]
					switch col {
					case 0:
						ui.Text(c, s.ID)
					case 1:
						ui.Text(c, s.PID).TextColor(t.TextMuted).Selectable()
					case 2:
						ui.Text(c, s.DisplayName).SingleLine().Selectable()
					}
				}).Height(float32(32*(len(a.sessions)+1)) + 8)
			})

			// 会话资源统计
			a.infoCard(c, locales.SystemResourcesOverview(), func() {
				running := 0
				for _, ct := range a.containers {
					if ct.IsRunning() {
						running++
					}
				}
				ui.Row(c).Gap(24).Children(func() {
					a.stat(c, locales.NavContainers(), fmt.Sprint(len(a.containers)),
						locales.SystemRunningCount(running))
					a.stat(c, locales.NavImages(), fmt.Sprint(len(a.images)), "")
					a.stat(c, locales.NavVolumes(), fmt.Sprint(len(a.volumes)), "")
					a.stat(c, locales.NavNetworks(), fmt.Sprint(len(a.networks)), "")
					a.stat(c, locales.NavCompose(), fmt.Sprint(len(a.projects)), "")
				})
			})

			// 诊断报告
			a.infoCard(c, locales.DiagTitle(), func() {
				ui.Text(c, locales.DiagHint()).
					FontSize(12).TextColor(t.TextMuted)

				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c, locales.DiagExport()).Clicked() {
						a.exportDiagnostic()
					}
					if ui.Button(c, locales.DiagCopy()).Tooltip(locales.DiagCopyTooltip()).Clicked() {
						a.copyDiagnostic()
					}
					if a.diagPath != "" {
						ui.Text(c, locales.DiagLastExport(baseName(a.diagPath))).
							FontSize(11).TextColor(t.Success).SingleLine().
							Tooltip(a.diagPath)
					}
				})

				// 界面树需要 e2e 构建，这里说明清楚避免误以为没生效。
				ui.Text(c, locales.DiagE2eHint()).
					FontSize(11).TextColor(t.TextMuted)
			})

			// 清理操作
			a.infoCard(c, locales.CommonMaintenance(), func() {
				ui.Text(c, locales.SystemMaintenanceHint()).
					FontSize(12).TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, locales.ContainersPrune()).Clicked() {
						a.confirm(locales.ContainersPrune(), locales.ContainersPruneConfirm(), true, func() {
							a.runAsync("prune-containers", locales.ContainersPruned(), func(ctx context.Context) (string, error) {
								rep, err := a.wslc.PruneContainers(ctx)
								return rep.Output, err
							})
						})
					}
					if ui.Button(c, locales.ImagesPrune()).Clicked() {
						a.confirm(locales.ImagesPrune(), locales.ImagesPruneConfirm(), false, func() {
							a.runAsync("prune-images", locales.ImagesPruned(), func(ctx context.Context) (string, error) {
								rep, err := a.wslc.PruneImages(ctx)
								return rep.Output, err
							})
						})
					}
					if ui.Button(c, locales.SystemRecheckEnv()).Clicked() {
						a.Init()
						a.Refresh()
						a.toast(locales.ErrorRechecked())
					}
				})
			})

			// 运行日志
			a.logCard(c)

			// 已知限制提示
			a.infoCard(c, locales.SystemKnownLimits(), func() {
				lines := []string{
					"• " + locales.SystemLimitVolumes(),
					"• " + locales.SystemLimitLoopback(),
					"• " + locales.SystemLimitSerial(),
					"• " + locales.SystemLimitRestartPolicy(),
				}
				for _, ln := range lines {
					ui.Text(c, ln).FontSize(12).TextColor(t.TextMuted).Selectable()
				}
			})
		})
	})
}

// infoCard 绘制一个带标题的卡片。
func (a *App) infoCard(c *ui.Context, title string, body func()) {
	t := c.Theme()
	ui.Column(c).Padding(14).Gap(10).Radius(8).Background(t.Surface).
		Border(1, t.Border).Children(func() {
		ui.Text(c, title).FontSize(13).Bold()
		body()
	})
}

// infoRow 绘制一行「标签: 值」。
func (a *App) infoRow(c *ui.Context, label, value string) {
	t := c.Theme()
	if strings.TrimSpace(value) == "" {
		value = "—"
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).Width(140)
		ui.Text(c, value).FontSize(12).Selectable().Grow(1).SingleLine().Tooltip(value)
	})
}

// stat 绘制一个统计数字块。
//
// 布局要点：每个块必须恰好两行（数字 + 文字），否则同一行里的块
// 行数不同时，数字会因底部的额外行而对不齐（之前「容器」多了
// 一行副标题，导致其余各项数字下移）。因此把 sub 并入第二行。
func (a *App) stat(c *ui.Context, label, value, sub string) {
	t := c.Theme()
	ui.Column(c).Gap(2).AlignItems(ui.Start).Children(func() {
		ui.Text(c, value).FontSize(22).Bold()
		caption := label
		if sub != "" {
			caption = label + " · " + sub
		}
		ui.Text(c, caption).FontSize(11).TextColor(t.TextMuted)
	})
}
