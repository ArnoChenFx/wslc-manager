package app

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// composePage 是 Compose 项目页：左侧项目列表 + 右侧项目详情。
func (a *App) composePage(c *ui.Context) {
	if a.composeErr != "" {
		a.errorPane(c, locales.ErrorComposeUnavailable(), a.composeErr)
		return
	}

	ui.Column(c).Fill().Children(func() {
		// 主体：左侧项目列表 + 右侧详情。
		// 有输出面板时上下分栏，让 up / build 等操作的进度可见可滚动。
		body := func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				a.projectListPane(c)
				ui.Divider(c)
				// FillWidth 是必需的：少它时这个 Column 按内容自然宽度撑开，
				// 于是表头那一行（名称 + 状态 + 四个按钮）把整体宽度推到
				// 窗口之外，右侧操作区整个看不见（默认 1180 宽时实测复现）。
				ui.Column(c).Grow(1).FillWidth().Shrink(1).Children(func() {
					a.projectDetail(c)
				})
			})
		}

		if a.outputOpen {
			ui.SplitVertical(c, &a.outputHeight, body, func() {
				a.outputPanel(c)
			}).Grow(1)
		} else {
			body()
		}
	})
}

// projectListPane 绘制项目列表。
func (a *App) projectListPane(c *ui.Context) {
	t := c.Theme()

	ui.Column(c).Width(280).FillHeight().Children(func() {
		ui.Row(c).Padding(8, 10).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.ComposeProjects()).FontSize(12).Bold().TextColor(t.TextMuted).Grow(1)
			if ui.Button(c, "").Label(locales.ComposeAdd()).Tooltip(locales.ComposeAddTooltip()).Children(func() {
				ui.Icon(c, icon("plus")).FontSize(14)
			}).Clicked() {
				a.addProjectOpen = true
				a.addProjectPath = ""
			}
			if ui.Button(c, "").Label(locales.ComposeScan()).Tooltip(locales.ComposeScanTooltip()).Children(func() {
				ui.Icon(c, icon("search")).FontSize(14)
			}).Clicked() {
				a.DiscoverProjects()
			}
		})
		ui.Divider(c)

		rows := a.filteredProjects()
		a.projectList.Key = func(i int) any {
			if i < len(rows) {
				return rows[i].Dir
			}
			return i
		}
		a.projectList.Selected = &a.projectSel
		a.projectList.RoundRows = true
		styleRows(&a.projectList, t)
		syncRowSelection(&a.projectList, a.projectSel)

		// 列表用一份专门的柔和主题：MyGo 的 List 选中行会整片铺 accent，
		// 而按钮需要饱和的 accent，两者冲突，只能在子树上覆盖。
		ui.Themed(c, listTheme(t)).Fill().Children(func() {
			ui.List(c, &a.projectList, len(rows), func(i int) {
				p := rows[i]
				ui.Column(c).Padding(6, 10).Gap(2).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ic := ui.Icon(c, icon("stack")).FontSize(14)
						if !p.Healthy() {
							ic.TextColor(t.Danger)
						}
						// 这里刻意不加 Selectable：
						// 列表行几乎被文字铺满，一旦文字可选，
						// 点击就变成选文字而不是选行，导致行几乎无法选中。
						// 想复制路径的话，右键菜单里有「复制目录路径」。
						ui.Text(c, p.Name).Bold().SingleLine().Grow(1)
						if n := len(p.Config.Services); n > 0 {
							ui.Badge(c, fmt.Sprint(n)).TextColor(t.TextMuted)
						}
					})
					ui.Text(c, baseName(p.Dir)).FontSize(11).TextColor(t.TextMuted).SingleLine()
				})
			}).Grow(1).Label(locales.ComposeList()).Children(func() {
				// 空列表时显示提示。
				// 必须挂在 List 自己的 Children 上，而不是在 List 之后再并一个
				// Fill() 的 Column：后者会把 Grow(1) 的列表压成 0 高度
				// （实测中列表高度真的变成了 0，行自然点不中）。
				if len(rows) > 0 {
					return
				}
				ui.Column(c).Padding(16).Gap(8).Children(func() {
					ui.Text(c, locales.ComposeEmptyTitle()).FontSize(13).TextColor(t.TextMuted)
					ui.Text(c, locales.ComposeEmptyBody()).FontSize(11).TextColor(t.TextMuted)
				})
			}).ContextMenu(func(m *ui.Menu) {
				if a.projectSel < 0 || a.projectSel >= len(rows) {
					return
				}
				p := rows[a.projectSel]
				if m.Item(locales.CommonOpenInExplorer()).Chosen() {
					a.openInExplorer(p.Dir)
				}
				if m.Item(locales.ComposeCopyDir()).Chosen() {
					c.WriteClipboard(p.Dir)
					a.toast(locales.CommonCopied())
				}
				if m.Item(locales.ComposeReload()).Chosen() {
					a.refreshProjects()
				}
				m.Separator()
				if m.Item(locales.ComposeRemoveFromList()).Chosen() {
					a.RemoveProject(p.Dir)
				}
			})
		})
	})
}

