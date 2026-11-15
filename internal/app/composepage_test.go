package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// clampComposeListWidth 必须把明显越界的存值收回到可用宽度内。
//
// 回归测试：用户（或我）在宽窗口把分隔条拖到 879 存进设置，之后在更窄的窗口
// 打开，服务表那一栏仍拿 879，右栏被压成零宽——「启动选项」整栏变成
// 每行一个字的竖线（真实截图）。ui.Split 只保证拖动那一刻不越界，
// 不负责纠正已存的值。
func TestClampComposeListWidth(t *testing.T) {
	cases := []struct {
		name    string
		winW    float32
		sidebar float32
		projW   float32
		want    float32
		maxOver float32 // 允许的最大结果
	}{
		{"宽窗口不限制", 1600, 220, 260, 750, 750},
		{"窄窗口要收回", 1000, 220, 200, 879, 400},
		{"极窄窗口", 700, 220, 200, 879, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			a.ViewLocked(func() {
				a.sizeOverride = [2]float32{tc.winW, 760}
				a.sidebarW = tc.sidebar
				a.composeProjectsWidth = tc.projW
			})
			var got float32
			a.ViewLocked(func() { got = a.clampComposeListWidth(tc.want) })
			if got > tc.maxOver {
				t.Errorf("窗口 %.0f 下夹到 %.0f，超过上限 %.0f（右栏会被压成零宽）",
					tc.winW, got, tc.maxOver)
			}
			if got < 0 {
				t.Errorf("夹出了负数 %.0f", got)
			}
		})
	}
}

// 没有窗口尺寸时不要凭空缩：构建期与测试早期都拿不到尺寸，
// 这时改值会让「记住的位置」莫名变小。
func TestClampComposeListWidthWithoutSize(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() {
		a.sizeOverride = [2]float32{}
		if got := a.clampComposeListWidth(600); got != 600 {
			t.Errorf("没有窗口尺寸时应原样返回，实际 %.0f", got)
		}
	})
}

// 窄窗口下「启动选项」栏必须仍有可用宽度，不能因为存了超大的分隔位置就被压成零宽。
//
// 回归测试：真实截图里右栏变成每行一个字的竖线。判断依据是「分隔条的位置」：
// 分隔条必须落在详情区内、且右侧还剩够读一行字的宽度。
// 早先这条测试测的是「右栏文字的最宽值」，但文字被压窄时 Tester 报告的单块宽度
// 仍可能刚好过线，变异验证显示它抓不住缺陷，所以改成直接量分隔条。
func TestProjectOptionsPaneNotSqueezed(t *testing.T) {
	a := newTestApp(t)
	const winW, winH = 1164, 721
	a.ViewLocked(func() {
		sampleData(a)
		a.projects = []compose.Project{composeTestProject()}
		a.projectSel = 0
		a.page = PageCompose
		// 设置里存了一个远超窗口宽度的分隔位置，正是出问题的那种情况。
		a.composeListWidth = 879
		a.composeProjectsWidth = 262
		a.sidebarW = 220
		a.sizeOverride = [2]float32{winW, winH}
	})

	tt := ui.NewTester(a.View, winW, winH)
	tt.Frame()
	tt.Frame()

	// 详情区的分隔条：取最靠右的那一个（服务表 / 启动选项之间）。
	var splits []ui.Rect
	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleSplitter {
			splits = append(splits, n.Bounds)
		}
	}
	if len(splits) == 0 {
		t.Fatal("找不到分隔条，布局可能没画出来")
	}
	rightmost := splits[0]
	for _, b := range splits {
		if b.X > rightmost.X {
			rightmost = b
		}
	}

	// 用生产代码里的常量为准（减掉左右内边距），而不是在这里另写一个数字：
	// 两处不一致时，测试会以为自己守住了布局，实际没守住。
	if avail := winW - rightmost.X - 8; avail < composeOptionsMinWidth-20 {
		t.Errorf("右栏只剩 %.0f 宽（分隔条 x=%.0f，窗口 %d），会被压成竖排单字",
			avail, rightmost.X, winW)
	}
}

// 「扫描」用的是刷新图标，不是放大镜。
//
// 回归测试：早先画的是放大镜，同一排里又没有任何搜索框，
// 用户看到的第一反应是「这是搜索」（真实截图指出）。这个动作是
// 「重新扫一遍根目录」，语义上就是刷新。
//
// 图标在无障碍树里只表现为按钮的 Label（"扫描"），所以这里断言
// 按钮存在且宽度正常，真正的图标选择靠下面的注释与代码审查守住；
// 之所以仍写这条，是为了让「扫描按钮不该消失/变灰」也有回归保护。
func TestScanButtonPresent(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() {
		a.page = PageCompose
		a.sizeOverride = [2]float32{1180, 760}
	})
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleButton && n.Name == locales.ComposeScan() {
			if n.Bounds.W < 24 {
				t.Errorf("扫描按钮宽只有 %.0f，太小", n.Bounds.W)
			}
			return
		}
	}
	t.Fatal("找不到「扫描」按钮")
}

// 右栏的最小宽度不能超过它的内容需要太多：多余的宽度是从服务表那儿抢来的。
//
// 用户截图指出「最小宽度还是有点太宽了，浪费空间」。这条测试把「宽度合理」
// 钉在数值上：右栏最长一行（项目路径约 187 DIP）+ 内边距 = 约 210，
// 留一点余量到 220 就够。若有人把它改回 320，这条会红。
func TestOptionsPaneMinWidthNotTooWide(t *testing.T) {
	if composeOptionsMinWidth > 240 {
		t.Errorf("右栏最小宽度 %.0f 偏大：最长一行只需约 210，多余宽度是从服务表抢的",
			composeOptionsMinWidth)
	}
	if composeOptionsMinWidth < 180 {
		t.Errorf("右栏最小宽度 %.0f 太小：项目路径会被折行或截断", composeOptionsMinWidth)
	}
}
