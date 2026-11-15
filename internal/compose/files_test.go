package compose

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 回归测试：WriteEditable 必须保留文件原有的 CRLF 换行。
// 早先直接把编辑器给的 LF 写回，整个 Windows 文件的行尾都被改写，
// git diff 里每一行都是改动，用户完全看不出真实修改是什么。
func TestWriteEditableKeepsCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("services:\r\n  web:\r\n    image: nginx\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 编辑器交回的一定是 LF。
	if err := WriteEditable(path, "services:\n  web:\n    image: nginx:alpine\n"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "services:\r\n  web:\r\n    image: nginx:alpine\r\n"
	if string(got) != want {
		t.Errorf("写入后换行符变了\n got: %q\nwant: %q", got, want)
	}
}

// 原文件是 LF 时不能被改成 CRLF：Linux 上写的文件在 Windows 上打开编辑，
// 不应该因为一次保存就把整个文件的行尾换掉。
func TestWriteEditableKeepsLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("services:\n  web:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEditable(path, "services:\n  web:\n    image: redis\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "\r\n") {
		t.Errorf("原文件是 LF，写入后却出现了 CRLF: %q", got)
	}
	if want := "services:\n  web:\n    image: redis\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// 新建的 .env（文件本来不存在）要能写出来，并且换行符跟随平台习惯。
func TestWriteEditableCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	if err := WriteEditable(path, "WEB_PORT=8080\nTAG=latest\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "WEB_PORT=8080\nTAG=latest\n"
	if runtime.GOOS == "windows" {
		want = strings.ReplaceAll(want, "\n", "\r\n")
	}
	if string(got) != want {
		t.Errorf("新文件内容不对\n got: %q\nwant: %q", got, want)
	}
}

// 目录不存在时要一路建出来，而不是报错。
// 新建项目的流程会用 WriteEditable 写第一份 compose.yaml。
func TestWriteEditableMakesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "compose.yaml")
	if err := WriteEditable(path, "services: {}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件没建出来: %v", err)
	}
}

// 保留原文件的权限位。临时文件默认 0600，改名会把权限一起带过去，
// 于是用户原本 0644 的文件在保存一次后变成只有自己能读。
func TestWriteEditableKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 权限位")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteEditable(path, "services:\n"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o640 {
		t.Errorf("权限被改成了 %o，应为 640", got)
	}
}

// 写入失败时不能留下临时文件（用户目录里不该出现 .compose.yaml.tmp*）。
func TestWriteEditableLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := WriteEditable(path, "services:\n  web:\n    image: nginx\n"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "compose.yaml" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("目录里应只有 compose.yaml，实际有: %v", names)
	}
}

// Windows 上 os.Rename 不能覆盖已存在的文件。这条测试确认「改一个有内容的
// 已有文件」也能成功——早先的实现会直接失败，用户按保存毫无反应。
func TestWriteEditableOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("old content that is long enough\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEditable(path, "new\n"); err != nil {
		t.Fatalf("覆盖已有文件失败: %v", err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "old content") {
		t.Errorf("旧内容还在: %q", got)
	}
}

// EditableFiles 要列出实际参与编排的文件，并把 .env 也列出来（即使不存在）。
func TestEditableFilesListsComposeAndEnv(t *testing.T) {
	dir := t.TempDir()
	compose := "services:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\"\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(newFake(t))
	files, err := c.EditableFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	byName := map[string]EditableFile{}
	for _, f := range files {
		names = append(names, f.Name)
		byName[f.Name] = f
	}

	if len(files) < 2 {
		t.Fatalf("至少要列出 compose.yaml 与 .env，实际: %v", names)
	}
	if got := byName["compose.yaml"]; got.Kind != FileKindCompose || got.Missing {
		t.Errorf("compose.yaml 应为存在中的 compose 文件，实际 %+v", got)
	}
	if got := byName["compose.yaml"].Content; got != compose {
		t.Errorf("内容不符\n got: %q\nwant: %q", got, compose)
	}
	env, ok := byName[".env"]
	if !ok {
		t.Fatalf(".env 应被列出（可新建），实际: %v", names)
	}
	if !env.Missing {
		t.Errorf("没有 .env 时应标为 Missing")
	}
	if env.Kind != FileKindEnv {
		t.Errorf(".env 的 Kind 应为 %q，实际 %q", FileKindEnv, env.Kind)
	}
}

