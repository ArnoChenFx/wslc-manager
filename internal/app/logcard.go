package app

import (
	"math"
	"os"
	"strings"

	"github.com/arno/wslc-manager/locales"
	"github.com/egoist/mygo/ui"
)

// logCard 是系统信息页上的「运行日志」卡片。
//
// 它直接读日志环形缓冲，因此任何页面的操作都会在这里留下痕迹：
// 用户报“点了没反应”时，先看这里有没有对应的命令记录，
// 就能立刻区分「没触发」还是「触发了但失败」。
func (a *App) logCard(c *ui.Context) {
	t := c.Theme()

	a.infoCard(c, locales.LogsTitle(), func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.LogsHint()).
				FontSize(12).TextColor(t.TextMuted).Grow(1)

			if ui.Button(c, locales.MenuCopy()).Tooltip(locales.LogsCopy()).Clicked() {
				if a.logs != nil {
					c.WriteClipboard(a.logs.Text())
					a.toast(locales.LogsCopied())
				}
			}
			if ui.Button(c, locales.CommonClear()).Tooltip(locales.LogsClear()).Clicked() {
				if a.logs != nil {
					a.logs.Clear()
				}
			}
		})

		entries := a.logs.Tail(logViewLines)

		// 日志面板固定高度、可滚动、自动跟随末尾：新日志总是可见。
		sc := &a.logScroll
		if a.logFollow {
			sc.Y = math.MaxFloat32
		}

		ui.Box(c).Height(260).Radius(6).Background(t.Background).
			Border(1, t.Border).Padding(8).
			Children(func() {
				ui.Scroll(c).Fill().TrackScroll(sc).Children(func() {
					if len(entries) == 0 {
						ui.Text(c, locales.LogsEmpty()).FontSize(11).TextColor(t.TextMuted)
						return
					}
					for _, e := range entries {
						ui.Text(c, e.Line()).
							FontSize(11).
							Font("Consolas").
							TextColor(logLevelColor(t, e.Level)).
							SingleLine().
							Selectable()
					}
				})
			})
	})
}

// logViewLines 是日志卡片里最多显示的行数。
const logViewLines = 200

// logLevelColor 给不同级别配不同颜色，方便一眼扫到错误。
func logLevelColor(t *ui.Theme, l LogLevel) ui.Color {
	switch l {
	case LevelError:
		return t.Danger
	case LevelWarn:
		return t.Warning
	case LevelDebug:
		// 调试信息弱化显示，避免淹没关键行。
		c := t.TextMuted
		return c
	default:
		return t.Text
	}
}

// exportDiagnostic 在后台生成诊断报告并保存到文件。
func (a *App) exportDiagnostic() {
	opt := DefaultDiagOptions()
	opt.LogTail = 0

	a.logInfo("diag", "开始生成诊断报告")
	a.toast(locales.DiagGenerating())

	go func() {
		// 生成过程要读界面状态与 UI 树，统一在主线程执行以避免竞争。
		var path string
		var err error
		a.update(func() {
			path, err = a.SaveDiagnostic(opt, "")
		})

		a.update(func() {
			if err != nil {
				a.logError("diag", "生成失败: %v", err)
				a.toast(locales.LogsDiagGenerateFailed(firstLine(err.Error())))
				return
			}
			a.diagPath = path
			a.logInfo("diag", "报告已保存: %s", path)
			a.toast(locales.LogsDiagSavedTo(baseName(path)))
			a.showOutput(locales.LogsDiagPanel(baseName(path)), filePreview(path))
		})
	}()
}

// copyDiagnostic 生成报告并复制到剪贴板。
func (a *App) copyDiagnostic() {
	opt := DefaultDiagOptions()

	go func() {
		var text string
		var err error
		a.update(func() {
			text, err = a.BuildDiagnostic(opt)
		})

		a.update(func() {
			if err != nil {
				a.toast(locales.LogsGenerateFailed(firstLine(err.Error())))
				return
			}
			a.pendingClipboard = text
			a.logInfo("diag", "报告已复制到剪贴板（%d 字节）", len(text))
		})
	}()
}

// filePreview 读取文件开头用于在输出面板里预览。
func filePreview(path string) string {
	data, err := readFileLimited(path, 64*1024)
	if err != nil {
		return locales.LogsCannotReadFile(err.Error())
	}
	text := string(data)
	if strings.Contains(text, "\n") {
		return text
	}
	return text
}

// readFileLimited 读取文件的前 n 个字节。
func readFileLimited(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := f.Read(buf)
	if err != nil && read == 0 {
		return nil, err
	}
	return buf[:read], nil
}