// filteredProjects 按搜索框过滤项目。
func (a *App) filteredProjects() []compose.Project {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.projects
	}
	out := make([]compose.Project, 0, len(a.projects))
	for _, p := range a.projects {
		if strings.Contains(strings.ToLower(p.Name+" "+p.Dir), q) {
			out = append(out, p)
		}
	}
	return out
}

// selectedProject 返回当前选中的项目。
func (a *App) selectedProject() *compose.Project {
	rows := a.filteredProjects()
	if a.projectSel < 0 || a.projectSel >= len(rows) {
		return nil
	}
	return &rows[a.projectSel]
}

// projectDetail 绘制选中项目的详情与操作。
func (a *App) projectDetail(c *ui.Context) {
	t := c.Theme()
	p := a.selectedProject()

	if p == nil {
		ui.Column(c).Fill().Center().Gap(8).Children(func() {
			ui.Text(c, locales.ComposeSelectHint()).FontSize(14).TextColor(t.TextMuted)
			ui.Text(c, locales.ComposeSelectHintBody()).FontSize(12).TextColor(t.TextMuted).Selectable()
		})
		return
	}

	if !p.Healthy() {
		ui.Column(c).Fill().Padding(16).Gap(10).Children(func() {
			ui.Text(c, p.Name).FontSize(16).Bold().Selectable()
			ui.Text(c, p.Dir).FontSize(11).TextColor(t.TextMuted).Selectable()
			ui.Text(c, locales.ComposeConfigLoadFailed()).Bold().TextColor(t.Danger)
			ui.Scroll(c).MaxHeight(200).Children(func() {
				ui.Text(c, p.Err).FontSize(12).PreserveSpaces().TextColor(t.Danger).Selectable()
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, locales.ComposeReload()).Clicked() {
					a.refreshProjects()
				}
				if ui.Button(c, locales.ComposeRemoveFromList()).Clicked() {
					a.RemoveProject(p.Dir)
				}
			})
		})
		return
	}

	ui.Column(c).Fill().FillWidth().Children(func() {
		// 项目头部
		a.projectHeader(c, *p)
		ui.Divider(c)

		// 主体：服务列表 + 选项，中间用可拖动的分隔条。
		//
		// 用 ui.Split 而不是 Row + Divider：后者是一条静态线，
		// 用户无法调整两栏比例。服务表列多、选项栏文字长，
		// 固定比例总有一边不合适（曾经把描述挤成 "Equival / ent to"）。
		//
		// *size 是第一栏（服务表）的宽度，会被 Split 限制在
		// 距两端各 40 DIP 之内，拖出去也不会把另一栏挤没。
		split := ui.Split(c, &a.composeListWidth, func() {
			ui.Column(c).FillHeight().Children(func() {
				a.serviceTable(c, *p)
			})
		}, func() {
			ui.Column(c).FillHeight().Children(func() {
				a.projectOptions(c, *p)
			})
		}).Grow(1).FillWidth().SplitReset(400)

		// 拖动结束后记住位置，下次打开保持。
		if split.Changed() {
			a.settings.ComposeListWidth = a.composeListWidth
			a.saveSettings()
		}
	})
}

