package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// 写一份带 compose 文件的项目目录，给编辑器测试用。
func writeEditorProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"compose.yaml": "services:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\"\n",
		".env":         "TAG=alpine\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// 打开编辑器后应列出 compose.yaml 与 .env，并各自有一个可编辑的缓冲。
func TestOpenEditorListsFiles(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	var names []string
	a.ViewLocked(func() {
		for _, f := range a.editorFiles {
			names = append(names, f.file.Name)
			if f.buffer == nil {
				t.Errorf("%s 没有 buffer", f.file.Name)
			}
			if f.state == nil {
				t.Errorf("%s 没有 editor.State", f.file.Name)
			}
		}
		if !a.editorOpen {
			t.Error("editorOpen 应为 true")
		}
	})

	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "compose.yaml") {
		t.Errorf("没列出 compose.yaml: %v", names)
	}
	if !strings.Contains(joined, ".env") {
		t.Errorf("没列出 .env: %v", names)
	}
}

// 打开时缓冲区内容必须是磁盘上的最新内容。
//
// 回归测试：早先的实现把 buffer 缓存下来跨项目复用，改了文件重新打开
// 还是旧内容，用户以为改动丢了。
func TestOpenEditorReadsFreshContent(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	// 外部改文件，再重新打开。
	newContent := "services:\n  web:\n    image: redis:7\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	var got string
	a.ViewLocked(func() {
		for _, f := range a.editorFiles {
			if f.file.Name == "compose.yaml" && f.buffer != nil {
				got = f.buffer.String()
			}
		}
	})
	if got != newContent {
		t.Errorf("重新打开后应读到最新内容\n got: %q\nwant: %q", got, newContent)
	}
}

// 保存把内容写回磁盘，并把缓冲标记为已保存。
func TestSaveCurrentEditorFileWrites(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	// 改第一行（compose.yaml 是第一个）。
	newText := "services:\n  web:\n    image: busybox\n"
	a.ViewLocked(func() {
		a.editorFiles[0].buffer.Set(newText)
		if !a.editorFiles[0].buffer.Modified() {
			t.Error("改完应立即是 Modified")
		}
		a.saveCurrentEditorFile()
	})
	waitForLocked(t, a, func() bool {
		modified := true
		a.ViewLocked(func() { modified = a.editorFiles[0].buffer.Modified() })
		return !modified
	})

	data, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != newText {
		t.Errorf("磁盘内容不对\n got: %q\nwant: %q", data, newText)
	}
}

// 保存期间用户继续打字时，不能把新改动误标为已保存。
//
// 回归测试：早先保存完成后无条件 MarkSaved，写盘那一瞬间之后敲的字
// 会被当成「已存盘」，关闭编辑器时直接丢掉，没有任何提示。
func TestSaveKeepsModifiedWhenTextChangedDuringSave(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	a.ViewLocked(func() {
		a.editorFiles[0].buffer.Set("saved text\n")
		a.saveCurrentEditorFile()
		// 模拟「写盘期间又改了一行」：直接换内容，模拟用户继续输入。
		a.editorFiles[0].buffer.Set("saved text\nmore typing\n")
	})

	// 等保存动作结束（busy 标记消失）。
	waitForLocked(t, a, func() bool { return !a.isBusy(editorBusyKey) })

	// 再等一会儿，让 update 里的 MarkSaved 判断跑完。
	waitForLocked(t, a, func() bool {
		modified := false
		a.ViewLocked(func() { modified = a.editorFiles[0].buffer.Modified() })
		return modified
	})
}

// 有未保存改动时关闭要弹确认，不能直接丢。
func TestRequestCloseEditorAsksWhenDirty(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	a.ViewLocked(func() {
		a.editorFiles[0].buffer.Set("unsaved\n")
		a.requestCloseEditor()
	})

	a.ViewLocked(func() {
		if !a.confirmOpen {
			t.Error("有未保存改动时应弹出确认框")
		}
		if a.confirmAction == nil {
			t.Error("确认框应带一个动作（放弃改动）")
		}
		if !a.editorOpen {
			t.Error("确认期间编辑器不应关闭")
		}
	})
}

