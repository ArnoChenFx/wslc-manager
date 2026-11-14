package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
)

// TestLeftSplitterDrags 验证「项目列表 / 项目详情」之间的分隔条能拖动。
//
// 回归测试：这条分隔条曾经是静态 ui.Divider，用户完全无法调整比例，
// 项目列表宽度被写死 280。改用 ui.Split 后这里用真实的按下-移动-释放
// 事件确认宽度真的会变，而不只是「存在一个 splitter 元素」。
func TestLeftSplitterDrags(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.projects = []compose.Project{{
		Dir: `E:\P`, Name: "proj",
		Config: compose.ProjectConfig{
			Name: "proj", Directory: `E:\P`,
			Services: map[string]compose.ServiceConfig{"ocd": {Name: "ocd"}},
		},
	}}
	a.projectSel = 0
	a.page = PageCompose

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	before := a.composeProjectsWidth

	// 找 x 在 400~600 之间那条：项目列表与详情之间的分隔条。
	var target ui.Rect
	found := false
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleSplitter && info.Bounds.X > 400 && info.Bounds.X < 600 {
			target, found = info.Bounds, true
			break
		}
	}
	if !found {
		t.Fatal("没有找到项目列表与详情之间的分隔条")
	}

	cx := target.X + 1
	cy := target.Y + target.H/2
	tt.Press(cx, cy)
	tt.Move(cx+120, cy)
	tt.Release(cx+120, cy)
	tt.Frame()

	t.Logf("拖动前 %.0f，拖动后 %.0f", before, a.composeProjectsWidth)
	if a.composeProjectsWidth == before {
		t.Errorf("拖动分隔条后宽度未变化（仍为 %.0f），不可拖动", before)
	}
}