// projectHeader 绘制项目标题与主要动作。
//
// 布局刻意分成两行（标题行 / 操作行）而不是挤在一行：
// 详情区在默认窗口（1180）下只有约 678 宽，而
// 「名称 + 路径 + 状态徽章 + 启动 + 停止 + 重启 + 更多」
// 排成一行时需求约 750，右侧按钮会被直接推到窗口之外
// （实测「启动」x=1150 只剩几十像素，后两个按钮宽度为 0，
// 整个操作区看不见）。拆成两行后，无论窗口多窄都不会溢出，
// 加按钮也不必再斤斤计较宽度。
func (a *App) projectHeader(c *ui.Context, p compose.Project) {
	t := c.Theme()

	ui.Column(c).FillWidth().Shrink(1).Padding(10, 12).Gap(8).Children(func() {
		// ── 标题行：名称 + 状态 ──────────────────────
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, p.Name).FontSize(16).Bold().SingleLine().Shrink(1).
				Selectable().Tooltip(p.Name)

			if rt := a.projectRuntimeOf(p); rt.Total > 0 {
				color := t.TextMuted
				label := locales.ComposePartialRunning(rt.Running, rt.Total)
				if rt.All() {
					color = t.Success
				}
				ui.Badge(c, label).TextColor(color)
			}
		})

		// ── 操作行：按钮 ─────────────────────────────
		busy := a.composeBusy(p.Dir)
		state := a.projectRuntimeOf(p)
		a.projectActions(c, p, busy, state)

		// ── 文件信息行 ───────────────────────────────
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			files := p.Config.ComposeFiles
			if len(files) > 0 {
				ui.Text(c, locales.ComposeFiles(joinNames(files))).FontSize(11).
					TextColor(t.TextMuted).SingleLine().Shrink(1).Selectable().
					Tooltip(strings.Join(files, "\n"))
			}
			if p.Config.EnvFile != nil && *p.Config.EnvFile != "" {
				ui.Text(c, "env: "+baseName(*p.Config.EnvFile)).FontSize(11).
					TextColor(t.TextMuted).SingleLine().Shrink(1).Selectable().
					Tooltip(*p.Config.EnvFile)
			}
		})
	})
}

// projectActions 绘制项目的操作按钮行。
//
// 按钮可用性由项目实际状态决定，而不是只看「有没有动作在跑」：
//   - 已全部运行：启动不可用（点了无事发生）
//   - 完全没在跑：停止/重启不可用
//   - 没有容器：移除不可用
//
// 这样按钮「灰掉」本身就是信息，用户不必点一次才知道。
func (a *App) projectActions(c *ui.Context, p compose.Project, busy bool, state projectRuntime) {
	t := c.Theme()

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		// 启动
		upBtn := ui.PrimaryButton(c, locales.ComposeStart()).
			Tooltip(locales.ComposeStartTooltip()).
			Disabled(busy || state.All())
		switch {
		case state.All():
			upBtn.Tooltip(locales.ComposeAlreadyRunning())
		case busy:
			upBtn.Children(func() {
				ui.Spinner(c).Size(12, 12).Label(locales.ComposeExecuting())
				ui.Text(c, locales.ComposeExecutingEllipsis())
			})
		}
		if !busy && !state.All() && upBtn.Clicked() {
			a.composeAction(p, compose.ActionUp, locales.ComposeStarted(p.Name))
		}

		// 停止
		stopBtn := ui.Button(c, locales.ComposeStop()).
			Tooltip(locales.ComposeStopTooltip()).
			Disabled(busy || state.None())
		if state.None() {
			stopBtn.Tooltip(locales.ComposeNotRunning())
		}
		if stopBtn.Clicked() && !busy && !state.None() {
			a.composeAction(p, compose.ActionStop, locales.ComposeStopped(p.Name))
		}

		// 重启
		restartBtn := ui.Button(c, locales.ComposeRestart()).
			Tooltip(locales.ComposeRestartTooltip()).
			Disabled(busy || state.None())
		if state.None() {
			restartBtn.Tooltip(locales.ComposeNotRunning())
		}
		if restartBtn.Clicked() && !busy && !state.None() {
			a.composeAction(p, compose.ActionRestart, locales.ComposeRestarted(p.Name))
		}

		vSep(c)

		// 移除（down）：危险且低频，但仍放主行——两行布局后空间够用。
		down := ui.Button(c, locales.ComposeDown()).
			Tooltip(locales.ComposeDownTooltip()).
			Disabled(busy || state.Total == 0)
		down.TextColor(t.Danger)
		if state.Total == 0 {
			down.Tooltip(locales.ComposeNothingToRemove())
		}
		if down.Clicked() && !busy && state.Total > 0 {
			name, dir := p.Name, p.Dir
			vols := a.downVolumes
			volSuffix := ""
			if vols {
				volSuffix = locales.ComposeRemoveWithVolumes()
			}
			a.confirm(locales.ComposeRemoveContainers(),
				locales.ComposeRemoveConfirm(name, volSuffix), vols, func() {
					a.composeRun(dir, compose.ActionDown,
						compose.ActionOptions{Volumes: vols},
						locales.ComposeRemoved(name))
				})
		}

		// 更多
		more := ui.Button(c, "").Label(locales.CommonMore()).Tooltip(locales.CommonMoreActions())
		more.Children(func() { ui.Icon(c, icon("settings")).FontSize(14) })
		more.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(locales.ImagesPullTitle()).Chosen() {
				a.composeAction(p, compose.ActionPull, locales.ComposePulled(p.Name))
			}
			if m.Item(locales.ComposeBuildImage()).Chosen() {
				a.composeAction(p, compose.ActionBuild, locales.ComposeBuilt(p.Name))
			}
			m.Separator()
			if m.Item(locales.ContainersShowStats()).Chosen() {
				a.composePS(p)
			}
			if m.Item(locales.ComposePreviewUp()).Chosen() {
				a.composeDryRun(p)
			}
			m.Separator()
			if m.Item(locales.CommonOpenInExplorer()).Chosen() {
				a.openInExplorer(p.Dir)
			}
			if m.Item(locales.ComposeRemoveFromList()).Chosen() {
				a.RemoveProject(p.Dir)
			}
		})
	})
}

