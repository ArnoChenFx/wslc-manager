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

	// Compose 页有两条需要能拖的分隔条：
	//   1. 项目列表 / 项目详情
	//   2. 服务表 / 启动选项
	// 两条都曾经是静态 ui.Divider（用 Row + Divider 拼的），
	// 用户完全调不了比例。这里逐条断言存在，防止又改回静态线。
	var splitters []ui.Rect
	for _, info := range tt.AccessAll() {
		if info.Role == ui.RoleSplitter {
			splitters = append(splitters, info.Bounds)
		}
	}
	// 侧边栏那条也算在内，所以至少是 3 条。
	if len(splitters) < 3 {
		t.Errorf("可拖动分隔条只有 %d 条，期望至少 3 条"+
			"（侧边栏、项目列表/详情、服务表/选项）", len(splitters))
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
