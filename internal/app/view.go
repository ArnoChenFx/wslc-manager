package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/locales"
)

// View 是窗口的根视图：顶部工具条 + 左右两栏 + 底部状态栏。
func (a *App) View(c *ui.Context) {
	// 先应用产品配色，后面所有取色都基于它。
	// 必须在取 t := c.Theme() 之前，否则这一帧会用到 MyGo 的默认色。
	a.applyTheme(c)
	t := c.Theme()

	// 消费待弹出的提示。这里做是因为 c.Toast 只能在构建期调用。
	a.flushToasts(c)

	// 开发期自截图（快捷键或 --shot）。必须放在最前面，
	// 这样捕获的是完整一帧而不是半成品。
	a.handleCapture(c)

	// 后台任务准备好的剪贴板内容在这里落地。
	// c.WriteClipboard 只能在构建期调用，所以后台只入队，视图来写。
	if a.pendingClipboard != "" {
		c.WriteClipboard(a.pendingClipboard)
		a.pendingClipboard = ""
	}

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.toolbar(c)
		ui.Divider(c)
		// 用 Split 做左右分栏：引擎自带可拖动的分隔条，
		// 用户能直接拉宽/收窄侧边栏，并把宽度持久化到设置里。
		// 用 Grow(1) 而不是 Fill：Fill 会让这一栏按「整个窗口高度」
		// 去算，把上面工具栏和下面状态栏的高度也算进去，
		// 结果内部再套 SplitVerticalPercent 时可用高度被高估 82px，
		// 下半栏的面板与滚动条被排到窗口之外（实测 Scroll 底部 814 > 760）。
		// Grow(1) 只取「剩余」高度，才是正确的约束。
		ui.Split(c, &a.sidebarW, func() {
			a.sidebar(c)
		}, func() {
			a.content(c)
		}).SplitReset(220).Grow(1)
		ui.Divider(c)
		a.statusBar(c)
	})

	a.dialogs(c)
}

// flushToasts 把排队中的提示弹出。
func (a *App) flushToasts(c *ui.Context) {
	if len(a.pendingToasts) == 0 {
		return
	}
	msgs := a.pendingToasts
	a.pendingToasts = nil
	for _, m := range msgs {
		c.Toast(m)
	}
}

// toolbar 是窗口顶部的操作条：页面标题、搜索框、刷新与设置。
func (a *App) toolbar(c *ui.Context) {
	t := c.Theme()

	ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, pages[a.page].title()).FontSize(15).Bold()

		if n := a.countForPage(); n >= 0 {
			ui.Badge(c, fmt.Sprint(n)).TextColor(t.TextMuted)
		}

		ui.Spacer(c)

		// 搜索框：只影响列表过滤。
		if a.page != PageSystem {
			ui.SearchField(c, &a.query).Placeholder(locales.CommonFilter()).Width(200)
		}

		// 刷新：只画一个图标，加载中让这个图标转动。
		// （之前误把图标画了两次：先单独建一个再放进按钮，导致右上出现两个刷新图标）
		btn := ui.Button(c, "").Label(locales.CommonRefresh()).Tooltip(locales.CommonRefreshTooltip())
		btn.Children(func() {
			ic := ui.Icon(c, icon("refresh")).FontSize(16)
			if a.loading {
				// 让图标随加载状态旋转，作为轻量的进行中指示。
				ic.Rotate(ic.Loop("spin", 900*time.Millisecond, ui.Linear) * 360)
			}
		})
		if btn.Clicked() {
			a.Refresh()
		}

		if btn := ui.Button(c, "").Label(locales.CommonSettings()).Tooltip(locales.CommonSettings()); true {
			btn.Children(func() { ui.Icon(c, icon("settings")).FontSize(16) })
			if btn.Clicked() {
				a.settingsOpen = true
			}
		}
	})

	// F5 刷新
	if c.Shortcut(0, ui.KeyF5) {
		a.Refresh()
	}
}

// countForPage 返回当前页面的条目数，-1 表示不显示。
func (a *App) countForPage() int {
	switch a.page {
	case PageContainers:
		return len(a.containers)
	case PageImages:
		return len(a.images)
	case PageVolumes:
		return len(a.volumes)
	case PageNetworks:
		return len(a.networks)
	case PageCompose:
		return len(a.projects)
	}
	return -1
}

