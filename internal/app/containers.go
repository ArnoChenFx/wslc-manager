package app

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/locales"
)

// filteredContainers 返回按搜索框和「隐藏已停止」过滤后的容器列表。
func (a *App) filteredContainers() []wslc.Container {
	q := strings.ToLower(strings.TrimSpace(a.query))
	out := make([]wslc.Container, 0, len(a.containers))
	for _, ct := range a.containers {
		if a.hideStopped && !ct.IsRunning() {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(ct.Names+" "+ct.Image+" "+ct.Status), q) {
			continue
		}
		out = append(out, ct)
	}
	return out
}

// containersPage 是容器管理页：上方表格 + 下方详情/日志面板。
func (a *App) containersPage(c *ui.Context) {
	rows := a.filteredContainers()

	// 操作条固定在上方。
	a.containersActionBar(c, rows)
	ui.Divider(c)

	if a.outputOpen {
		// 表格与输出面板用可拖动的上下分栏，和 Docker Desktop 一致：
		// 用户能根据日志长短自己调比例。
		ui.SplitVerticalPercent(c, &a.outputPercent, func() {
			a.containerTable(c, rows)
		}, func() {
			a.outputPanel(c)
		}).Grow(1)
		return
	}

	// 只有表格时让它占满剩余高度。
	a.containerTable(c, rows)
}

// containersActionBar 绘制容器页的操作按钮。
func (a *App) containersActionBar(c *ui.Context, rows []wslc.Container) {
	t := c.Theme()
	sel := a.selectedContainer(rows)

	ui.Row(c).Padding(8, 12).Gap(6).AlignItems(ui.Center).Children(func() {
		// 运行
		a.iconAction(c, "play", locales.CommonStart(), locales.ContainersStart(),
			sel != nil && !sel.IsRunning(), func() {
				a.containerAction(wslc.ActionStart, sel.ID,
					locales.CommonStarted()+sel.Names)
			})

		// 停止
		a.iconAction(c, "stop", locales.CommonStop(), locales.ContainersStop(),
			sel != nil && sel.IsRunning(), func() {
				a.containerAction(wslc.ActionStop, sel.ID,
					locales.CommonStopped()+sel.Names)
			})

		// 重启
		a.iconAction(c, "restart", locales.CommonRestart(), locales.ContainersRestart(),
			sel != nil && sel.IsRunning(), func() {
				a.containerAction(wslc.ActionRestart, sel.ID,
					locales.CommonRestarted()+sel.Names)
			})

		vSep(c)

		// 日志
		if ui.Button(c, locales.CommonLogs()).Tooltip(locales.ContainersShowLogs()).
			Disabled(sel == nil).Clicked() && sel != nil {
			a.loadLogs(*sel)
		}
		// 检查
		if ui.Button(c, locales.CommonCheck()).Tooltip(locales.ContainersShowInspect()).
			Disabled(sel == nil).Clicked() && sel != nil {
			a.loadInspect(*sel)
		}
		// 统计
		if ui.Button(c, locales.CommonStats()).Tooltip(locales.ContainersShowStats()).
			Disabled(sel == nil).Clicked() && sel != nil {
			a.loadStats(*sel)
		}

		ui.Spacer(c)

		// 删除
		delBtn := ui.Button(c, locales.CommonDelete()).Tooltip(locales.ContainersForceDelete()).
			Disabled(sel == nil)
		delBtn.TextColor(t.Danger)
		if delBtn.Clicked() && sel != nil {
			name := sel.Names
			id := sel.ID
			a.confirm(locales.ContainersDelete(), locales.ContainersConfirmDelete(name), true, func() {
				a.containerAction(wslc.ActionRemove, id, locales.CommonDeleted()+name)
			})
		}

		// 隐藏已停止
		hide := a.hideStopped
		if ui.Checkbox(c, &hide, locales.ContainersHideStopped()).Changed() {
			a.hideStopped = hide
			a.settings.HideStopped = hide
			a.saveSettings()
		}
	})
}

// vSep 在 Row 里画一根竖直分隔条。
//
// 不能用 ui.Divider：它在父容器方向为横时画的是横线，
// 放在 Row 里会变成拉满宽度的一条线，把工具栏高度撑开。
func vSep(c *ui.Context) {
	ui.Box(c).Width(1).Height(18).Background(c.Theme().Border).Margin(0, 4)
}

// selectedContainer 返回当前选中的容器，未选中时返回 nil。
//
// 注意：表格的选中状态由 ListState 维护，而选中项可能因刷新而移动，
// 因此这里用 ID 而不是下标来定位。
func (a *App) selectedContainer(rows []wslc.Container) *wslc.Container {
	if a.containerSel < 0 || a.containerSel >= len(rows) {
		return nil
	}
	return &rows[a.containerSel]
}

