package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestScrollbarInsideWindow 验证滚动区（及其滚动条）不会跑到窗口外。
//
// 回归测试：日志面板打开时，滚动条被画到窗口之外——用户截图里
// 滚动条整条都在可视区下方。
//
// 根因不在日志面板，而在最外层的左右分栏：
// 它用的是 Fill() 而不是 Grow(1)，于是这一栏按「整个窗口高度」
// 计算，把上面工具栏和下面状态栏也算进去了。里面再套上下分栏时
// 可用高度被高估 82px，下半栏被排到窗口外。
//
// 这里把各种上下比例都过一遍，断言滚动区底部始终不超出窗口。
// （此前这里只打日志、不判失败，外层分栏退回 Fill() 时测试照样通过，
// 等于没有守住；现在越界就报错。）
func TestScrollbarInsideWindow(t *testing.T) {
	for _, pct := range []float32{30, 50, 58, 70, 80, 90, 99} {
		a := newTestApp(t)
		sampleData(a)
		a.page = PageContainers
		a.outputOpen = true
		a.outputTitle = "日志"
		a.outputPercent = pct
		a.outputText = ""
		for i := 0; i < 60; i++ {
			a.outputText += "line\n"
		}

		const H = 760
		tt := ui.NewTester(a.View, 1180, H)
		tt.Frame()

		bottom := float32(0)
		for _, info := range tt.AccessAll() {
			if info.Role == ui.RoleScroll {
				bottom = info.Bounds.Y + info.Bounds.H
			}
		}
		if bottom > H {
			t.Errorf("pct=%.0f: 滚动区底部 %.0f 超出窗口高度 %d", pct, bottom, H)
		}
	}
}
