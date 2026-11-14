package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/locales"
)

// 本文件提供「开发期自截图」能力：按 Ctrl+Shift+S 把当前窗口内容
// 用 MyGo 的离屏渲染写成 PNG。
//
// 为什么需要它：用系统截图会受其它窗口遮挡、DPI 缩放影响，
// 而 CapturePage 由引擎直接渲染，结果与真实画面一致且可重复。
// 这只在开发期使用，不影响正常运行（不按键就什么都不做）。

// captureSeq 是截图文件序号。
var captureSeq int

// captureDir 是截图输出目录，可用环境变量 WSLC_MANAGER_SHOT_DIR 覆盖。
func captureDir() string {
	if d := os.Getenv("WSLC_MANAGER_SHOT_DIR"); d != "" {
		return d
	}
	return "."
}

// handleCapture 处理截图快捷键：按下时保存当前帧为 PNG。
//
// 除了快捷键，如果启动时带了 --shot 参数，也会在首帧后自动截一张，
// 便于自动化检查界面（不必手动按键、不受其它窗口遮挡）。
func (a *App) handleCapture(c *ui.Context) {
	trigger := c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyS)
	if a.autoShot != "" {
		trigger = true
	}
	if !trigger {
		return
	}
	if a.win == nil {
		return
	}

	png, err := a.win.CapturePage()
	if err != nil {
		a.toast(locales.ShotFailed(err.Error()))
		return
	}

	dir := captureDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.toast(locales.ShotMkdirFailed(err.Error()))
		return
	}
	// 自动截图使用指定文件名（可多次覆盖），快捷键则顺序编号。
	name := a.autoShot
	if name == "" {
		captureSeq++
		name = fmt.Sprintf("shot-%02d.png", captureSeq)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, png, 0o644); err != nil {
		a.toast(locales.ShotWriteFailed(err.Error()))
		return
	}
	if a.autoShot == "" {
		a.toast(locales.ShotSaved(name))
	}
	// 自动截图只做一次，否则每一帧都会重写文件并触发重绘。
	a.autoShot = ""
	_ = time.Now
}

// ShootPage 让应用在启动后直接切到某页，便于截图检查（调试用）。
func (a *App) ShootPage(page Page) {
	if page >= PageContainers && page <= PageSystem {
		a.page = page
	}
}

// OpenSettingsForShot 打开设置对话框（调试截图用）。
func (a *App) OpenSettingsForShot() { a.settingsOpen = true }

// SetAutoShot 开启自动截图：下一次视图构建时把窗口写入 name。
func (a *App) SetAutoShot(name string) { a.autoShot = name }

// ScheduleAutoShot 请求在 delay 之后截一张图。
//
// 启窗口的首帧通常还没有后台数据（列表为空），因此调试时用延迟截图
// 才能看到真实内容的布局。delay 为 0 则下一帧就截。
func (a *App) ScheduleAutoShot(name string, delay time.Duration) {
	if name == "" {
		return
	}
	if delay <= 0 {
		a.SetAutoShot(name)
		return
	}
	go func() {
		time.Sleep(delay)
		a.update(func() { a.autoShot = name })
	}()
}