// containerTable 绘制容器表格。
func (a *App) containerTable(c *ui.Context, rows []wslc.Container) {
	t := c.Theme()

	// 没有数据时只画空状态。
	//
	// 之前先建一个 Grow(1) 的表格、再追加一个 Fill() 的空状态，
	// 两个都要求占满剩余空间，导致文字位置飘移、与表头重叠。
	if len(rows) == 0 {
		a.emptyState(c, locales.ContainersEmptyTitle(), locales.ContainersEmptyBody())
		return
	}

	cols := []ui.TableColumn{
		{Title: locales.ContainersName(), Width: 190, MinWidth: 120},
		{Title: locales.CommonStatus(), Width: 110, MinWidth: 80},
		{Title: locales.NavImages(), Width: 220, MinWidth: 120},
		{Title: locales.CommonPort(), Width: 160, MinWidth: 80},
		{Title: locales.ComposeProjects(), Width: 130, MinWidth: 80},
	}

	// 用容器 ID 作为行的稳定标识，刷新时选中项跟着容器走。
	a.containerList.Key = func(i int) any {
		if i < len(rows) {
			return rows[i].ID
		}
		return i
	}
	a.containerList.Selected = &a.containerSel
	a.containerList.RoundRows = true
	styleRows(&a.containerList, t)
	syncRowSelection(&a.containerList, a.containerSel)

	table := ui.Table(c, &a.containerList, cols, len(rows), func(row, col int) {
		if row < 0 || row >= len(rows) {
			return
		}
		ct := rows[row]

		// 选中行的底色带一点强调色（见 styleRows），比普通行更亮/更暗。
		// 次要文字（TextMuted）压在上面会明显发灰、看不清，
		// 所以选中时改用正常前景色——这是实测截图确认过的问题。
		selected := a.containerList.CellSelected(row, col)
		muted := t.TextMuted
		if selected {
			muted = t.Text
		}

		switch col {
		case 0:
			// 名称前放一个状态圆点，用颜色区分运行/停止。
			//
			// 用 ui.Icon 而不是裸的 Box：图标与文字同属「行内」类元素，
			// 共享同一套行高与基线规则，对齐由引擎保证；
			// 裸 Box 只能按盒子居中，在文字行里容易看起来偏上。
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				dotColor := t.TextMuted
				switch {
				case ct.IsRunning():
					dotColor = t.Success
				case strings.EqualFold(ct.State, "exited"):
					dotColor = t.TextMuted
				default:
					dotColor = t.Warning
				}
				ui.Icon(c, icon("dot")).FontSize(11).TextColor(dotColor)
				ui.Text(c, displayName(ct)).SingleLine()
			})
		case 1:
			color := muted
			if ct.IsRunning() && !selected {
				color = t.Success
			}
			ui.Text(c, ct.StateLabel()).SingleLine().TextColor(color)
		case 2:
			ui.Text(c, ct.Image).SingleLine().TextColor(muted)
		case 3:
			ui.Text(c, truncate(ct.Ports, 40)).SingleLine().TextColor(muted)
		case 4:
			proj := ct.Label("com.wslc-compose.project")
			if proj == "" {
				proj = "—"
			}
			ui.Text(c, proj).SingleLine().TextColor(muted)
		}
	}).Grow(1).Label(locales.ContainersTitle())

	// 双击行 = 打开日志
	if table.Submitted() {
		if sel := a.selectedContainer(rows); sel != nil {
			a.loadLogs(*sel)
		}
	}

	// 右键菜单
	table.ContextMenu(func(m *ui.Menu) {
		sel := a.selectedContainer(rows)
		if sel == nil {
			return
		}
		if m.Item(locales.CommonStart()).Disabled(sel.IsRunning()).Chosen() {
			a.containerAction(wslc.ActionStart, sel.ID, locales.CommonStarted()+sel.Names)
		}
		if m.Item(locales.CommonStop()).Disabled(!sel.IsRunning()).Chosen() {
			a.containerAction(wslc.ActionStop, sel.ID, locales.CommonStopped()+sel.Names)
		}
		if m.Item(locales.CommonRestart()).Disabled(!sel.IsRunning()).Chosen() {
			a.containerAction(wslc.ActionRestart, sel.ID, locales.CommonRestarted()+sel.Names)
		}
		m.Separator()
		if m.Item(locales.ContainersShowLogs()).Chosen() {
			a.loadLogs(*sel)
		}
		if m.Item(locales.CommonDetails()).Chosen() {
			a.loadInspect(*sel)
		}
		if m.Item(locales.ContainersCopyID()).Chosen() {
			c.WriteClipboard(sel.ID)
			a.toast(locales.CommonCopied() + " " + sel.ID)
		}
		m.Separator()
		if m.Item(locales.CommonDelete()).Chosen() {
			name, id := sel.Names, sel.ID
			a.confirm(locales.ContainersDelete(), locales.ContainersConfirmDelete(name), true, func() {
				a.containerAction(wslc.ActionRemove, id, locales.CommonDeleted()+name)
			})
		}
	})
}

// displayName 返回容器名；wslc 有时会给空名。
func displayName(ct wslc.Container) string {
	if n := strings.TrimSpace(ct.Names); n != "" {
		return n
	}
	return ct.ID
}