// sidebar 是左侧导航栏。
func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()

	page := a.pageID()
	side := ui.Sidebar(c, &page, func() {
		ui.SidebarSection(c, locales.NavResources(), nil, func() {
			for _, p := range pages {
				if p.Page == PageCompose || p.Page == PageSystem {
					continue
				}
				ui.SidebarItem(c, pageID(p.Page), icon(p.Icon), p.title()).Children(func() {
					if n := a.countOf(p.Page); n > 0 {
						ui.Badge(c, fmt.Sprint(n)).TextColor(t.TextMuted)
					}
				})
			}
		})
		ui.SidebarSection(c, locales.NavOrchestration(), nil, func() {
			ui.SidebarItem(c, pageID(PageCompose), icon("stack"), locales.NavCompose()).Children(func() {
				if n := len(a.projects); n > 0 {
					ui.Badge(c, fmt.Sprint(n)).TextColor(t.TextMuted)
				}
			})
			ui.SidebarItem(c, pageID(PageSystem), icon("cpu"), locales.NavSystem())
		})
	}).Fill()

	if side.Changed() {
		a.page = parsePageID(page)
		a.query = ""
		a.logInfo("ui", "切换到页面 %s", pages[a.page].title())
	}
}

// countOf 返回某类资源的数量。
func (a *App) countOf(p Page) int {
	switch p {
	case PageContainers:
		return len(a.containers)
	case PageImages:
		return len(a.images)
	case PageVolumes:
		return len(a.volumes)
	case PageNetworks:
		return len(a.networks)
	case PageCompose:
		return len(a.projects)
	}
	return 0
}

// pageID 把 Page 转成侧边栏使用的稳定字符串 ID。
func pageID(p Page) string { return fmt.Sprintf("page-%d", int(p)) }

// parsePageID 把侧边栏 ID 转回 Page。
func parsePageID(s string) Page {
	var n int
	if _, err := fmt.Sscanf(s, "page-%d", &n); err != nil {
		return PageContainers
	}
	if n < 0 || n > int(PageSystem) {
		return PageContainers
	}
	return Page(n)
}

// a.pageID 返回当前页面的 ID。
func (a *App) pageID() string { return pageID(a.page) }

// content 根据当前页面构建主内容区。
//
// 关键：每个页面都必须套在一个 Fill() 的容器里。
// 右边这一栏是 Split 的 pane（本质上是一个 Box），如果直接把页面元素
// 挂上去，页面没有得到确定的高度约束：内部 Scroll.Fill() 拿到的
// 「父内容高度」是不确定的，结果 Scroll 被撑成内容高度而不会滚动，
// 超出视口的部分（比如系统页最后一张卡片）就永远看不到。
func (a *App) content(c *ui.Context) {
	if a.wslcErr != "" {
		a.errorPane(c, locales.ErrorWslcUnavailable(), a.wslcErr)
		return
	}

	ui.Column(c).Fill().Children(func() {
		switch a.page {
		case PageContainers:
			a.containersPage(c)
		case PageImages:
			a.imagesPage(c)
		case PageVolumes:
			a.volumesPage(c)
		case PageNetworks:
			a.networksPage(c)
		case PageCompose:
			a.composePage(c)
		case PageSystem:
			a.systemPage(c)
		}
	})
}

// errorPane 显示一个居中的错误提示。
func (a *App) errorPane(c *ui.Context, title, detail string) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(10).Padding(32).Children(func() {
		ui.Text(c, title).FontSize(16).Bold().TextColor(t.Danger)
		ui.Text(c, detail).TextColor(t.TextMuted).MaxWidth(560).TextAlign(ui.Center)
		if ui.Button(c, locales.ErrorRecheck()).Clicked() {
			a.Init()
			a.Refresh()
		}
	})
}

// statusBar 是底部状态栏。
func (a *App) statusBar(c *ui.Context) {
	t := c.Theme()

	ui.Row(c).Padding(6, 12).Gap(12).AlignItems(ui.Center).Children(func() {
		if a.loading {
			ui.Spinner(c).Size(12, 12).Label(locales.CommonLoading())
			ui.Text(c, locales.StatusLoading()).FontSize(11).TextColor(t.TextMuted)
		} else if a.lastRefresh.IsZero() {
			ui.Text(c, locales.StatusNotRefreshed()).FontSize(11).TextColor(t.TextMuted)
		} else {
			ui.Text(c, locales.StatusUpdatedAt(a.lastRefresh.Format("15:04:05"))).FontSize(11).TextColor(t.TextMuted)
		}

		if a.lastError != "" {
			ui.Text(c, "⚠ "+truncate(a.lastError, 120)).FontSize(11).TextColor(t.Danger).
				Tooltip(a.lastError)
		}

		ui.Spacer(c)

		ver := a.wslcVer
		if ver == "" {
			ver = "wslc"
		}
		ui.Text(c, ver).FontSize(11).TextColor(t.TextMuted)
		if a.composeVer != "" {
			ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, a.composeVer).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// truncate 按字符数截断文本。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
