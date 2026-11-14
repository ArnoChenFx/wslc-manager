package app

// 这个文件放两个与「设置」相关的交互辅助：
//
//   - languageSelect：界面语言切换。用 MyGo 的 App.SetLocale，
//     它自己会持久化用户选择，所以这里不写 settings 文件。
//   - pickDirectory：调用系统原生目录选择框。它是阻塞的，
//     所以必须放到后台 goroutine，选完再回主线程写状态。

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/locales"
)

// 语言标签用 BCP 47，和 locales 词表的文件名一致。
const (
	langZhHans = "zh-Hans"
	langEn     = "en"
)

// languageSelect 渲染语言下拉框。
//
// 当前语言从 mygo.App.Locale() 读，而不是从 settings 读：
// MyGo 才是语言的唯一来源（它会记住选择、也支持 MYGO_LOCALE 覆盖）。
func (a *App) languageSelect(c *ui.Context) {
	// 选项文本用各语言自己的写法，这样任何语言下都能认出自己的语言。
	options := []string{"中文", "English"}
	cur := options[0]
	if currentLocale() == langEn {
		cur = options[1]
	}

	sel := cur
	if ui.Select(c, &sel, options).Changed() && sel != cur {
		tag := optionsToTag(sel)
		setLocale(sel)
		// 同时写进设置：MyGo 的存储位置随平台不同，
		// 设置文件才是这个应用自己的那一份。
		a.settings.Language = tag
		a.saveSettings()
	}
}

// optionsToTag 把界面上的选项文本映射成 BCP 47 标签。
func optionsToTag(label string) string {
	if label == "English" {
		return langEn
	}
	return langZhHans
}

// currentLocale 返回当前界面语言标签。
//
// 没有运行中的 app（测试环境）时退回中文——这也正是词的源语言。
func currentLocale() string {
	if !mygo.App.IsReady() {
		return langZhHans
	}
	if l := mygo.App.Locale(); l != "" {
		return l
	}
	return langZhHans
}

// setLocale 切换界面语言。
//
// setLocale 必须在主线程调用（SetLocale 内部会排队到主线程）；
// 视图构建期就在主线程，所以直接调用即可。
func setLocale(label string) {
	if !mygo.App.IsReady() {
		// 测试环境没有事件循环，切换语言无意义，静默跳过。
		return
	}
	mygo.App.SetLocale(optionsToTag(label))
}

// pickDirectory 弹出系统目录选择框，选中后把路径交给 onPick。
//
// 两个关键点：
//   - Dialog.Open 是阻塞的，必须放后台，否则界面会卡住；
//   - 它要求 app 已就绪（内部调用 needsApp），测试环境或没有窗口时
//     直接跳过，避免 panic。
func (a *App) pickDirectory(title string, onPick func(string)) {
	if !mygo.App.IsReady() {
		a.toast(locales.ErrorDialogUnavailable())
		return
	}
	if onPick == nil {
		return
	}

	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:     title,
			Directory: true,
			Parent:    a.win,
		})
		if err != nil || len(paths) == 0 {
			// 用户取消时不要弹错误——那会把「取消」变成一次报错。
			return
		}
		dir := paths[0]
		a.update(func() { onPick(dir) })
	}()
}