// containerAction 对容器执行生命周期动作。
func (a *App) containerAction(action wslc.ContainerAction, id, doneMsg string) {
	if a.wslc == nil {
		return
	}
	label := map[wslc.ContainerAction]string{
		wslc.ActionStart:   locales.CommonStart(),
		wslc.ActionStop:    locales.CommonStop(),
		wslc.ActionRestart: locales.CommonRestart(),
		wslc.ActionRemove:  locales.CommonDelete(),
		wslc.ActionKill:    locales.CommonTerminate(),
	}[action]

	a.runAsync("container-"+string(action)+"-"+id, doneMsg, func(ctx context.Context) (string, error) {
		return "", a.wslc.ActOnContainer(ctx, action, id)
	})
	_ = label
}

// loadLogs 读取容器日志并显示在输出面板。
func (a *App) loadLogs(ct wslc.Container) {
	if a.wslc == nil {
		return
	}
	a.update(func() {
		a.outputTitle = locales.CommonLogs() + " · " + displayName(ct)
		a.outputText = locales.StatusLoading()
		a.outputOpen = true
		a.outputBusy = true
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60_000_000_000)
		defer cancel()
		out, err := a.wslc.Logs(ctx, ct.ID, 500, false)
		a.update(func() {
			a.outputBusy = false
			if err != nil {
				a.outputText = locales.CommonReadLogsFailed() + err.Error() + "\n\n" + out
				return
			}
			if strings.TrimSpace(out) == "" {
				a.outputText = locales.ContainersLogsEmpty()
				return
			}
			a.outputText = out
		})
	}()
}

// loadInspect 读取容器 inspect 详情。
func (a *App) loadInspect(ct wslc.Container) {
	if a.wslc == nil {
		return
	}
	a.update(func() {
		a.outputTitle = locales.CommonDetails() + " · " + displayName(ct)
		a.outputText = locales.StatusLoading()
		a.outputOpen = true
		a.outputBusy = true
	})
	go func() {
		ctx, cancel := longContext()
		defer cancel()
		out, err := a.wslc.InspectContainer(ctx, ct.ID)
		a.update(func() {
			a.outputBusy = false
			if err != nil {
				a.outputText = locales.CommonReadDetailsFailed() + err.Error()
				return
			}
			a.outputText = out
		})
	}()
}

// loadStats 读取容器资源占用。
func (a *App) loadStats(ct wslc.Container) {
	if a.wslc == nil {
		return
	}
	a.update(func() {
		a.outputTitle = locales.CommonStats() + " · " + displayName(ct)
		a.outputText = locales.StatusLoading()
		a.outputOpen = true
		a.outputBusy = true
	})
	go func() {
		ctx, cancel := longContext()
		defer cancel()
		out, err := a.wslc.Stats(ctx, true)
		a.update(func() {
			a.outputBusy = false
			if err != nil {
				a.outputText = locales.CommonReadStatsFailed() + err.Error() + "\n\n" + out
				return
			}
			if strings.TrimSpace(out) == "" {
				a.outputText = locales.ContainersNoStats()
				return
			}
			a.outputText = prettyStats(out)
		})
	}()
}

// prettyStats 把 stats 的 JSON 行渲染成可读的文本表格。
func prettyStats(raw string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return raw
	}
	return b.String()
}

// outputPanel 是底部的输出面板（日志 / inspect / 统计）。
//
// 它作为 SplitVertical 的下半栏使用，因此自己填满可用高度即可，
// 不需要再指定尺寸。
func (a *App) outputPanel(c *ui.Context) {
	t := c.Theme()

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center).Background(t.Surface).Children(func() {
			ui.Text(c, a.outputTitle).FontSize(12).Bold().SingleLine().Grow(1)
			if a.outputBusy {
				ui.Spinner(c).Size(12, 12).Label(locales.CommonRunning())
				if a.outputLines > 0 {
					ui.Text(c, locales.CommonLineCount(a.outputLines)).FontSize(11).
						TextColor(t.TextMuted).Selectable()
				}
			}
			if ui.Button(c, locales.CommonCopy()).Tooltip(locales.CommonCopyAll()).Clicked() {
				c.WriteClipboard(a.outputText)
				a.toast(locales.CommonCopiedToClipboard())
			}
			if ui.Button(c, "").Label(locales.CommonClose()).Tooltip(locales.ComposeClosePanel()).Children(func() {
				ui.Icon(c, icon("x")).FontSize(14)
			}).Clicked() {
				a.outputOpen = false
			}
		})
		ui.Divider(c)
		// 流式输出时把滚动位置跟到末尾，新进度行才看得见。
		// 用 ScrollState 而不是无状态滚动：只有它能读/写偏移。
		sc := &a.outputScroll
		if a.outputBusy {
			sc.Y = math.MaxFloat32
		}
		ui.Scroll(c).Grow(1).Padding(8, 12).TrackScroll(sc).Children(func() {
			ui.Text(c, a.outputText).FontSize(12).Font("Consolas").PreserveSpaces().Selectable()
		})
	})
}

// confirm 打开确认对话框。
func (a *App) confirm(title, body string, danger bool, action func()) {
	a.update(func() {
		a.confirmTitle = title
		a.confirmBody = body
		a.confirmDanger = danger
		a.confirmAction = action
		a.confirmOpen = true
	})
}

var _ = fmt.Sprint