// serviceTable 绘制项目中的服务定义。
func (a *App) serviceTable(c *ui.Context, p compose.Project) {
	t := c.Theme()
	names := p.ServiceNames()
	if len(names) == 0 {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, locales.ComposeNoServices()).TextColor(t.TextMuted)
		})
		return
	}

	cols := []ui.TableColumn{
		// 详情区默认只有约 678（1180 窗口），要同时容纳服务表和
		// 启动选项栏。第一栏默认 400，列宽总和必须控制在 400 以内，
		// 否则最后一列会被压成 0 宽（曾实测「副本数」列宽 = 0）。
		// 分隔条可拖动，想看更宽的服务表可以自己拉。
		{Title: locales.ComposeServices(), Width: 96, MinWidth: 56},
		{Title: locales.ContainersImage(), MinWidth: 84},
		{Title: locales.CommonPort(), Width: 96, MinWidth: 52},
		{Title: locales.CommonDependsOn(), Width: 72, MinWidth: 40},
		{Title: locales.CommonReplicas(), Width: 40, MinWidth: 30, Align: ui.End},
	}

	a.serviceList.Key = func(i int) any {
		if i < len(names) {
			return names[i]
		}
		return i
	}
	a.serviceList.Selected = &a.serviceSel
	a.serviceList.RoundRows = true
	styleRows(&a.serviceList, t)
	syncRowSelection(&a.serviceList, a.serviceSel)

	table := ui.Table(c, &a.serviceList, cols, len(names), func(row, col int) {
		if row < 0 || row >= len(names) {
			return
		}
		name := names[row]
		svc := p.Config.Services[name]
		switch col {
		case 0:
			ui.Text(c, name).Bold().SingleLine()
		case 1:
			text := svc.Image
			if svc.Build != nil {
				if text != "" {
					text += locales.ComposeLocalBuild()
				} else {
					text = locales.ComposeBuildContextValue(orDefault(svc.Build.Context, "."))
				}
			}
			if text == "" {
				text = "—"
			}
			ui.Text(c, text).SingleLine().TextColor(t.TextMuted)
		case 2:
			ports := make([]string, 0, len(svc.Ports))
			for _, pm := range svc.Ports {
				ports = append(ports, pm.String())
			}
			txt := strings.Join(ports, ", ")
			if txt == "" {
				txt = "—"
			}
			ui.Text(c, txt).SingleLine().TextColor(t.TextMuted)
		case 3:
			txt := strings.Join(svc.DependsOn, ", ")
			if txt == "" {
				txt = "—"
			}
			ui.Text(c, txt).SingleLine().TextColor(t.TextMuted)
		case 4:
			n := svc.Replicas
			if n == 0 {
				n = 1
			}
			ui.Text(c, fmt.Sprint(n)).TextColor(t.TextMuted)
		}
	}).Grow(1).Label(locales.ComposeServiceList())

	if table.Submitted() {
		if a.serviceSel >= 0 && a.serviceSel < len(names) {
			a.showServiceDetail(p, names[a.serviceSel])
		}
	}

	table.ContextMenu(func(m *ui.Menu) {
		if a.serviceSel < 0 || a.serviceSel >= len(names) {
			return
		}
		name := names[a.serviceSel]
		svc := p.Config.Services[name]
		if m.Item(locales.ComposeStartService()).Chosen() {
			a.composeRun(p.Dir, compose.ActionUp, compose.ActionOptions{Detach: true, Services: []string{name}}, locales.ComposeServiceStarted(name))
		}
		if m.Item(locales.ComposeStopService()).Chosen() {
			a.composeRun(p.Dir, compose.ActionStop, compose.ActionOptions{Services: []string{name}}, locales.ComposeServiceStopped(name))
		}
		if m.Item(locales.ComposeRestartService()).Chosen() {
			a.composeRun(p.Dir, compose.ActionRestart, compose.ActionOptions{Services: []string{name}}, locales.ComposeServiceRestarted(name))
		}
		m.Separator()
		if m.Item(locales.CommonLogs()).Chosen() {
			a.composeLogs(p, name, svc)
		}
		if m.Item(locales.ImagesShowConfig()).Chosen() {
			a.showServiceDetail(p, name)
		}
	})
}