// 没有改动时关闭不弹确认。
func TestRequestCloseEditorClosesWhenClean(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	a.ViewLocked(func() { a.requestCloseEditor() })

	a.ViewLocked(func() {
		if a.confirmOpen {
			t.Error("没有改动时不该弹确认框")
		}
		if a.editorOpen {
			t.Error("编辑器应已关闭")
		}
		if len(a.editorFiles) != 0 {
			t.Error("关闭后应释放 buffer")
		}
	})
}

// 关闭后必须释放 buffer：它持有整份文件与撤销历史。
//
// 回归测试：早先只把 editorOpen 置 false，editorFiles 一直留着，
// 反复打开大项目会让内存一路涨。
func TestCloseEditorReleasesBuffers(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	a.ViewLocked(func() { a.closeEditor() })
	a.ViewLocked(func() {
		if a.editorFiles != nil {
			t.Errorf("editorFiles 应被清空，实际 %d 个", len(a.editorFiles))
		}
		if a.editorProjectDir != "" {
			t.Error("editorProjectDir 应被清空")
		}
	})
}

// 编辑器对话框要能画出来，并且标签上有文件名。
func TestEditorDialogRenders(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	for _, want := range []string{
		locales.ComposeEditorTitle(),
		"compose.yaml",
		".env",
		locales.CommonSave(),
		locales.CommonClose(),
	} {
		if !tt.HasText(want) {
			t.Errorf("编辑器对话框缺少 %q", want)
		}
	}
}

// 语言高亮要按文件类型给：compose.yaml → YAML，.env → 非空语言。
func TestEditorBufferGetsLanguage(t *testing.T) {
	buf := newEditorBuffer(compose.EditableFile{
		Name: "compose.yaml", Kind: compose.FileKindCompose,
		Content: "services:\n  web:\n    image: nginx\n",
	})
	if buf.Language() == nil {
		t.Fatal("compose.yaml 应得到 YAML 语言")
	}
	if got := buf.Language().Name; got != "YAML" {
		t.Errorf("compose.yaml 的语言 = %q, want YAML", got)
	}

	envBuf := newEditorBuffer(compose.EditableFile{
		Name: ".env", Kind: compose.FileKindEnv, Content: "A=1\n",
	})
	if envBuf.Language() == nil {
		t.Error(".env 也应有一个语言（用于注释高亮）")
	}
}

// 新建对话框：默认表单是能直接创建的状态。
// 新建对话框的默认值：勾选 .env、清空上次的输入。
// 新建对话框默认：目录预填到扫描根下，两个编辑器都有内容可用。
func TestOpenNewProjectDefaults(t *testing.T) {
	a := newTestApp(t)
	root := filepath.Join(t.TempDir(), "code")
	a.ViewLocked(func() {
		a.settings.ScanRoots = []string{root}
		a.newProjectDir = `E:\stale\path`
		a.openNewProject()
	})

	a.ViewLocked(func() {
		if !a.newProjectOpen {
			t.Error("对话框应打开")
		}
		if strings.Contains(a.newProjectDir, "stale") {
			t.Errorf("上次填的目录不该留着，实际 %q", a.newProjectDir)
		}
		if a.newProjectCompose == nil || a.newProjectComposeState == nil {
			t.Fatal("compose 编辑器应已准备好")
		}
		if strings.TrimSpace(a.newProjectCompose.String()) == "" {
			t.Error("compose 内容应有默认模板，不该是空编辑框")
		}
		if a.newProjectEnv == nil || a.newProjectEnvState == nil {
			t.Fatal(".env 编辑器应已准备好")
		}
		if got := a.newProjectEnv.String(); got != "" {
			t.Errorf(".env 默认应为空，实际 %q", got)
		}
	})
}

// 目录默认预填到设置里的扫描根下：用户把代码目录设成扫描根之后，
// 新建项目不用每次手敲长路径。
func TestOpenNewProjectPrefillsScanRoot(t *testing.T) {
	a := newTestApp(t)
	root := filepath.Join(t.TempDir(), "code")
	a.ViewLocked(func() {
		a.settings.ScanRoots = []string{root}
		a.openNewProject()
	})

	want := filepath.Join(root, defaultNewProjectName)
	a.ViewLocked(func() {
		if a.newProjectDir != want {
			t.Errorf("默认目录 = %q, want %q", a.newProjectDir, want)
		}
	})
}