// 已存在的 .env 要读出内容。
func TestEditableFilesReadsEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  web:\n    image: \"nginx:${TAG}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TAG=alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(newFake(t))
	files, err := c.EditableFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name != ".env" {
			continue
		}
		if f.Missing {
			t.Errorf(".env 存在却标成了 Missing")
		}
		if f.Content != "TAG=alpine\n" {
			t.Errorf(".env 内容不对: %q", f.Content)
		}
		return
	}
	t.Fatal("没有列出 .env")
}

// 回归测试：compose.yaml 写坏（YAML 语法错误）时，编辑入口仍然要能打开。
// 早先实现里加载失败就整次列举失败，导致最需要改文件的时候反而进不去编辑器。
func TestEditableFilesWorksWhenComposeIsBroken(t *testing.T) {
	dir := t.TempDir()
	broken := "services:\n  web: [unclosed\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(newFake(t))
	files, err := c.EditableFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("compose 文件坏了也应能列出文件: %v", err)
	}

	found := false
	for _, f := range files {
		if f.Name == "compose.yaml" {
			found = true
			if f.Content != broken {
				t.Errorf("坏文件的内容也要原样读出来供修复\n got: %q", f.Content)
			}
		}
	}
	if !found {
		t.Fatalf("compose.yaml 没被列出: %+v", files)
	}
}

// compose.override.yaml 存在时要一并列出：它同样是参与编排的文件。
func TestEditableFilesIncludesOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.override.yaml"),
		[]byte("services:\n  web:\n    ports:\n      - \"8080:80\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(newFake(t))
	files, err := c.EditableFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "compose.override.yaml") {
		t.Errorf("override 文件没被列出: %v", names)
	}
}

// 空目录：不能列出 .env 之外的任何东西，也不该报错。
func TestEditableFilesEmptyDir(t *testing.T) {
	c := New(newFake(t))
	files, err := c.EditableFiles(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("空目录不该报错: %v", err)
	}
	for _, f := range files {
		if f.Name != ".env" {
			t.Errorf("空目录里只应列出 .env，实际还有 %q", f.Name)
		}
	}
}

// 目录为空时明确报错，而不是返回一个空列表让界面猜。
func TestEditableFilesRejectsEmptyDir(t *testing.T) {
	c := New(newFake(t))
	if _, err := c.EditableFiles(context.Background(), "   "); err == nil {
		t.Fatal("空目录参数应报错")
	}
}

// 超过大小上限的文件不读进内存，但要给出可读的说明。
func TestReadEditableRejectsHugeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	big := strings.Repeat("A=1\n", maxEditableSize/4+1024)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readEditable(path); err == nil {
		t.Fatal("超大文件应被拒绝")
	} else if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("错误信息应说明太大，实际: %v", err)
	}
}

// 读到 CRLF 文件时要统一成 LF 再交给编辑器：
// 否则每行末尾多一个不可见字符，光标列号会整体偏移一位。
func TestReadEditableNormalizesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readEditable(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\nb\n" {
		t.Errorf("应统一成 LF，实际 %q", got)
	}
}

// 回归测试：compose.yaml 写坏时，编辑入口必须仍能打开 override 文件。
//
// 这条测试专门覆盖「加载失败 → 退回按命名约定探测」那条分支。
// 早先只有一条「override 被列出」的测试，而它是由加载器报告的
// （ComposeFiles 里本来就含 override），把回退分支删掉也照样通过——
// 变异验证抓出了这个盲区。
func TestEditableFilesFindsOverrideWhenComposeIsBroken(t *testing.T) {
	dir := t.TempDir()
	// 主文件写坏，加载器必然失败。
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  web: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.override.yaml"),
		[]byte("services:\n  web:\n    ports:\n      - \"8080:80\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(newFake(t))
	files, err := c.EditableFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("主文件坏了也应能列出文件: %v", err)
	}

	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "compose.yaml") {
		t.Errorf("主文件没列出: %v", names)
	}
	if !strings.Contains(joined, "compose.override.yaml") {
		t.Errorf("加载失败时也要按命名约定找到 override 文件，实际: %v", names)
	}
}
