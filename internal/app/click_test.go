package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRowClickStillSelects 验证点击行内文字能选中整行。
//
// 回归测试：为了让 CellsStyle（选中行配色）生效，表格必须开启
// CellSelect，这会把选择模型从「整行」变成「单元格块」。
// 一旦这里出问题，用户会发现点文字选不中行——之前就踩过类似的坑
// （在单元格里放 Selectable，结果文字抢走了点击）。
func TestRowClickStillSelects(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers

	tt := ui.NewTester(a.View, 1180, 400)
	tt.Frame()
	if a.containerSel != -1 {
		t.Fatalf("初始应无选中，实际 %d", a.containerSel)
	}

	// 点第一行的文字
	tt.Click("demo-web-1")
	tt.Frame()
	if a.containerSel != 0 {
		t.Errorf("点击第一行后应选中 0，实际 %d", a.containerSel)
	}

	// 点第二行
	tt.Click("legacy")
	tt.Frame()
	if a.containerSel != 1 {
		t.Errorf("点击第二行后应选中 1，实际 %d", a.containerSel)
	}
}
