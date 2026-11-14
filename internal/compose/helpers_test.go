package compose

import (
	"os"
	"path/filepath"
	"testing"
)

// 下面几个小助手避免在测试里重复 import os / filepath。

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件 %s 失败: %v", p, err)
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建目录 %s 失败: %v", dir, err)
	}
}

func filepathJoin(elem ...string) string { return filepath.Join(elem...) }
