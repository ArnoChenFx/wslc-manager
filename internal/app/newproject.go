// 本文件是「从零创建 compose 项目」的对话框。
//
// 只有三件事：选目录、写 compose.yaml、写 .env。
// 刻意不提供项目名 / 服务名 / 镜像 / 端口这些参数——那是替用户做决定，
// 而用户本来就要改内容。直接给代码编辑器让对方写，比填表单再猜生成结果更省事。
package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// newProjectBusyKey 是创建动作的忙碌标记。
const newProjectBusyKey = "new-project"

// defaultNewProjectName 是预填目录的最后一段。
// 用固定的占位名而不是时间戳或序号：用户可以一眼看出这段是要换的。
const defaultNewProjectName = "myapp"

// newProjectDialog 绘制新建项目对话框。
func (a *App) newProjectDialog(c *ui.Context) {
	t := c.Theme()

	ui.Modal(c, &a.newProjectOpen, func() {
		ui.Column(c).Width(820).Gap(10).Children(func() {
			ui.Text(c, locales.ComposeNewTitle()).FontSize(15).Bold()

			// ── 目录 ──────────────────────────────
			ui.Field(c, locales.ComposeNewDir(), func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c, &a.newProjectDir).
						Placeholder(locales.ComposeNewDirPlaceholder()).
						Grow(1).AutoFocus()
					if ui.Button(c, locales.CommonBrowse()).
						Tooltip(locales.ComposeNewDirBrowse()).Clicked() {
						// 选父目录后自动补一个子目录名，符合「一个项目一个目录」的习惯。
						// 留一个光秃的父目录很容易直接点「创建」，
						// 把 compose.yaml 造在一堆无关文件中间。
						a.pickDirectory(locales.ComposeNewDir(), func(parent string) {
							a.newProjectDir = filepath.Join(parent, defaultNewProjectName)
						})
					}
				})
			}).Description(locales.ComposeNewDirHint())

			// ── 内容：compose.yaml / .env 两个标签 ──
			names := []string{"compose.yaml", ".env"}
			before := a.newProjectTab
			ui.Tabs(c, &a.newProjectTab, names...)
			if a.newProjectTab != before {
				// 切标签后把键盘焦点交给新编辑器，否则焦点还留在目录输入框，
				// 用户在里面打字会静静地打到目录上去（实测就这样把标题打进了路径）。
				a.newProjectFocusPending = true
			}
			if a.newProjectTab < 0 || a.newProjectTab >= len(names) {
				a.newProjectTab = 0
			}

			state := a.newProjectComposeState
			if a.newProjectTab == 1 {
				state = a.newProjectEnvState
			}
			if state != nil {
				// 必须给确定高度：Modal 面板按内容收缩，Grow 拿不到剩余高度，
				// 实测会得到 height=0（见 editorDialog 里的同一处说明）。
				editor.View(c, state).Height(a.editorHeight(c)).FillWidth().
					Bind(&a.newProjectEditorH)
				if a.newProjectFocusPending {
					// 用 Handle.Focus 而不是 AutoFocus：AutoFocus 只在元素首次入树时生效，
					// 而切标签时编辑器已存在，AutoFocus 不会重新抢焦点，
					// 敲的字就落到了标签按钮或目录输入框上。
					a.newProjectEditorH.Focus(c)
					a.newProjectFocusPending = false
				}
			}

			// ── 当前标签的说明 ──────────────────────
			ui.Text(c, a.newProjectFieldHint()).FontSize(11).TextColor(t.TextMuted)

			// ── 错误 ──────────────────────────────
			if a.newProjectError != "" {
				ui.Text(c, a.newProjectError).FontSize(11).TextColor(t.Danger).Selectable()
			}

			// ── 动作 ──────────────────────────────
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.CommonCancel()).Clicked() {
					a.closeNewProject()
				}
				btn := ui.PrimaryButton(c, locales.CommonCreate())
				if a.newProjectBlocked() {
					btn.Disabled(true)
				}
				if btn.Clicked() && !a.newProjectBlocked() {
					a.createNewProject()
				}
			})
		})
	})
}

// newProjectFieldHint 说明当前标签对应的内容会怎么处理。
func (a *App) newProjectFieldHint() string {
	if a.newProjectTab == 1 {
		return locales.ComposeNewEnvHint()
	}
	return locales.ComposeNewComposeHint()
}