// orDefault 在字符串为空时返回默认值。
func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// showServiceDetail 展示服务的解析后配置。
func (a *App) showServiceDetail(p compose.Project, name string) {
	a.showOutput(locales.ComposeServiceTitle(name), serviceDetailText(p, name))
}

// serviceDetailText 生成服务的解析后配置文本。
//
// 抽成独立函数是为了让视图测试能直接断言内容，而不依赖输出面板。
func serviceDetailText(p compose.Project, name string) string {
	svc := p.Config.Services[name]
	var b strings.Builder
	line := func(k string, v any) {
		fmt.Fprintf(&b, "%-14s %v\n", k+":", v)
	}

	line(locales.ComposeServices(), name)
	line(locales.ComposeProjects(), p.Config.Name)
	if svc.Image != "" {
		line(locales.NavImages(), svc.Image)
	}
	if svc.Build != nil {
		line(locales.CommonBuildContext(), svc.Build.Context)
		if svc.Build.Dockerfile != "" {
			line("Dockerfile", svc.Build.Dockerfile)
		}
	}
	if svc.Container != "" {
		line(locales.ContainersName(), svc.Container)
	}
	line(locales.CommonReplicas(), svc.Replicas)

	if len(svc.Ports) > 0 {
		b.WriteString(locales.ComposeDetailPorts())
		for _, pm := range svc.Ports {
			fmt.Fprintf(&b, "  - %s\n", pm.String())
		}
	}
	if len(svc.Volumes) > 0 {
		b.WriteString(locales.ComposeDetailMounts())
		for _, v := range svc.Volumes {
			fmt.Fprintf(&b, "  - [%s] %s\n", orDefault(v.Type, "volume"), v.String())
		}
	}
	if len(svc.Env) > 0 {
		b.WriteString(locales.ComposeDetailEnv())
		keys := make([]string, 0, len(svc.Env))
		for k := range svc.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := ""
			if svc.Env[k] != nil {
				v = *svc.Env[k]
			}
			fmt.Fprintf(&b, "  - %s=%s\n", k, v)
		}
	}
	if len(svc.DependsOn) > 0 {
		b.WriteString(locales.ComposeDetailDependsOn())
		for _, d := range svc.DependsOn {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
	}
	if len(svc.Networks) > 0 {
		b.WriteString(locales.ComposeDetailNetworks())
		for _, n := range svc.Networks {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	if len(svc.Profiles) > 0 {
		line("Profiles", strings.Join(svc.Profiles, ", "))
	}
	if svc.Restart != nil && *svc.Restart != "" {
		line(locales.ComposeRestartPolicy(), *svc.Restart)
	}
	if svc.Health != nil && !svc.Health.Disable {
		b.WriteString(locales.ComposeDetailHealthcheck())
		if len(svc.Health.Test) > 0 {
			b.WriteString(locales.ComposeDetailHealthCommand(strings.Join(svc.Health.Test, " ")))
		}
		if svc.Health.Interval != "" {
			b.WriteString(locales.ComposeDetailHealthInterval(svc.Health.Interval))
		}
		if svc.Health.Timeout != "" {
			b.WriteString(locales.ComposeDetailHealthTimeout(svc.Health.Timeout))
		}
		if svc.Health.Retries > 0 {
			b.WriteString(locales.ComposeDetailHealthRetries(svc.Health.Retries))
		}
	}
	if len(svc.Labels) > 0 {
		b.WriteString(locales.ComposeDetailLabels())
		keys := make([]string, 0, len(svc.Labels))
		for k := range svc.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  - %s=%s\n", k, svc.Labels[k])
		}
	}

	return b.String()
}

// optionSwitch 画一行「开关 + 标签」，描述另起一行。
//
// 不用 Form/Field 的原因见调用处：窄栏里标签列会挤掉描述的空间。
func (a *App) optionSwitch(c *ui.Context, label, desc string, value *bool) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Switch(c, value).Label(label)
			ui.Text(c, label).FontSize(12).Shrink(1).SingleLine().Grow(1)
		})
		ui.Text(c, desc).FontSize(11).TextColor(t.TextMuted).Grow(1)
	})
}

