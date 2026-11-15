// 本文件是「编辑 compose 项目的文件」界面：compose.yaml（含 override）与 .env。
//
// 设计取舍：
//
//   - 用 MyGo 自带的代码编辑器（ui/editor）而不是自己画文本域：它带 YAML 高亮、
//     行号、折叠、多光标与查找替换，compose 文件正是它擅长的东西。
//   - 每个文件一个 Buffer，切换标签只换显示的那个，草稿不丢。
//     Buffer 自己记撤销历史，所以来回切标签不会把「刚才的撤销」弄没。
//   - 保存不改动文件的换行符与权限（见 compose.WriteEditable），
//     保存成功后立刻刷新项目，让界面与服务表跟上文件的真实内容。
//   - 有未保存改动时关闭要二次确认：编辑器里可能就是用户刚敲的端口映射，
//     点一下叉号就丢掉太伤人。
package app

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// editorFile 是编辑器里打开的一个文件。
type editorFile struct {
	// file 是磁盘上这个文件的描述（路径、种类、是否缺失）。
	file compose.EditableFile
	// buffer 是编辑器的工作副本，带撤销历史。
	buffer *editor.Buffer
	// state 是编辑器自己的状态（光标、选区、滚动位置），跨帧保留。
	state *editor.State
	// loadErr 表示这个文件读不出来（权限、太大），此时只显示原因，不给编辑器。
	loadErr string
}

// editorDialog 绘制「编辑文件」对话框。
//
// 这里不做任何磁盘 IO：内容在打开对话框时由 openEditor 载入，
// 保存走 runAsync。视图层只负责画和收集用户意图。
func (a *App) editorDialog(c *ui.Context) {
	t := c.Theme()

	ui.Modal(c, &a.editorOpen, func() {
		// 快捷键必须写在 Modal 的子树里。
		//
		// ui.Context.Shortcut 会先判断「当前构建位置是不是在 modal 内」：
		// 它在 c.parent 链上找 modal 的 id，找不到就返回 false。
		// 早先把这段写在 Modal 调用之外，于是 insideModal() 为假，
		// Ctrl+S 被直接忽略——按了完全没反应（用户实测）。
		if !a.busyKey(editorBusyKey) && c.Shortcut(ui.Ctrl, ui.KeyS) {
			a.saveCurrentEditorFile()
		}

		ui.Column(c).Width(920).Gap(10).Children(func() {
			// ── 标题行 ────────────────────────────
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.ComposeEditorTitle()).FontSize(15).Bold()
				if a.editorProjectDir != "" {
					ui.Text(c, baseName(a.editorProjectDir)).FontSize(11).
						TextColor(t.TextMuted).SingleLine().Shrink(1).Selectable().
						Tooltip(a.editorProjectDir)
				}
			})

			if len(a.editorFiles) == 0 {
				ui.Text(c, locales.ComposeEditorNoFiles()).
					FontSize(12).TextColor(t.TextMuted)
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if ui.Button(c, locales.CommonClose()).Clicked() {
						a.closeEditor()
					}
				})
				return
			}

			// ── 文件标签 ──────────────────────────
			a.editorTabs(c)

			// ── 编辑器主体 ────────────────────────
			cur := a.currentEditorFile()
			if cur == nil {
				return
			}

			if cur.loadErr != "" {
				ui.Text(c, cur.loadErr).FontSize(12).TextColor(t.Danger).Selectable()
			} else if cur.state != nil {
				// 这里必须给一个**确定的高度**，不能靠 Grow(1)。
				//
				// Modal 的面板是按内容高度收缩的（coreModal → panel 没有 Grow），
				// 里面没有「剩余高度」可分，Grow(1) 得到的就是 0——
				// 实测编辑器 height=0，看上去就是对话框中间一片空白。
				//
				// 取 55% 窗口高度（限制在 240~620 之间）：小窗口下不至于把底部按钮
				// 挤出去，大窗口下也不会让编辑器高得荒谬。
				view := editor.View(c, cur.state).Height(a.editorHeight(c)).FillWidth()
				// 句柄要每帧都绑：切换标签后视图会重建，旧句柄不再对应当前元素。
				view = view.Bind(&a.editorViewH)
				if a.editorFocusPending {
					// 用 Handle.Focus 而不是 AutoFocus：AutoFocus 只在元素**首次入树**时生效，
					// 而切标签时编辑器本来就存在，元素只是换了内容，AutoFocus 不会重新抢焦点，
					// 结果敲键盘还落在标签按钮上（实测就是这样）。
					a.editorViewH.Focus(c)
					a.editorFocusPending = false
				}
			}

			// ── 底部：路径 + 动作 ─────────────────
			a.editorFooter(c, cur)
		})
	})
}

