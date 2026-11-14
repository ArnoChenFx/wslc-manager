package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// Compose 详情分栏的回归测试。
//
// 两个曾经出过的问题：
//  1. 分隔条是静态 Divider，用户无法调整两栏比例；
//  2. 选项栏被压太窄，描述文字逐词换行（"Equival / ent to / up -d"）。
func TestComposeSplitAndOptions(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.projects = []compose.Project{{
		Dir:  `E:\Project\wslc\open-compute`,
		Name: "open-compute",
		Config: compose.ProjectConfig{
			Name: "open-compute", Directory: `E:\Project\wslc\open-compute`,
			ComposeFiles: []string{`E:\Project\wslc\open-compute\docker-compose.yml`},
			Services: map[string]compose.ServiceConfig{
				"ocd": {Name: "ocd", Image: "open-compute/ocd:v0.4.0"},
			},
		},
	}}
	a.projectSel = 0
	a.page = PageCompose

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	// 分隔条必须存在且可聚焦（Split 的分隔条 role 是 splitter）。
	found := false
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleSplitter {
			found = true
			break
		}
	}
	if !found {
		t.Error("没有找到可拖动的分隔条（Split），仍是静态 Divider")
	}

	// 选项栏的描述必须在窗口内，且宽度足够容纳一句英文
	for _, want := range []string{
		locales.ComposeStartOptions(),
		locales.ComposeUpTooltip(),
	} {
		r, ok := tt.Find(want)
		if !ok {
			t.Errorf("找不到 %q", want)
			continue
		}
		if r.X+r.W > 1180 {
			t.Errorf("%q 超出窗口：右边界 %.0f", want, r.X+r.W)
		}
		if r.W < 100 {
			t.Errorf("%q 太窄（%.0f），会逐词换行", want, r.W)
		}
	}
}
