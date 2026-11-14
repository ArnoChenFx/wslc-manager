package app

// 项目操作按钮的可用性测试。
//
// 为什么需要：这些按钮之前只看「有没有动作正在跑」，
// 完全不看项目实际状态，于是出现「已经全部运行，启动按钮还能点」
// 这种自相矛盾的界面。用户点了要么无事发生，要么报错，
// 从界面上看不出按钮能不能用。
//
// 这里直接断言 Disabled 状态，而不是靠肉眼看截图。

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/locales"
)

// projectWith 构造一个测试用项目，并放入给定状态的容器。
func projectWith(t *testing.T, a *App, states ...string) compose.Project {
	t.Helper()
	p := compose.Project{
		Dir:  `E:\Project\demo`,
		Name: "demo",
		Config: compose.ProjectConfig{
			Name:      "demo",
			Directory: `E:\Project\demo`,
			Services:  map[string]compose.ServiceConfig{},
		},
	}
	a.projects = []compose.Project{p}
	a.projectSel = 0

	a.containers = nil
	for i, st := range states {
		a.containers = append(a.containers, wslc.Container{
			ID:     "c" + string(rune('1'+i)),
			Names:  "demo-" + string(rune('a'+i)) + "-1",
			State:  st,
			Labels: "com.wslc-compose.project=demo",
		})
	}
	return p
}

// buttonDisabled 查某个按钮当前是否被禁用。
//
// Tester 的可访问性树会带上 disabled 状态，用它比读内部字段更真实。
func buttonDisabled(t *testing.T, tt *ui.Tester, label string) bool {
	t.Helper()
	for _, info := range tt.AccessAll() {
		// Name 是按钮的无障碍名称，也就是它的 Label。
		if info.Name == label {
			return info.Disabled
		}
	}
	t.Fatalf("找不到按钮 %q", label)
	return false
}

// TestProjectButtonsFollowState 覆盖三种典型状态。
func TestProjectButtonsFollowState(t *testing.T) {
	up := locales.ComposeStart()
	stop := locales.ComposeStop()
	restart := locales.ComposeRestart()

	cases := []struct {
		name    string
		states  []string
		wantUp  bool // true = 禁用
		wantStp bool
		wantRst bool
	}{
		{
			name:   "全部运行中",
			states: []string{"running", "running"},
			// 已全部运行：启动无意义；停止/重启可用。
			wantUp: true, wantStp: false, wantRst: false,
		},
		{
			name:   "全部已停止",
			states: []string{"exited", "exited"},
			// 没在跑：停止/重启无意义；启动可用。
			wantUp: false, wantStp: true, wantRst: true,
		},
		{
			name:   "部分运行",
			states: []string{"running", "exited"},
			// 混合状态：三个动作都合理。
			wantUp: false, wantStp: false, wantRst: false,
		},
		{
			name:   "没有容器",
			states: nil,
			// 还没创建：只能启动。
			wantUp: false, wantStp: true, wantRst: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp(t)
			sampleData(a)
			projectWith(t, a, c.states...)
			a.page = PageCompose

			tt := ui.NewTester(a.View, 1280, 800)
			tt.Frame()

			if got := buttonDisabled(t, tt, up); got != c.wantUp {
				t.Errorf("启动按钮 disabled=%v，期望 %v", got, c.wantUp)
			}
			if got := buttonDisabled(t, tt, stop); got != c.wantStp {
				t.Errorf("停止按钮 disabled=%v，期望 %v", got, c.wantStp)
			}
			if got := buttonDisabled(t, tt, restart); got != c.wantRst {
				t.Errorf("重启按钮 disabled=%v，期望 %v", got, c.wantRst)
			}
		})
	}
}

// TestComposeHeaderFitsWindow 确保默认窗口宽度下表头按钮全部可见。
//
// 回归测试：项目表头曾经把「名称 + 路径 + 状态 + 启动/停止/重启/移除/更多」
// 排成一行，需求约 750，而详情区在 1180 窗口下只有约 678。
// 结果按钮被推到窗口之外：「启动」只剩 11~23 像素，后两个宽度为 0，
// 整个操作区看不见（用户截图确认过）。
//
// 这里直接量按钮的右边界，比肉眼看截图可靠。
func TestComposeHeaderFitsWindow(t *testing.T) {
	const W = 1180 // 默认窗口宽度

	a := newTestApp(t)
	sampleData(a)
	sampleData(a)
	a.projects = []compose.Project{{
		Dir:  `E:\Project\wslc\open-compute`,
		Name: "open-compute",
		Config: compose.ProjectConfig{
			Name:      "open-compute",
			Directory: `E:\Project\wslc\open-compute`,
			ComposeFiles: []string{
				`E:\Project\wslc\open-compute\docker-compose.yml`,
			},
			Services: map[string]compose.ServiceConfig{
				"ocd": {Name: "ocd", Image: "open-compute/ocd:v0.4.0"},
			},
		},
	}}
	a.projectSel = 0
	a.page = PageCompose

	tt := ui.NewTester(a.View, W, 760)
	tt.Frame()

	for _, label := range []string{
		locales.ComposeStart(), locales.ComposeStop(),
		locales.ComposeRestart(), locales.ComposeDown(),
		locales.CommonMore(),
	} {
		r, ok := tt.Find(label)
		if !ok {
			t.Errorf("找不到按钮 %q", label)
			continue
		}
		if r.W < 20 {
			t.Errorf("%q 宽度只有 %.0f，被压扁了", label, r.W)
		}
		if r.X+r.W > W {
			t.Errorf("%q 右边界 %.0f 超出窗口宽度 %d", label, r.X+r.W, W)
		}
	}

	// 「启动选项」面板也必须在窗口内，之前它整个被推到 x=1225。
	if r, ok := tt.Find(locales.ComposeStartOptions()); ok {
		if r.X > W {
			t.Errorf("启动选项在窗口之外：x=%.0f > %d", r.X, W)
		}
	}
}