// editorBusyKey 是保存动作的忙碌标记。
const editorBusyKey = "editor-save"

// busyKey 报告某个动作是否正在执行。
//
// 包一层而不是直接调 isBusy：编辑器里多处要做「保存中就不要重复触发」的判断，
// 统一在这里免得各处记错锁的用法。
func (a *App) busyKey(key string) bool { return a.isBusy(key) }

// editorTabs 画文件标签，并处理切换。
//
// 有未保存改动的标签加一个圆点：用户切走再切回来时能看出哪个改过，
// 关闭时也知道会丢掉什么。
func (a *App) editorTabs(c *ui.Context) {
	labels := make([]string, 0, len(a.editorFiles))
	for i := range a.editorFiles {
		name := a.editorFiles[i].file.Name
		if a.editorFiles[i].buffer != nil && a.editorFiles[i].buffer.Modified() {
			name += " •"
		}
		if a.editorFiles[i].file.Missing {
			name += " ＋"
		}
		labels = append(labels, name)
	}

	before := a.editorTab
	ui.Tabs(c, &a.editorTab, labels...)
	if a.editorTab != before {
		// 切标签时把焦点交给新编辑器：否则键盘还在给旧编辑器，
		// 表现为「打了字却没反应」。
		if cur := a.currentEditorFile(); cur != nil && cur.state != nil {
			a.editorFocusPending = true
		}
	}
	if a.editorTab < 0 || a.editorTab >= len(a.editorFiles) {
		a.editorTab = 0
	}
}

// editorFooter 画底部的路径提示与按钮。
func (a *App) editorFooter(c *ui.Context, cur *editorFile) {
	t := c.Theme()

	modified := cur.buffer != nil && cur.buffer.Modified()

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, cur.file.Path).FontSize(11).TextColor(t.TextMuted).
			SingleLine().Shrink(1).Selectable().Tooltip(cur.file.Path)

		ui.Text(c, locales.ComposeEditorSaveHint()).FontSize(11).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).Justify(ui.End).Grow(1).Children(func() {
			if ui.Button(c, locales.CommonClose()).Clicked() {
				a.closeEditor()
			}
			// 没有改动时禁用保存按钮：按钮「灰掉」本身就是「已是最新」的提示。
			save := ui.PrimaryButton(c, locales.CommonSave())
			if !modified || cur.loadErr != "" || a.busyKey(editorBusyKey) {
				save.Disabled(true)
			}
			if save.Clicked() {
				a.saveCurrentEditorFile()
			}
		})
	})
}

// currentEditorFile 返回当前标签对应的文件，越界返回 nil。
func (a *App) currentEditorFile() *editorFile {
	if a.editorTab < 0 || a.editorTab >= len(a.editorFiles) {
		return nil
	}
	return &a.editorFiles[a.editorTab]
}

// editorBufferByPath 按文件路径找出当前缓冲区，没有则返回 nil。
//
// 后台协程回调里要用它而不是提前抓一个 *editorFile：
// 对话框可能在异步动作期间关闭或重开，抓到的指针会悬空（见 saveCurrentEditorFile）。
func (a *App) editorBufferByPath(path string) *editor.Buffer {
	for i := range a.editorFiles {
		if strings.EqualFold(a.editorFiles[i].file.Path, path) {
			return a.editorFiles[i].buffer
		}
	}
	return nil
}

// openEditor 载入项目的可编辑文件并打开对话框。
//
// 读盘在后台进行：项目目录可能在慢速磁盘甚至网络盘上，不能让界面卡住。
// 打开时把每个文件的内容装进新的 Buffer，所以「打开」总是拿到磁盘上的最新内容。
func (a *App) openEditor(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	// 先清空，避免上一个项目的标签在加载期间显示出来。
	a.closeEditorState()

	a.runAsync("editor-open", "", func(ctx context.Context) (string, error) {
		files, err := a.compose.EditableFiles(ctx, dir)
		if err != nil {
			return "", err
		}
		a.update(func() {
			a.editorProjectDir = dir
			a.editorTab = 0
			a.editorFiles = make([]editorFile, 0, len(files))
			for _, f := range files {
				ef := editorFile{file: f}
				if f.Err != "" {
					ef.loadErr = f.Err
				} else {
					ef.buffer = newEditorBuffer(f)
					ef.state = &editor.State{Buffer: ef.buffer}
				}
				a.editorFiles = append(a.editorFiles, ef)
			}
			a.editorOpen = true
		})
		return "", nil
	})
}

