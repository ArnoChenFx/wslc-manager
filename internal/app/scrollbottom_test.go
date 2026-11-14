package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestOutputScrollReachesLastLine 验证滚到底时最后一行完整可见。
//
// 回归测试：日志面板曾经滚到底也看不到最后几行
// （用户截图里 "Status": "running" 只露出一半）。
//
// 查下来是内容高度算得比实际小：
//   - 整段文本塞进一个 Text 时，高度由字体度量算出，比「行数 × 行高」小；
//   - 改成逐行渲染 + FixedLineHeight 后仍有约 46 px 的偏差，
//     Scroll 的滚动上限始终比「内容高 − 可视高」少一截。
//
// 现在末尾留三行缓冲，让「滚到底」时最后一行还能完整停在可视区内。
//
// 断言用「高度不为 0 且底部不超出窗口」，这两个条件正是当初被破坏的。
func TestOutputScrollReachesLastLine(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers
	a.outputOpen = true
	a.outputTitle = "日志 · ocd"
	a.outputPercent = 50
	a.outputText = ""
	for i := 0; i < 40; i++ {
		a.outputText += "line\n"
	}
	a.outputText += "ZZZ-LAST"

	const W, H = 1180, 760
	tt := ui.NewTester(a.View, W, H)
	tt.Frame()

	tt.Scroll(float32(W)/2, 600, 0, 100000)
	tt.Frame()

	t.Logf("MaxY=%.0f scrollY=%.0f", a.outputScroll.MaxY, a.outputScroll.Y)
	r, ok := tt.Find("ZZZ-LAST")
	if !ok {
		t.Fatal("找不到最后一行")
	}
	t.Logf("最后一行 y=%.0f h=%.0f 底部=%.0f", r.Y, r.H, r.Y+r.H)
	if r.Y+r.H > H {
		t.Errorf("仍然超出 %.0f px", r.Y+r.H-float32(H))
	}
}