// projectOptions 是项目右侧的操作选项面板。
func (a *App) projectOptions(c *ui.Context, p compose.Project) {
	t := c.Theme()

	ui.Column(c).FillWidth().Shrink(1).FillHeight().Padding(10, 12).Gap(10).Children(func() {
		ui.Text(c, locales.ComposeStartOptions()).FontSize(12).Bold().TextColor(t.TextMuted)

		// 这里刻意不用 ui.Form：Form 会把标签排成独立左列、描述再占一列，
		// 在这个窄栏里标签列先吃掉一半宽度，描述被压成
		// "Equival / ent to / up -d" 这种逐词换行（用户截图确认过）。
		// 改成「标签 + 开关同一行、描述另起一行」，宽度全给文字。
		a.optionSwitch(c, locales.ImagesPullRunning(),
			locales.ComposeUpTooltip(), &a.upDetach)
		a.optionSwitch(c, locales.ComposeStartFirst(),
			locales.ComposeBuildTooltip(), &a.upBuild)
		a.optionSwitch(c, locales.ComposeRemoveVolumes(),
			locales.ComposeRemoveVolumesTooltip(), &a.downVolumes)

		ui.Divider(c)

		ui.Text(c, locales.ComposeProjectPath()).FontSize(12).Bold().TextColor(t.TextMuted)
		// SingleLine + Shrink：路径和命令都很长，不限制会把这一栏撑宽，
		// 进而把整个右栏推出窗口（截图里「启动选项」被裁在窗口右边）。
		ui.Text(c, p.Dir).FontSize(11).TextColor(t.TextMuted).
			SingleLine().Shrink(1).Selectable().Tooltip(p.Dir)

		ui.Spacer(c)

		ui.Text(c, locales.CommonCommandPreview()).FontSize(12).Bold().TextColor(t.TextMuted)
		ui.Text(c, a.upPreview(p)).FontSize(11).TextColor(t.TextMuted).
			SingleLine().Shrink(1).Selectable().Tooltip(a.upPreview(p))
	})
}

// upPreview 生成一条「将要执行的命令」提示文本。
func (a *App) upPreview(p compose.Project) string {
	args := []string{"wslc-compose", "up"}
	if a.upDetach {
		args = append(args, "-d")
	}
	if a.upBuild {
		args = append(args, "--build")
	}
	return strings.Join(args, " ")
}

// composeAction 对项目执行一个编排动作（使用界面上的选项）。
func (a *App) composeAction(p compose.Project, action compose.Action, done string) {
	opt := a.currentActionOptions()
	a.composeRun(p.Dir, action, opt, done)
}

// currentActionOptions 读取界面上的选项。
func (a *App) currentActionOptions() compose.ActionOptions {
	return compose.ActionOptions{
		Detach:  a.upDetach,
		Build:   a.upBuild,
		Volumes: a.downVolumes,
	}
}