// newEditorBuffer 为文件内容建一个带语言高亮的缓冲区。
// newEditorBuffer 为文件内容建一个带语言高亮的缓冲区。
func newEditorBuffer(f compose.EditableFile) *editor.Buffer {
	buf := editor.NewBuffer(f.Content)
	// 按文件名挑语言：compose.yaml 得到 YAML。
	//
	// .env 要特别处理：MyGo 没有 dotenv 语言，而 LanguageForText(".env")
	// 返回 nil（Shell 认领的是 .envrc，不是 .env）。这里显式给 Shell：
	// .env 的 # 注释、引号、 展开与 shell 一致，高亮结果可接受。
	// 不给语言的后果不只是“没颜色”：注释与字符串都按纯文本画，也不好读。
	lang := editor.LanguageForText(f.Name, f.Content)
	if lang == nil && f.Kind == compose.FileKindEnv {
		lang = editor.LanguageNamed("Shell")
	}
	buf.SetLanguage(lang)
	return buf
}

// saveCurrentEditorFile 把当前标签的内容写回磁盘。
func (a *App) saveCurrentEditorFile() {
	cur := a.currentEditorFile()
	if cur == nil || cur.buffer == nil || cur.loadErr != "" {
		return
	}
	if !cur.buffer.Modified() {
		return
	}
	if a.busyKey(editorBusyKey) {
		return
	}

	path := cur.file.Path
	text := cur.buffer.String()
	// 记下要保存的那份文本：保存期间用户可能继续打字，
	// 完成后只应把「这一份」标记为已保存，否则会把新改动误标成已存盘。
	saved := text

	a.runAsync(editorBusyKey, "", func(ctx context.Context) (string, error) {
		if err := compose.WriteEditable(path, text); err != nil {
			return "", err
		}
		a.update(func() {
			// 不捕获上面的 cur：它是指向 a.editorFiles 切片元素的指针，
			// 而对话框可能在写盘期间关闭（a.editorFiles 被置 nil），
			// 也可能重新打开（切片被整体替换）。两者都会让这个指针悬空，
			// 读到旧对象甚至与写入竞争。按路径在当前切片里重新找。
			if buf := a.editorBufferByPath(path); buf != nil && buf.String() == saved {
				// 只有内容没再变化时才 MarkSaved：如果用户在写盘期间又改了，
				// Modified 必须继续为真，否则改动会被悄悄丢掉。
				buf.MarkSaved()
			}
			// 文件变了，项目要重新加载：服务表、命令预览都以此为准。
			a.refreshProjects()
		})
		return "", nil
	})
	a.toast(locales.ComposeEditorSaved(baseName(path)))
}

// requestCloseEditor 处理用户想关闭对话框的意图：
// 有未保存改动时先确认，没有就直接关。
func (a *App) requestCloseEditor() {
	var dirty []string
	for i := range a.editorFiles {
		if a.editorFiles[i].buffer != nil && a.editorFiles[i].buffer.Modified() {
			dirty = append(dirty, a.editorFiles[i].file.Name)
		}
	}
	if len(dirty) == 0 {
		a.closeEditor()
		return
	}
	// 复用全局确认框，避免多一套确认 UI。
	// danger=true：放弃的确实可能是用户刚敲的端口映射，用红色按钮提醒。
	a.confirm(
		locales.ComposeEditorDiscardTitle(),
		locales.ComposeEditorDiscardBody(strings.Join(dirty, "、")),
		true,
		func() { a.closeEditor() },
	)
}

// closeEditor 关闭对话框并释放缓冲。
func (a *App) closeEditor() {
	a.editorOpen = false
	a.closeEditorState()
}

// closeEditorState 清掉编辑器状态。
//
// 一定要清 Buffer：它持有整份文件文本与撤销历史，不释放就等于关掉对话框后
// 还一直占着内存。
func (a *App) closeEditorState() {
	a.editorFiles = nil
	a.editorTab = 0
	a.editorProjectDir = ""
	a.editorFocusPending = false
}

// editorHeight 给出编辑器控件的高度。
//
// 见 editorDialog 里的说明：Modal 面板按内容收缩，Grow 拿不到高度，
// 所以这里按窗口高度算一个确定值。
//
// 上下限的用意：小窗口（比如 760 高）留出标题、标签、底部按钮的位置，
// 编辑器仍要有可用的编辑面积；超大窗口下不让它吃掉整屏，
// 否则用户会以为其他内容消失了。
func (a *App) editorHeight(c *ui.Context) float32 {
	_, v := c.Size()
	h := v * 0.55
	if h < 240 {
		h = 240
	}
	if h > 620 {
		h = 620
	}
	// 窗口本身比下限还矮时，别再强行 240：比窗口还高的控件会被裁掉，
	// 底部按钮就点不到了。这时按可用高度给。
	if max := v - 260; h > max && max > 120 {
		h = max
	}
	return h
}