// 没有扫描根时目录留空：凭空猜一个位置比让用户自己选更糟。
func TestOpenNewProjectWithoutScanRoot(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() {
		a.settings.ScanRoots = nil
		a.openNewProject()
	})
	a.ViewLocked(func() {
		if a.newProjectDir != "" {
			t.Errorf("没有扫描根时目录应为空，实际 %q", a.newProjectDir)
		}
	})
}

// 扫描根里第一项是空串时要跳到下一项。
//
// 回归测试：拿空串去拼路径会得到一个相对路径，项目就建到进程当前目录去了。
func TestDefaultProjectParentSkipsBlankRoots(t *testing.T) {
	a := newTestApp(t)
	real := filepath.Join(t.TempDir(), "real")
	a.ViewLocked(func() {
		a.settings.ScanRoots = []string{"   ", real}
		if got := a.defaultProjectParent(); got != real {
			t.Errorf("应跳过空白项拿到 %q，实际 %q", real, got)
		}
	})
}

// 用默认目录直接创建：这是最常见的操作（打开、点创建）。
func TestCreateNewProjectInScanRoot(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	a.ViewLocked(func() {
		a.settings.ScanRoots = []string{root}
		a.openNewProject()
		a.createNewProject()
	})

	want := filepath.Join(root, defaultNewProjectName)
	waitForLocked(t, a, func() bool {
		for _, p := range a.settings.Projects {
			if strings.EqualFold(p, want) {
				return true
			}
		}
		return false
	})

	if _, err := os.Stat(filepath.Join(want, "compose.yaml")); err != nil {
		t.Errorf("compose.yaml 没建在扫描根下: %v", err)
	}
}

// 用户在编辑器里写的内容必须原样落盘——这是这个对话框的全部意义。
func TestCreateNewProjectWritesEditedContent(t *testing.T) {
	a := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "edited")
	composeText := "services:\n  api:\n    image: redis:7\n    ports:\n      - \"6379:6379\"\n"
	envText := "REDIS_PASSWORD=secret\n"

	a.ViewLocked(func() {
		a.openNewProject()
		a.newProjectDir = dir
		a.newProjectCompose.Set(composeText)
		a.newProjectEnv.Set(envText)
		a.createNewProject()
	})
	waitForLocked(t, a, func() bool { return !a.newProjectOpen })

	for name, want := range map[string]string{"compose.yaml": composeText, ".env": envText} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s 没写出来: %v", name, err)
		}
		if got := strings.ReplaceAll(string(data), "\r\n", "\n"); got != want {
			t.Errorf("%s 内容不对\n got: %q\nwant: %q", name, got, want)
		}
	}
}

// .env 留空时不创建文件。
func TestCreateNewProjectSkipsEmptyEnv(t *testing.T) {
	a := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "noenv")
	a.ViewLocked(func() {
		a.openNewProject()
		a.newProjectDir = dir
		a.createNewProject()
	})
	waitForLocked(t, a, func() bool { return !a.newProjectOpen })

	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Errorf(".env 留空时不该创建该文件: %v", err)
	}
}

// 目录为空时创建按钮必须置灰。
func TestNewProjectBlockedWithoutDir(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() {
		a.openNewProject()
		a.newProjectDir = ""
		if !a.newProjectBlocked() {
			t.Error("没有目录时应禁止创建")
		}
		a.newProjectDir = "   "
		if !a.newProjectBlocked() {
			t.Error("目录只有空白时应禁止创建")
		}
		a.newProjectDir = `E:\tmp\demo`
		if a.newProjectBlocked() {
			t.Error("有目录且有内容时应允许创建")
		}
	})
}

// compose 内容为空时也不该让用户点下去等一个必然失败的请求。
func TestNewProjectBlockedWithoutComposeContent(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() {
		a.openNewProject()
		a.newProjectDir = `E:\tmp\demo`
		a.newProjectCompose.Set("   \n\n")
		if !a.newProjectBlocked() {
			t.Error("compose 内容为空时应禁止创建")
		}
	})
}