// newProjectBlocked 报告创建按钮是否应该置灰。
//
// 只看两个必填项，不访问磁盘（构建期不做 IO）：
// 目录为空、或 compose 内容是空的，都不该让用户点下去等一个必然失败的请求。
func (a *App) newProjectBlocked() bool {
	if a.busyKey(newProjectBusyKey) {
		return true
	}
	if strings.TrimSpace(a.newProjectDir) == "" {
		return true
	}
	if a.newProjectCompose == nil {
		return true
	}
	return strings.TrimSpace(a.newProjectCompose.String()) == ""
}

// createNewProject 在后台写出项目，成功后加进项目列表。
func (a *App) createNewProject() {
	if a.busyKey(newProjectBusyKey) {
		return
	}
	dir := strings.TrimSpace(a.newProjectDir)
	composeText := ""
	if a.newProjectCompose != nil {
		composeText = a.newProjectCompose.String()
	}
	envText := ""
	if a.newProjectEnv != nil {
		envText = a.newProjectEnv.String()
	}
	a.newProjectError = ""

	a.runAsync(newProjectBusyKey, "", func(ctx context.Context) (string, error) {
		res, err := compose.CreateProject(dir, composeText, envText)
		if err != nil {
			// 失败原因留在对话框里，而不是一闪而过的 toast：
			// 用户需要照着它改目录或内容，而对话框里正拿着他刚写的东西。
			a.update(func() { a.newProjectError = err.Error() })
			return "", err
		}
		a.update(func() {
			a.closeNewProject()
			// 加进项目列表并立刻刷新，用户马上能在服务表里看到新项目。
			a.settings.Projects = mergeDirs(a.settings.Projects, []string{res.Dir})
			a.saveSettings()
		})
		a.refreshProjects()
		return "", nil
	})
	a.toast(locales.ComposeNewCreated(baseName(dir)))
}

// openNewProject 打开新建对话框。
//
// 目录预填到设置里的**扫描根目录**下面：用户把代码目录设成扫描根之后，
// 新建项目的首选位置就是那里，不必每次手敲长路径。没设扫描根时留空。
// 两个编辑器都给默认内容：空编辑框只会让人先楞一下。
func (a *App) openNewProject() {
	a.closeNewProject()
	a.newProjectOpen = true
	a.newProjectTab = 0

	if parent := a.defaultProjectParent(); parent != "" {
		a.newProjectDir = filepath.Join(parent, defaultNewProjectName)
	}

	a.newProjectCompose = editor.NewBuffer(compose.DefaultComposeYAML)
	a.newProjectCompose.SetLanguage(editor.LanguageFor("compose.yaml"))
	a.newProjectComposeState = &editor.State{Buffer: a.newProjectCompose}

	a.newProjectEnv = editor.NewBuffer("")
	a.newProjectEnv.SetLanguage(editor.LanguageNamed("Shell"))
	a.newProjectEnvState = &editor.State{Buffer: a.newProjectEnv}

	a.newProjectFocusPending = true
}

// closeNewProject 关闭对话框并释放编辑缓冲。
//
// 一定要清 Buffer：它持有整份文本与撤销历史，留着就是白占内存，
// 而且下次打开会带着上回的残留内容。
func (a *App) closeNewProject() {
	a.newProjectOpen = false
	a.newProjectDir = ""
	a.newProjectCompose = nil
	a.newProjectComposeState = nil
	a.newProjectEnv = nil
	a.newProjectEnvState = nil
	a.newProjectTab = 0
	a.newProjectError = ""
	a.newProjectFocusPending = false
}

// defaultProjectParent 返回新建项目的默认上级目录：设置里的第一个扫描根。
//
// 为什么用扫描根而不是「上一个项目的父目录」：扫描根是用户明确声明「我的项目
// 都在这下面」的地方，语义稳定；从上一个项目推断出来的位置会随选中项漂移，
// 用户很难预期下次会建到哪里。
//
// 只跳过空白项：路径是否存在由 compose.CreateProject 在真正创建时判断，
// 预填阶段不去碰磁盘（构建期不做 IO）。
func (a *App) defaultProjectParent() string {
	for _, root := range a.settings.ScanRoots {
		if r := strings.TrimSpace(root); r != "" {
			return r
		}
	}
	return ""
}