// composeRun 在后台执行 wslc-compose 动作，并把输出实时显示在面板里。
//
// 之前用的是 runAsync：它在后台跑完才一次性把结果写进面板，
// 而 compose 页根本不渲染输出面板，a.busy 也没有任何界面读取，
// 于是 up / down / build 这类可能跑几十秒的操作看起来完全没反应。
// 现在改为：立即打开面板 → 逐行流式刷新 → 结束时给出总结。
func (a *App) composeRun(dir string, action compose.Action, opt compose.ActionOptions, done string) {
	stream := a.composeStream
	if stream == nil {
		return
	}

	// 状态叠加：同一项目上的操作不互相覆盖面板内容。
	key := "compose-" + string(action) + "-" + dir
	title := composeActionTitle(action) + " · " + baseName(dir)

	optDesc := ""
	if opt.Detach {
		optDesc += " -d"
	}
	if opt.Build {
		optDesc += " --build"
	}
	if opt.Volumes {
		optDesc += " -v"
	}
	if len(opt.Services) > 0 {
		optDesc += " " + strings.Join(opt.Services, " ")
	}
	a.logInfo("compose", "%s: wslc-compose %s%s（%s）",
		title, string(action), optDesc, dir)

	a.update(func() {
		a.outputTitle = title
		a.outputText = locales.ComposeRunningAction(string(action)) + " …\n"
		a.outputOpen = true
		a.outputBusy = true
		a.outputLines = 0
		a.setBusy(key, true)
	})

	go func() {
		ctx, cancel := longContext()
		defer cancel()

		var mu sync.Mutex
		var lines []string

		appendLine := func(line string) {
			mu.Lock()
			lines = append(lines, line)
			text := strings.Join(lines, "\n")
			n := len(lines)
			mu.Unlock()

			a.update(func() {
				a.outputText = text
				a.outputLines = n
			})
		}

		res, err := stream(ctx, dir, action, opt, appendLine)

		mu.Lock()
		final := strings.Join(lines, "\n")
		mu.Unlock()

		failed := err != nil || !res.Ok()
		switch {
		case err != nil:
			if final != "" {
				final += "\n\n"
			}
			final += locales.ComposeExecuteFailed() + err.Error()
		case !res.Ok():
			if final != "" {
				final += "\n\n"
			}
			final += locales.ComposeExecuteFailed() + res.Error().Error()
		default:
			if final == "" {
				final = locales.CommonNoOutput()
			}
			final += locales.ComposeFinishWithDuration(res.Duration.Round(time.Millisecond).String())
		}

		a.update(func() {
			a.outputText = final
			a.outputBusy = false
			a.outputLines = 0
			a.setBusy(key, false)
			if failed {
				a.lastError = firstLine(final)
				a.toast(locales.ComposeOperationFailed() + firstLine(final))
			} else if done != "" {
				a.toast(done)
			}
		})
		a.Refresh()
	}()
}

// composeBusy 报告某个项目目录上是否有编排动作正在执行。
func (a *App) composeBusy(dir string) bool {
	a.busyMu.Lock()
	defer a.busyMu.Unlock()
	for key, on := range a.busy {
		if !on {
			continue
		}
		if strings.HasPrefix(key, "compose-") && strings.HasSuffix(key, dir) {
			return true
		}
	}
	return false
}

// composeActionTitle 把动作翻译成面板标题里的中文。
func composeActionTitle(action compose.Action) string {
	switch action {
	case compose.ActionUp:
		return locales.ComposeStart()
	case compose.ActionDown:
		return locales.ComposeDown()
	case compose.ActionStart:
		return locales.ComposeStart()
	case compose.ActionStop:
		return locales.ComposeStop()
	case compose.ActionRestart:
		return locales.ComposeRestart()
	case compose.ActionPull:
		return locales.ImagesPullTitle()
	case compose.ActionBuild:
		return locales.ComposeBuildImage()
	case compose.ActionLogs:
		return locales.CommonLogs()
	case compose.ActionPS:
		return locales.ContainersState()
	}
	return string(action)
}