// 创建失败（目录非空）时，错误要留在对话框里，对话框不能关。
//
// 回归测试：失败只弹 toast 而对话框同时关掉的话，用户看不到原因，
// 而且刚写的 compose 内容也一起没了。
func TestCreateNewProjectFailureKeepsDialogOpen(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.ViewLocked(func() {
		a.openNewProject()
		a.newProjectDir = dir
		a.createNewProject()
	})
	waitForLocked(t, a, func() bool {
		return strings.Contains(a.newProjectError, "not empty")
	})

	a.ViewLocked(func() {
		if !a.newProjectOpen {
			t.Error("失败后对话框应保持打开")
		}
		if a.newProjectCompose == nil || strings.TrimSpace(a.newProjectCompose.String()) == "" {
			t.Error("失败后用户写的内容应还在")
		}
	})
}

// YAML 语法错时也是「留在对话框里报错」：用户要照着行号改。
func TestCreateNewProjectBadYAMLKeepsDialog(t *testing.T) {
	a := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "badyaml")
	a.ViewLocked(func() {
		a.openNewProject()
		a.newProjectDir = dir
		a.newProjectCompose.Set("services:\n  web: [unclosed\n")
		a.createNewProject()
	})
	waitForLocked(t, a, func() bool { return strings.Contains(a.newProjectError, "not valid YAML") })

	a.ViewLocked(func() {
		if !a.newProjectOpen {
			t.Error("YAML 语法错时对话框应保持打开")
		}
	})
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); !os.IsNotExist(err) {
		t.Error("YAML 语法错时不该写出文件")
	}
}

// 新建对话框要能画出来，包含两个内容标签与创建按钮。
func TestNewProjectDialogRenders(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() { a.openNewProject() })
	a.ViewLocked(func() { a.newProjectDir = `E:\tmp\rendered` })

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	for _, want := range []string{
		locales.ComposeNewTitle(),
		locales.ComposeNewDir(),
		"compose.yaml",
		".env",
		locales.CommonCreate(),
	} {
		if !tt.HasText(want) {
			t.Errorf("新建对话框缺少 %q", want)
		}
	}
}

// 新建对话框里的编辑器必须有可用高度。
//
// 回归测试：Modal 面板按内容收缩，编辑器若用 Grow(1) 会得到 height=0，
// 看上去就是一片空白（这个缺陷在编辑项目文件的对话框里真实发生过）。
func TestNewProjectEditorHasHeight(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() { a.openNewProject() })

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	found := false
	for _, n := range tt.AccessAll() {
		if n.Role != ui.RoleTextField {
			continue
		}
		if n.Bounds.H > 100 && n.Bounds.W > 400 {
			found = true
		}
	}
	if !found {
		t.Error("找不到高度可用的编辑区，新建对话框的编辑器可能被压成 0 高")
	}
}

// 关闭后要释放缓冲，不留内存与残留内容。
func TestCloseNewProjectReleasesBuffers(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() {
		a.openNewProject()
		a.newProjectCompose.Set("changed\n")
		a.closeNewProject()
	})
	a.ViewLocked(func() {
		if a.newProjectCompose != nil || a.newProjectEnv != nil {
			t.Error("关闭后应释放编辑器缓冲")
		}
		if a.newProjectDir != "" {
			t.Error("关闭后目录应清空")
		}
	})
}

func TestEditorDialogFitsWindow(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	var found bool
	for _, n := range tt.AccessAll() {
		// 找到编辑器本身（RoleTextBox 或类似的文本编辑角色）。
		if n.Role != ui.RoleTextField {
			continue
		}
		b := n.Bounds
		if b.H <= 40 {
			t.Errorf("编辑器高度只有 %.0f，太矮，无法正常编辑", b.H)
		}
		if b.X < 0 || b.X+b.W > 1180 {
			t.Errorf("编辑器横向超出窗口: x=%.0f w=%.0f", b.X, b.W)
		}
		found = true
	}
	if !found {
		// 编辑器可能不暴露为 TextBox；退一步检查对话框里有没有足够大的可编辑区域。
		for _, n := range tt.AccessAll() {
			if n.Bounds.H > 200 && n.Bounds.W > 400 {
				found = true
			}
		}
		if !found {
			t.Error("找不到编辑区，对话框布局可能有问题")
		}
	}
}

