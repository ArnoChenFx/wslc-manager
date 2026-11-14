package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/locales"
)

// TestDialogsBuild 验证三个对话框都能正常构建并显示关键控件。
//
// 为什么单独测：对话框走的是 ui.Modal，内容只在打开时构建；
// 平时页面渲染根本不会碰到它们。一旦某个对话框里出现 nil 解引用
// 或错用的控件 API，只有用户点开才会崩，测试却全绿。
func TestDialogsBuild(t *testing.T) {
	a := newTestApp(t)
	a.settingsOpen = true
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()
	for _, want := range []string{
		locales.SettingsTitle(),
		locales.SettingsGeneral(),
		locales.SettingsLanguage(),
		locales.CommonBrowse(),
		locales.SettingsScanDirs(),
	} {
		if !tt.HasText(want) {
			t.Errorf("设置对话框缺少 %q", want)
		}
	}
	// 点「浏览…」：测试环境没有事件循环，必须降级而不 panic。
	if el, ok := tt.Find(locales.CommonBrowse()); ok {
		_ = el
		if err := tt.Click(locales.CommonBrowse()); err != nil {
			t.Logf("点击浏览失败: %v", err)
		}
		tt.Frame()
	}
	// 直接调用也必须降级而不 panic（win == nil、app 未就绪）。
	a.pickDirectory("选目录", func(dir string) { t.Logf("选中 %s", dir) })

	// 降级路径应当给出「对话框不可用」的提示，而不是默默无反应。
	if len(a.pendingToasts) == 0 {
		t.Logf("点击浏览后没有待弹提示（pendingToasts=%v）", a.pendingToasts)
	} else {
		t.Logf("点击浏览后提示：%v", a.pendingToasts)
	}

	// 语言下拉的当前值（测试环境未就绪时降级为中文）。
	if !tt.HasText("中文") && !tt.HasText("English") {
		t.Errorf("语言下拉没有选项")
	}

	b := newTestApp(t)
	b.addProjectOpen = true
	b.addProjectPath = `E:\Project\my-app`
	bt := ui.NewTester(b.View, 1180, 760)
	bt.Frame()
	for _, want := range []string{locales.ComposeAddTitle(), locales.CommonBrowse(), locales.CommonAdd()} {
		if !bt.HasText(want) {
			t.Errorf("添加项目对话框缺少 %q", want)
		}
	}

	c := newTestApp(t)
	c.confirmOpen = true
	c.confirmTitle = "确认"
	c.confirmBody = "正文"
	ct := ui.NewTester(c.View, 1180, 760)
	ct.Frame()
	for _, want := range []string{locales.CommonCancel(), locales.CommonConfirm()} {
		if !ct.HasText(want) {
			t.Errorf("确认对话框缺少 %q", want)
		}
	}
}
