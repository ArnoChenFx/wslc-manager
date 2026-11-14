package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "nested", "settings.json"))
	got, err := s.Load()
	if err != nil {
		t.Fatalf("文件不存在时不应报错: %v", err)
	}
	if got.ScanDepth <= 0 {
		t.Errorf("默认 ScanDepth 应大于 0, got %d", got.ScanDepth)
	}
	if got.PollSeconds <= 0 {
		t.Errorf("默认 PollSeconds 应大于 0, got %d", got.PollSeconds)
	}
	if got.SidebarWidth <= 0 {
		t.Errorf("默认 SidebarWidth 应大于 0, got %v", got.SidebarWidth)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s := New(path)

	want := Settings{
		Projects:     []string{`E:\Project\a`, `E:\Project\b`},
		ScanRoots:    []string{`E:\Project`},
		ScanDepth:    5,
		PollSeconds:  10,
		HideStopped:  true,
		SidebarWidth: 260,
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !reflect.DeepEqual(got.Projects, want.Projects) {
		t.Errorf("Projects = %v, want %v", got.Projects, want.Projects)
	}
	if got.ScanDepth != want.ScanDepth || got.PollSeconds != want.PollSeconds {
		t.Errorf("数值字段不匹配: %+v", got)
	}
	if !got.HideStopped {
		t.Error("HideStopped 丢失")
	}
	if got.SidebarWidth != want.SidebarWidth {
		t.Errorf("SidebarWidth = %v, want %v", got.SidebarWidth, want.SidebarWidth)
	}
}

func TestLoadPartialFile(t *testing.T) {
	// 只写了部分字段的文件，其余应落回默认值。
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"projects":["E:\\x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0] != `E:\x` {
		t.Errorf("Projects = %v", got.Projects)
	}
	if got.PollSeconds <= 0 || got.ScanDepth <= 0 {
		t.Errorf("缺失字段应使用默认值: %+v", got)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	// 坏文件应返回 error，同时给出默认值，不要让应用启动失败。
	got, err := New(path).Load()
	if err == nil {
		t.Error("损坏的 JSON 应返回错误")
	}
	if got.ScanDepth <= 0 {
		t.Error("即使出错也应返回可用的默认值")
	}
}

func TestCleanPathsDedup(t *testing.T) {
	got := cleanPaths([]string{`E:\a`, `E:\a`, "", `E:\b`})
	if len(got) != 2 {
		t.Errorf("应去重并去掉空串, got %v", got)
	}
}

func TestCleanPathsCaseInsensitiveDedup(t *testing.T) {
	got := cleanPaths([]string{`E:\Project`, `E:\project`})
	if len(got) != 1 {
		t.Errorf("Windows 路径应忽略大小写去重, got %v", got)
	}
}

func TestLoadClampsInvalidValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// 手工写一个带非法值的文件：ScanDepth 0、SidebarWidth 负数。
	if err := os.WriteFile(path,
		[]byte(`{"scanDepth":0,"sidebarWidth":-5,"projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ScanDepth <= 0 {
		t.Errorf("ScanDepth 应被修正, got %d", got.ScanDepth)
	}
	if got.SidebarWidth <= 0 {
		t.Errorf("SidebarWidth 应被修正, got %v", got.SidebarWidth)
	}
}