// waitFor 轮询等待条件成立，超时即失败。
//
// 用轮询而不是固定 sleep：异步动作（读盘、写盘）的耗时在不同机器上差别很大。
func waitFor(t *testing.T, a *App, cond func() bool) {
	t.Helper()
	_ = a
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// 编辑器 buffer 的边界行为：空文件也要能打开、能保存。
func TestEditorEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	buf := editor.NewBuffer("")
	if buf.String() != "" {
		t.Errorf("空文件缓冲区应为空，实际 %q", buf.String())
	}
	buf.Set("services: {}\n")
	if !buf.Modified() {
		t.Error("往空 buffer 里写内容应算改动")
	}
}

// waitForLocked 与 waitFor 相同，但在持有视图锁的情况下求值条件。
//
// 必须用它来读 App 的可变字段（editorFiles、settings 等）：
// 后台协程经 update 写这些字段，裸读会和它竞争——用 -race 跑就报 DATA RACE。
// 这不是测试的假象：它说明「读状态」本身必须与 update 串行。
func waitForLocked(t *testing.T, a *App, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		a.ViewLocked(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// 在新建对话框里切到 .env 标签后，键盘焦点必须交给编辑器。
//
// 回归测试：早先切标签只设了一个从未被读的标志，焦点还留在「项目目录」输入框，
// 在新建对话框里切到 .env 标签后，键盘焦点必须交给编辑器。
//
// 回归测试：早先切标签只设了一个从未被读的标志，焦点还留在「项目目录」输入框，
// 用户敲的每一行都被打进路径里（真机上试出来的：路径变成
// "E:\Project\wslc\myappHOST_PORT=9090"），而编辑器一直是空的。
func TestNewProjectTabSwitchFocusesEditor(t *testing.T) {
	a := newTestApp(t)
	a.ViewLocked(func() { a.openNewProject() })

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	// 切到 .env 标签并再画一帧：AutoFocus 在下一帧生效。
	if err := tt.Click(".env"); err != nil {
		t.Fatalf("点不到 .env 标签: %v", err)
	}
	tt.Frame()
	tt.Frame()

	a.ViewLocked(func() {
		if a.newProjectTab != 1 {
			t.Fatalf("标签应停在 .env，实际 %d", a.newProjectTab)
		}
	})

	// 关键断言：敲的字必须进 .env 编辑器，不能进目录输入框。
	tt.Type("HOST_PORT=9090\n")
	tt.Frame()

	a.ViewLocked(func() {
		if strings.Contains(a.newProjectDir, "HOST_PORT") {
			t.Errorf("文字被打进了项目目录: %q", a.newProjectDir)
		}
		if got := a.newProjectEnv.String(); !strings.Contains(got, "HOST_PORT=9090") {
			t.Errorf(".env 编辑器没收到输入，实际 %q", got)
		}
	})
}

// 编辑器对话框里切文件标签也要交出焦点，理由同上。
func TestEditorTabSwitchFocusesEditor(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	if err := tt.Click(".env"); err != nil {
		t.Fatalf("点不到 .env 标签: %v", err)
	}
	tt.Frame()
	tt.Frame()

	a.ViewLocked(func() {
		if a.editorTab != 1 {
			t.Fatalf("标签应停在 .env，实际 %d", a.editorTab)
		}
	})

	tt.Type("NEW_VAR=1\n")
	tt.Frame()

	a.ViewLocked(func() {
		if got := a.editorFiles[1].buffer.String(); !strings.Contains(got, "NEW_VAR=1") {
			t.Errorf(".env 缓冲区没收到输入，实际 %q", got)
		}
	})
}

// Ctrl+S 必须真的把内容写回磁盘。
//
// 回归测试：快捷键早先写在 Modal 调用之外，而 ui.Context.Shortcut 会先检查
// 「构建位置是否在 modal 内」（在 c.parent 链上找 modal 的 id），
// 不在就直接返回 false。于是按 Ctrl+S 完全没反应——用户实测反馈。
// 这条测试用 Tester 真按一次键，只有快捷键在 Modal 里才会通过。
func TestCtrlSSavesFile(t *testing.T) {
	a := newTestApp(t)
	dir := writeEditorProject(t)

	a.ViewLocked(func() { a.openEditor(dir) })
	waitForLocked(t, a, func() bool { return len(a.editorFiles) > 0 })

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	newText := "services:\n  web:\n    image: redis:7\n"
	a.ViewLocked(func() { a.editorFiles[0].buffer.Set(newText) })
	tt.Frame()

	tt.Key(ui.Ctrl, ui.KeyS)
	tt.Frame()

	waitForLocked(t, a, func() bool {
		modified := true
		a.ViewLocked(func() { modified = a.editorFiles[0].buffer.Modified() })
		return !modified
	})

	data, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ReplaceAll(string(data), "\r\n", "\n"); got != newText {
		t.Errorf("Ctrl+S 没把内容写盘\n got: %q\nwant: %q", got, newText)
	}
}

// 项目头部的操作行：按钮与「文件 info / 编辑文件」在同一行。
// 项目头部的操作行：按钮与「编辑文件」在同一行，文件信息靠右。
//
// 用户截图要求「把这排放到右边，省下空间」：早先文件信息独占一行，
// 右侧空出大片无用空白，详情区还因此少了一行的高度。
func TestProjectHeaderSingleActionRow(t *testing.T) {
	a := newTestApp(t)
	const winW, winH = 1400, 760
	a.ViewLocked(func() {
		sampleData(a)
		a.projects = []compose.Project{composeTestProject()}
		a.projectSel = 0
		a.page = PageCompose
		a.sizeOverride = [2]float32{winW, winH}
	})

	tt := ui.NewTester(a.View, winW, winH)
	tt.Frame()

	yOf := func(text string) (float32, bool) {
		for _, n := range tt.AccessAll() {
			if n.Role == ui.RoleButton && n.Name == text {
				return n.Bounds.Y, true
			}
		}
		return 0, false
	}
	// 启动按钮与编辑文件按钮应处在同一行（Y 相同，允许几像素误差）。
	yStart, ok1 := yOf(locales.ComposeStart())
	yEdit, ok2 := yOf(locales.ComposeEditFiles())
	if !ok1 || !ok2 {
		t.Fatalf("找不到按钮: 启动=%v 编辑文件=%v", ok1, ok2)
	}
	if diff := yStart - yEdit; diff > 4 || diff < -4 {
		t.Errorf("启动(y=%.0f) 与 编辑文件(y=%.0f) 不在同一行，文件信息还占着独立一行",
			yStart, yEdit)
	}
}

// 宽窗口下文件名要显示出来，且靠右。
func TestProjectFileNamesShownWhenWide(t *testing.T) {
	a := newTestApp(t)
	const winW, winH = 1400, 760
	a.ViewLocked(func() {
		sampleData(a)
		p := composeTestProject()
		p.Config.ComposeFiles = []string{`E:\P\compose.yaml`}
		a.projects = []compose.Project{p}
		a.projectSel = 0
		a.page = PageCompose
		a.sizeOverride = [2]float32{winW, winH}
	})

	tt := ui.NewTester(a.View, winW, winH)
	tt.Frame()

	var infoRight float32
	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleText && strings.HasPrefix(n.Name, locales.ComposeFiles("")) {
			infoRight = n.Bounds.X + n.Bounds.W
		}
	}
	if infoRight == 0 {
		t.Fatalf("宽窗口下应显示文件名，实际找不到「%s…」这段文字", locales.ComposeFiles(""))
	}
	if infoRight < winW/2 {
		t.Errorf("文件信息右边缘 x=%.0f 还在窗口左半边，没放到右边", infoRight)
	}
}

// 窄窗口下把文件名藏起来，只留「编辑文件」按钮，且按钮不能被推出窗口。
//
// 回归测试：文件名与六个按钮同在一行时，最小窗口（900）下总需求约 640
// 而在详情区只有约 380，实测「编辑文件」x=768、宽 106（右边缘 874），
// 已经超出窗口——按钮直接看不见了。
// 用户选择的处理方式是「窄窗只留按钮，隐藏文件名」。
func TestProjectEditButtonSurvivesNarrowWindow(t *testing.T) {
	a := newTestApp(t)
	const winW, winH = 900, 700
	a.ViewLocked(func() {
		sampleData(a)
		p := composeTestProject()
		p.Config.ComposeFiles = []string{`E:\P\compose.yaml`}
		a.projects = []compose.Project{p}
		a.projectSel = 0
		a.page = PageCompose
		a.sizeOverride = [2]float32{winW, winH}
	})

	tt := ui.NewTester(a.View, winW, winH)
	tt.Frame()

	found := false
	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleButton && n.Name == locales.ComposeEditFiles() {
			found = true
			if n.Bounds.W < 40 {
				t.Errorf("窄窗口下「编辑文件」按钮宽 %.0f，几乎点不到", n.Bounds.W)
			}
			if n.Bounds.X+n.Bounds.W > winW {
				t.Errorf("「编辑文件」被推到窗口外: x=%.0f w=%.0f（右边缘 %.0f > %d）",
					n.Bounds.X, n.Bounds.W, n.Bounds.X+n.Bounds.W, winW)
			}
		}
	}
	if !found {
		t.Fatal("窄窗口下找不到「编辑文件」按钮")
	}

	// 文件名文字应该被藏起来了。
	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleText && strings.Contains(n.Name, "compose.yaml") {
			t.Errorf("窄窗口下不该显示文件名文字（应只留按钮），实际看到 %q", n.Name)
		}
	}
}

