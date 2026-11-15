package compose

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 最小的可用 compose 内容，测试里反复用。
const testCompose = "services:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\"\n"

// 生成的项目必须能被 compose-go 正常加载——这是「能用」的唯一标准。
func TestCreateProjectLoads(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	res, err := CreateProject(dir, testCompose, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("没写 .env 时应只生成 compose.yaml，实际: %v", res.Files)
	}

	c := New(newFake(t))
	p, err := c.LoadProject(t.Context(), dir)
	if err != nil {
		t.Fatalf("生成的项目加载失败: %v", err)
	}
	svc, ok := p.Config.Services["web"]
	if !ok {
		t.Fatalf("没有 web 服务: %+v", p.Config.Services)
	}
	if svc.Image != "nginx:alpine" {
		t.Errorf("镜像 = %q", svc.Image)
	}
}

// 用户写的 .env 要原样落盘，并参与变量插值。
func TestCreateProjectWritesEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "envdemo")
	compose := "services:\n  web:\n    image: \"nginx:${TAG}\"\n"
	env := "TAG=alpine\n"

	res, err := CreateProject(dir, compose, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("应生成 compose.yaml 与 .env，实际: %v", res.Files)
	}

	got, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	// 落盘时按平台写换行符，比较前统一。
	if strings.ReplaceAll(string(got), "\r\n", "\n") != env {
		t.Errorf(".env 内容不对: %q", got)
	}

	p, err := New(newFake(t)).LoadProject(t.Context(), dir)
	if err != nil {
		t.Fatalf("带 .env 的项目加载失败: %v", err)
	}
	if got := p.Config.Services["web"].Image; got != "nginx:alpine" {
		t.Errorf("变量没被插值，镜像 = %q", got)
	}
}

// .env 内容为空或只有空白时不创建文件。
//
// 回归测试：早先无条件写 .env，用户只想建 compose.yaml 时目录里
// 莫名多出一个空 .env，之后还容易被误认为「这个项目需要 env」。
func TestCreateProjectSkipsBlankEnv(t *testing.T) {
	for _, env := range []string{"", "   ", "\n\n", "\r\n"} {
		t.Run("env="+strings.ReplaceAll(env, "\n", "\\n"), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "noenv")
			res, err := CreateProject(dir, testCompose, env)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Files) != 1 {
				t.Errorf("空白 .env 不该生成文件，实际: %v", res.Files)
			}
			if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
				t.Errorf(".env 不该存在: %v", err)
			}
		})
	}
}

// 目录非空时必须拒绝，绝不能覆盖用户已有的文件。
//
// 回归测试：早先无条件写文件，如果用户选了一个已有项目的目录，
// 它会被悄悄换成刚生成的内容。
func TestCreateProjectRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "compose.yaml")
	existing := "services:\n  mine:\n    image: redis\n"
	if err := os.WriteFile(keep, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := CreateProject(dir, testCompose, "A=1\n")
	if err == nil {
		t.Fatal("非空目录应被拒绝")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("错误信息应说明目录非空，实际: %v", err)
	}

	// 原文件必须原样还在。
	got, _ := os.ReadFile(keep)
	if strings.ReplaceAll(string(got), "\r\n", "\n") != existing {
		t.Errorf("原项目被覆盖了: %q", got)
	}
}

// 空目录（存在但没有内容）是允许的，这是最常见的用法：mkdir 之后创建。
func TestCreateProjectAllowsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateProject(dir, testCompose, ""); err != nil {
		t.Fatalf("空目录应允许创建: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
}

// 目录不存在时要一路建出来。
func TestCreateProjectMakesNestedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if _, err := CreateProject(dir, testCompose, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
}

// 输入校验：这些错误必须给出可读说明，而且不能留下任何文件。
func TestCreateProjectValidation(t *testing.T) {
	cases := []struct {
		name    string
		dir     string
		compose string
		env     string
		want    string
	}{
		{"空目录", "  ", testCompose, "", "directory is empty"},
		{"compose 为空", filepath.Join(t.TempDir(), "x"), "", "", "compose.yaml is empty"},
		{"compose 只有空白", filepath.Join(t.TempDir(), "x"), "  \n\t\n", "", "compose.yaml is empty"},
		{
			"YAML 语法错误", filepath.Join(t.TempDir(), "x"),
			"services:\n  web: [unclosed\n", "", "not valid YAML",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateProject(tc.dir, tc.compose, tc.env)
			if err == nil {
				t.Fatal("应报错")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息 %q 不含 %q", err, tc.want)
			}
			// 校验失败时不应建出目录。
			if tc.dir != "" && strings.TrimSpace(tc.dir) != "" {
				if _, statErr := os.Stat(tc.dir); statErr == nil {
					t.Errorf("校验失败却创建了目录 %s", tc.dir)
				}
			}
		})
	}
}

// YAML 语法错误的报告要带行号：没有行号用户只能一行行找。
func TestCreateProjectYAMLErrorMentionsLine(t *testing.T) {
	// 前面的空行不算：yaml.v3 报的是有问题的位置（services 之下那一段），
	// 这里只验证「带行号」这件事。
	_, err := CreateProject(filepath.Join(t.TempDir(), "x"), "\n\nservices:\n  web: [unclosed\n", "")
	if err == nil {
		t.Fatal("应报错")
	}
	if !strings.Contains(err.Error(), "line ") {
		t.Errorf("错误信息应带行号，实际: %v", err)
	}
}

// 内容原样保留（除了平台换行符），包括注释与缩进：
// 用户写的东西不该在保存时被重新排版。
func TestCreateProjectKeepsContentVerbatim(t *testing.T) {
	content := "# 我的项目\nservices:\n  web:\n    image: nginx   # 注释留在行尾\n    environment:\n      A: \"1\"\n"
	dir := filepath.Join(t.TempDir(), "verbatim")
	if _, err := CreateProject(dir, content, "# env 注释\nA=1\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != content {
		t.Errorf("内容被改动了\n got: %q\nwant: %q", got, content)
	}
}

// 落盘用平台习惯的换行符（Windows 上 CRLF），用户在记事本里打开才正常。
func TestCreateProjectUsesPlatformLineEndings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "eol")
	if _, err := CreateProject(dir, testCompose, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	hasCRLF := strings.Contains(string(data), "\r\n")
	if runtime.GOOS == "windows" && !hasCRLF {
		t.Errorf("Windows 上应写 CRLF")
	}
	if runtime.GOOS != "windows" && hasCRLF {
		t.Errorf("非 Windows 平台不该写 CRLF")
	}
}

// 默认模板本身必须是合法 YAML，而且能被加载器接受。
//
// 回归测试：模板是用户第一眼看到的东西，它若有语法错，
// 每个新建的项目一开始就是坏的。
func TestDefaultComposeYAMLIsValid(t *testing.T) {
	if err := checkYAMLSyntax(DefaultComposeYAML); err != nil {
		t.Fatalf("默认模板有语法错: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "default")
	if _, err := CreateProject(dir, DefaultComposeYAML, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := New(newFake(t)).LoadProject(t.Context(), dir); err != nil {
		t.Errorf("默认模板生成的项目加载失败: %v", err)
	}
}
