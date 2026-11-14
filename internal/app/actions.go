package app

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
)

// iconAction 绘制一个「图标 + 提示」的紧凑操作按钮。
//
// enabled 为 false 时按钮禁用（视觉上变灰且不可点击）。
func (a *App) iconAction(c *ui.Context, iconName, label, tooltip string, enabled bool, fn func()) {
	btn := ui.Button(c, "").Label(label).Tooltip(tooltip).Disabled(!enabled)
	btn.Children(func() { ui.Icon(c, icon(iconName)).FontSize(15) })
	if btn.Clicked() && enabled {
		fn()
	}
}

// longContext 返回一个 5 分钟超时的上下文，用于可能较慢的只读查询。
func longContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Minute)
}

// emptyState 绘制统一的空列表提示。
//
// 布局要点：用 Grow(1)（而不是 Fill）占满父级剩余空间，并在其中居中。
// 早先的写法是「表格 Grow(1) + 空状态 Fill()」同时存在，两者都要求
// 全尺寸，导致文字与表头重叠、视觉上没对齐。
func (a *App) emptyState(c *ui.Context, title, hint string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(6).Padding(24).Children(func() {
		ui.Text(c, title).FontSize(14).TextColor(t.TextMuted)
		if hint != "" {
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
		}
	})
}