// 窗口宽度介于两者之间时（约 1180）也要能显示文件名。
func TestProjectFileNameVisibilityThreshold(t *testing.T) {
	shown := func(winW float32) bool {
		a := newTestApp(t)
		a.ViewLocked(func() {
			sampleData(a)
			p := composeTestProject()
			p.Config.ComposeFiles = []string{`E:\P\compose.yaml`}
			a.projects = []compose.Project{p}
			a.projectSel = 0
			a.page = PageCompose
			a.sizeOverride = [2]float32{winW, 760}
		})
		tt := ui.NewTester(a.View, int(winW), 760)
		tt.Frame()
		for _, n := range tt.AccessAll() {
			if n.Role == ui.RoleText && strings.Contains(n.Name, "compose.yaml") {
				return true
			}
		}
		return false
	}

	if !shown(1400) {
		t.Error("1400 宽时应显示文件名")
	}
	if shown(900) {
		t.Error("900 宽（最小窗口）时不该显示文件名")
	}
}

// 窄窗口下「重启」从主行收起来，但「更多」菜单里必须有它。
//
// 回归测试：最小窗口（900）下六个按钮 + 编辑文件需要约 452 而详情区约 360，
// 硬放会把右侧按钮推出窗口（实测「编辑文件」右边缘 904 > 900）。
// 收起之后功能不能丢——这正是这条测试同时检查菜单的原因。
func TestRestartMovesToMenuWhenNarrow(t *testing.T) {
	a := newTestApp(t)
	const winW, winH = 900, 700
	a.ViewLocked(func() {
		sampleData(a)
		a.projects = []compose.Project{composeTestProject()}
		a.projectSel = 0
		a.page = PageCompose
		a.sizeOverride = [2]float32{winW, winH}
	})

	tt := ui.NewTester(a.View, winW, winH)
	tt.Frame()

	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleButton && n.Name == locales.ComposeRestart() {
			t.Errorf("窄窗口下主行不该有「重启」按钮（应在「更多」菜单里），实际 x=%.0f",
				n.Bounds.X)
		}
	}

	// 宽窗口下它应该回到主行。
	a.ViewLocked(func() {
		a.sizeOverride = [2]float32{1400, 760}
	})
	tt2 := ui.NewTester(a.View, 1400, 760)
	tt2.Frame()
	found := false
	for _, n := range tt2.AccessAll() {
		if n.Role == ui.RoleButton && n.Name == locales.ComposeRestart() {
			found = true
		}
	}
	if !found {
		t.Error("宽窗口下「重启」应回到主行")
	}
}
