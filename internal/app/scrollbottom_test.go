package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// outputScrollViewport 返回日志面板滚动区的可视矩形。
//
// 容器页只有日志面板这一个滚动区（表格是 Table，不是 Scroll），
// 所以直接按角色取即可；以后页面里再出现别的 Scroll，这里要改成按位置区分。
func outputScrollViewport(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleScroll {
			return info.Bounds
		}
	}
	t.Fatal("找不到日志面板的滚动区")
	return ui.Rect{}
}

// numberedLines 生成 n 行「line i」，行与行之间用换行分隔，末尾没有换行。
func numberedLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return strings.Join(lines, "\n")
}

// TestOutputScrollReachesLastLine 验证滚到底时最后一行完整可见。
//
// 回归测试：日志面板曾经滚到底也看不到最后几行
// （用户截图里 "Status": "running" 只露出一半）。
//
// 根因在最外层的左右分栏，不在日志文本本身：分栏用 Fill() 时，
// 这一栏按「整个窗口高度」计算，把上面的工具栏和下面的状态栏也算了进去，
// 可用高度被高估，日志面板的滚动区于是伸到了窗口底边之外——
// 内容并没有算错，只是滚动区的下半截在窗口外面，怎么滚都看不到。
// 改成 Grow(1) 只取剩余高度后即恢复正常。
//
// 早先误判成「整段文本的高度被字体度量算小了」，于是把日志拆成逐行 Text、
// 再在末尾垫三行留白去凑，结果文字选区被切成一行一块，批量复制失效。
// 所以这里直接断言真正出问题的量，并且不关心日志是几个 Text：
//
//  1. 滚动区底边不超出窗口；
//  2. 内容确实滚到了尽头（否则下面的量没有意义）；
//  3. 最后一行的底边落在可视区内，并且还留着底部内边距。
//
// 把窗口高度和上下分栏比例都过一遍，避免只在某个尺寸下碰巧通过。
func TestOutputScrollReachesLastLine(t *testing.T) {
	const W = 1180
	for _, h := range []int{560, 760, 900} {
		for _, pct := range []float32{30, 50, 70} {
			a := newTestApp(t)
			sampleData(a)
			a.page = PageContainers
			a.outputOpen = true
			a.outputTitle = "日志 · ocd"
			a.outputPercent = pct
			// 行数要多到一定撑出滚动条，否则「滚到底」没有意义。
			a.outputText = numberedLines(40) + "\nZZZ-LAST"

			tt := ui.NewTester(a.View, W, h)
			tt.Frame()

			// 在滚动区中间滚轮向下滚到尽头，和用户的操作一致。
			vp := outputScrollViewport(t, tt)
			tt.Scroll(vp.X+vp.W/2, vp.Y+vp.H/2, 0, 100000)
			tt.Frame()

			vp = outputScrollViewport(t, tt)
			where := fmt.Sprintf("h=%d pct=%.0f", h, pct)

			// 1. 滚动区自己不能伸到窗口外面（真正的根因）。
			if vp.Y+vp.H > float32(h) {
				t.Errorf("%s: 滚动区底边 %.1f 超出窗口 %d", where, vp.Y+vp.H, h)
			}

			// 2. 内容要真的滚到了尽头。
			if a.outputScroll.MaxY <= 0 || a.outputScroll.Y < a.outputScroll.MaxY {
				t.Errorf("%s: 没有滚到底 Y=%.1f MaxY=%.1f", where, a.outputScroll.Y, a.outputScroll.MaxY)
			}

			// 3. 最后一行完整可见：底边在可视区内，且底部内边距（8）没有被吃掉。
			//    Find 给的是可见部分的外框，若内容还有一截没滚出来，
			//    底边会被裁在视口底边上，间隙就变成 0。
			//
			// 日志可能是整段一个 Text，也可能被拆成一行一个 Text：
			// 先按整段找，找不到再按最后一行找，两种结构下量到的都是最后一行的底边。
			r, ok := tt.Find(a.outputText)
			if !ok {
				r, ok = tt.Find("ZZZ-LAST")
			}
			if !ok {
				t.Errorf("%s: 找不到日志文本", where)
				continue
			}
			if r.H <= 0 {
				t.Errorf("%s: 日志文本高度为 %.1f", where, r.H)
			}
			const bottomPadding = 8
			if gap := (vp.Y + vp.H) - (r.Y + r.H); gap < bottomPadding-1 {
				t.Errorf("%s: 最后一行离视口底边只有 %.1f，应留出约 %d 的底部内边距（内容被裁）",
					where, gap, bottomPadding)
			}
			t.Logf("%s: 视口底=%.1f 文字底=%.1f MaxY=%.0f", where, vp.Y+vp.H, r.Y+r.H, a.outputScroll.MaxY)
		}
	}
}
