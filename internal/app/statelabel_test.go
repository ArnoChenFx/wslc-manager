package app

import (
	"regexp"
	"testing"

	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/locales"
)

// 回归测试：早先容器状态文案写死在 wslc 包里，只有中文——
// 英文界面的「状态」列也显示「运行中」「已停止」。这个缺陷是渲染英文版宣传片时才露出来的。
//
// 这里直接比较两种语言的结果（stateLabelIn 带语言参数），不改全局语言，
// 因此不会和别的测试互相影响。
func TestStateLabelFollowsLanguage(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		health string
		zh, en string
	}{
		{name: "running", state: "running", zh: "运行中", en: "Running"},
		{name: "running healthy", state: "running", health: "healthy", zh: "运行中（健康）", en: "Running (healthy)"},
		{name: "running unhealthy", state: "running", health: "unhealthy", zh: "运行中（不健康）", en: "Running (unhealthy)"},
		{name: "exited", state: "exited", zh: "已停止", en: "Stopped"},
		{name: "created", state: "created", zh: "已创建", en: "Created"},
		{name: "paused", state: "paused", zh: "已暂停", en: "Paused"},
		{name: "restarting", state: "restarting", zh: "重启中", en: "Restarting"},
		{name: "empty state", state: "", zh: "未知", en: "Unknown"},
		// wslc 以后可能出现这里不认识的状态：原样显示，比编一个翻译诚实。
		{name: "unknown state passes through", state: "weird", zh: "weird", en: "weird"},
		// 大小写不敏感：wslc 不同版本对状态的大小写不一致。
		{name: "upper case", state: "RUNNING", zh: "运行中", en: "Running"},
	}

	han := regexp.MustCompile(`\p{Han}`)
	zh, en := locales.In("zh-Hans"), locales.In("en")
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ct := wslc.Container{State: tc.state, Health: tc.health}
			if got := stateLabelIn(zh, ct); got != tc.zh {
				t.Errorf("中文 = %q, want %q", got, tc.zh)
			}
			got := stateLabelIn(en, ct)
			if got != tc.en {
				t.Errorf("英文 = %q, want %q", got, tc.en)
			}
			if han.MatchString(got) {
				t.Errorf("英文状态文案里混入了汉字: %q", got)
			}
		})
	}
}