// composePS 显示项目当前的容器状态。
func (a *App) composePS(p compose.Project) {
	if a.compose == nil {
		return
	}
	go func() {
		ctx, cancel := longContext()
		defer cancel()
		entries, err := a.compose.PS(ctx, p.Dir, true)

		a.update(func() {
			if err != nil {
				a.showOutput(locales.ComposeContainerState(p.Name), locales.ComposeReadFailed()+err.Error())
				return
			}
			if len(entries) == 0 {
				a.showOutput(locales.ComposeContainerState(p.Name), locales.ComposeNoContainers())
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%-32s %-16s %-28s %s\n", locales.CommonName(), locales.ComposeServices(), locales.NavImages(), locales.CommonStatus())
			for _, e := range entries {
				ports := ""
				if e.Ports != "" {
					ports = "  " + e.Ports
				}
				fmt.Fprintf(&b, "%-32s %-16s %-28s %s%s\n", e.Name, e.Service, e.Image, e.Status, ports)
			}
			a.showOutput(locales.ComposeContainerState(p.Name), b.String())
		})
	}()
}

// composeDryRun 预览 up 将要执行的 wslc 命令。
func (a *App) composeDryRun(p compose.Project) {
	if a.compose == nil {
		return
	}
	opt := a.currentActionOptions()
	go func() {
		ctx, cancel := longContext()
		defer cancel()
		out, err := a.compose.DryRun(ctx, p.Dir, compose.ActionUp, opt)
		a.update(func() {
			if err != nil {
				a.showOutput(locales.ComposeUpPreview(p.Name), locales.ComposeGenerateFailed()+err.Error()+"\n\n"+out)
				return
			}
			a.showOutput(locales.ComposeUpPreview(p.Name), out)
		})
	}()
}

// composeLogs 显示某个服务的日志。
func (a *App) composeLogs(p compose.Project, service string, svc compose.ServiceConfig) {
	if a.compose == nil {
		return
	}
	// 找到该服务的容器：标签 com.wslc-compose.project + service。
	var target *wslcContainerRef
	for i := range a.containers {
		ct := a.containers[i]
		if ct.Label("com.wslc-compose.project") == p.Config.Name &&
			ct.Label("com.wslc-compose.service") == service {
			target = &wslcContainerRef{ID: ct.ID, Name: displayName(ct)}
			break
		}
	}
	if target == nil {
		a.toast(locales.ComposeHasNoContainers(service))
		return
	}
	if a.wslc == nil {
		return
	}
	a.update(func() {
		a.outputTitle = locales.ComposeLogsTitle(p.Name + "/" + service)
		a.outputText = locales.StatusLoading()
		a.outputOpen = true
		a.outputBusy = true
	})
	go func() {
		ctx, cancel := longContext()
		defer cancel()
		out, err := a.wslc.Logs(ctx, target.ID, 500, false)
		a.update(func() {
			a.outputBusy = false
			if err != nil {
				a.outputText = locales.CommonReadLogsFailed() + err.Error() + "\n\n" + out
				return
			}
			if strings.TrimSpace(out) == "" {
				a.outputText = locales.ComposeLogsEmpty()
				return
			}
			a.outputText = out
		})
	}()
}

// wslcContainerRef 是内部使用的容器引用。
type wslcContainerRef struct {
	ID   string
	Name string
}

// projectRuntime 描述一个 Compose 项目的运行状态，用于决定按钮可用性。
type projectRuntime struct {
	// Total 是该项目名下的容器总数。
	Total int
	// Running 是其中处于运行中的数量。
	Running int
}

// Any 表示至少有一个容器在跑。
func (r projectRuntime) Any() bool { return r.Running > 0 }

// All 表示全部容器都在跑（且确实有容器）。
func (r projectRuntime) All() bool { return r.Total > 0 && r.Running == r.Total }

// None 表示没有容器在跑。
func (r projectRuntime) None() bool { return r.Running == 0 }

// projectRuntimeOf 统计某个项目的容器运行状态。
//
// 直接复用已加载的容器列表（它们带 com.wslc-compose.project 标签），
// 不再额外调用 `wslc-compose ps`：每个项目一次额外进程会让
// 项目列表刷新变得很慢，而容器列表本来就随刷新更新。
func (a *App) projectRuntimeOf(p compose.Project) projectRuntime {
	var rt projectRuntime
	for _, ct := range a.containers {
		if ct.Label("com.wslc-compose.project") != p.Name {
			continue
		}
		rt.Total++
		if ct.IsRunning() {
			rt.Running++
		}
	}
	return rt
}

// joinNames 把文件路径列表渲染成短名字，用「, 」连接。
//
// 完整路径放在 Tooltip 里，界面上只显示文件名，
// 避免一行里出现好几个绝对路径把宽度撑开。
func joinNames(paths []string) string {
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, baseName(p))
	}
	return strings.Join(names, ", ")
}
