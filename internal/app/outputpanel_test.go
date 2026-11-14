package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestOutputPanelAlwaysInside 验证日志面板在任何情况下都不越界。
//
// 回归测试：分隔条位置曾经按像素存（outputHeight）。
// 窗口变矮、或用户把分隔条拖到底时，这个像素值会超过可用高度，
// 于是整个日志面板被推到窗口之外——滚动条也跟着滚到看不见的区域，
// 用户看到的是一大片被裁掉的空白（截图确认过）。
//
// 改用百分比后天然不会越界；这里把窗口高度和百分比的组合都过一遍，
// 包括 100% 这种极端值。
func TestOutputPanelAlwaysInside(t *testing.T) {
	for _, h := range []int{560, 620, 760, 900} {
		for _, pct := range []float32{0, 50, 92, 99, 100} {
			a := newTestApp(t)
			sampleData(a)
			a.page = PageContainers
			a.outputOpen = true
			a.outputTitle = "日志 · ocd"
			a.outputText = "x\n"
			a.outputPercent = pct

			tt := ui.NewTester(a.View, 1180, h)
			tt.Frame()

			// 面板标题必须可见且不超出窗口
			r, ok := tt.Find("日志 · ocd")
			if !ok {
				t.Errorf("h=%d pct=%.0f: 找不到面板标题", h, pct)
				continue
			}
			if r.Y < 0 || r.Y+r.H > float32(h) {
				t.Errorf("h=%d pct=%.0f: 面板标题越界 y=%.0f h=%.0f",
					h, pct, r.Y, r.H)
			}
		}
	}
}
