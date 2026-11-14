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
				ui.Column(c).Grow(1).Children(func() {
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

	ui.Column(c).Fill().Children(func() {
		// 项目头部
		a.projectHeader(c, *p)
		ui.Divider(c)

		// 主体：服务列表 + 选项
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Grow(1).FillHeight().Children(func() {
				a.serviceTable(c, *p)
			})
			ui.Divider(c)
			ui.Column(c).Width(260).FillHeight().Children(func() {
				a.projectOptions(c, *p)
			})
		})
	})
}

// projectHeader 绘制项目标题与主要动作。
func (a *App) projectHeader(c *ui.Context, p compose.Project) {
	t := c.Theme()

	ui.Column(c).Padding(10, 12).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, p.Name).FontSize(16).Bold().Selectable()
			ui.Text(c, baseName(p.Dir)).FontSize(11).TextColor(t.TextMuted).Selectable().Tooltip(p.Dir)
			ui.Spacer(c)

			// 该目录上是否有编排动作正在跑（用于禁用重复点击 + 显示转圈）。
			running := a.composeBusy(p.Dir)

			// 启动
			upBtn := ui.PrimaryButton(c, locales.ComposeStart()).Tooltip("wslc-compose up").Disabled(running)
			if running {
				// 操作进行中：用转圈图标替代文字，让用户看到它在跑。
				upBtn.Children(func() {
					sp := ui.Spinner(c).Size(12, 12).Label(locales.ComposeExecuting())
					_ = sp
					ui.Text(c, locales.ComposeExecutingEllipsis())
				})
			}
			if !running && upBtn.Clicked() {
				a.composeAction(p, compose.ActionUp, locales.ComposeStarted(p.Name))
			}
			// 停止
			if ui.Button(c, locales.ComposeStop()).Tooltip("wslc-compose stop").Disabled(running).Clicked() && !running {
				a.composeAction(p, compose.ActionStop, locales.ComposeStopped(p.Name))
			}
			// 重启
			if ui.Button(c, locales.ComposeRestart()).Tooltip("wslc-compose restart").Disabled(running).Clicked() && !running {
				a.composeAction(p, compose.ActionRestart, locales.ComposeRestarted(p.Name))
			}
			// 移除（down）
			down := ui.Button(c, locales.ComposeDown()).Tooltip("wslc-compose down")
			down.TextColor(t.Danger)
			if down.Clicked() {
				name, dir := p.Name, p.Dir
				vols := a.downVolumes
				volSuffix := ""
				if vols {
					volSuffix = locales.ComposeRemoveWithVolumes()
				}
				a.confirm(locales.ComposeRemoveContainers(), locales.ComposeRemoveConfirm(name, volSuffix), vols, func() {
					a.composeRun(dir, compose.ActionDown, compose.ActionOptions{Volumes: vols}, locales.ComposeRemoved(name))
				})
			}

			// 更多操作：用 MyGo 在窗口内绘制的菜单按钮，跨平台一致。
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

		// 文件与环境信息
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			files := p.Config.ComposeFiles
			if len(files) > 0 {
				ui.Text(c, locales.ComposeFiles(joinNames(files))).FontSize(11).TextColor(t.TextMuted).Selectable().
					Tooltip(strings.Join(files, "\n"))
			}
			if p.Config.EnvFile != nil && *p.Config.EnvFile != "" {
				ui.Text(c, "env: "+baseName(*p.Config.EnvFile)).FontSize(11).TextColor(t.TextMuted).Selectable().
					Tooltip(*p.Config.EnvFile)
			}
		})
	})
}

// joinNames 把文件路径列表渲染成短名字。
func joinNames(files []string) string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, baseName(f))
	}
	return strings.Join(names, ", ")
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
		{Title: locales.ComposeServices(), Width: 160, MinWidth: 90},
		{Title: locales.ContainersImage(), MinWidth: 180},
		{Title: locales.CommonPort(), Width: 170, MinWidth: 90},
		{Title: locales.CommonDependsOn(), Width: 140, MinWidth: 80},
		{Title: locales.CommonReplicas(), Width: 60, MinWidth: 50, Align: ui.End},
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

// projectOptions 是项目右侧的操作选项面板。
func (a *App) projectOptions(c *ui.Context, p compose.Project) {
	t := c.Theme()

	ui.Column(c).FillHeight().Padding(10, 12).Gap(10).Children(func() {
		ui.Text(c, locales.ComposeStartOptions()).FontSize(12).Bold().TextColor(t.TextMuted)

		ui.Form(c, func() {
			ui.Field(c, locales.ImagesPullRunning(), func() {
				ui.Switch(c, &a.upDetach).Label(locales.ImagesPullRunning())
			}).Description(locales.ComposeUpTooltip())

			ui.Field(c, locales.ComposeStartFirst(), func() {
				ui.Switch(c, &a.upBuild).Label(locales.ComposeStartFirst())
			}).Description(locales.ComposeBuildTooltip())

			ui.Field(c, locales.ComposeRemoveVolumes(), func() {
				ui.Switch(c, &a.downVolumes).Label(locales.ComposeRemoveVolumes())
			}).Description(locales.ComposeRemoveVolumesTooltip())
		})

		ui.Divider(c)

		ui.Text(c, locales.ComposeProjectPath()).FontSize(12).Bold().TextColor(t.TextMuted)
		ui.Text(c, p.Dir).FontSize(11).TextColor(t.TextMuted).Selectable()

		ui.Spacer(c)

		ui.Text(c, locales.CommonCommandPreview()).FontSize(12).Bold().TextColor(t.TextMuted)
		ui.Text(c, a.upPreview(p)).FontSize(11).TextColor(t.TextMuted).PreserveSpaces().Selectable()
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
