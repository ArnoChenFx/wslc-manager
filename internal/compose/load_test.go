package compose

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

// writeProject 在临时目录里写出一组文件（相对路径 → 内容），返回目录。
// compose 文件用 "compose.yaml" 这个键。
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// loadYAML 加载一段 compose 文本。环境固定为空，结果不受运行机器影响。
func loadYAML(t *testing.T, yaml string, extra ...map[string]string) *types.Project {
	t.Helper()
	return loadYAMLEnv(t, yaml, []string{}, extra...)
}

// loadYAMLEnv 同 loadYAML，但可以指定插值用的环境。
func loadYAMLEnv(t *testing.T, yaml string, environ []string, extra ...map[string]string) *types.Project {
	t.Helper()
	files := map[string]string{"compose.yaml": yaml}
	for _, m := range extra {
		for k, v := range m {
			files[k] = v
		}
	}
	dir := writeProject(t, files)
	p, err := loadProject(context.Background(), LoadOptions{Dir: dir, Environ: environ})
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	return p
}

func TestLoadProjectName(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "My_App")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	load := func(o LoadOptions) (*types.Project, error) {
		o.Dir, o.Environ = dir, append(o.Environ, "X=1")
		return loadProject(context.Background(), o)
	}
	const svc = "services:\n  a:\n    image: x\n"

	write(svc)
	p, err := load(LoadOptions{})
	if err != nil || p.Name != "my_app" {
		t.Fatalf("目录名应规范化成 my_app, got %q, %v", nameOf(p), err)
	}

	write("name: From-File\n" + svc)
	if p, _ = load(LoadOptions{}); nameOf(p) != "from-file" {
		t.Errorf("文件里的 name 应优先于目录名, got %q", nameOf(p))
	}
	if p, _ = load(LoadOptions{Environ: []string{"COMPOSE_PROJECT_NAME=from-env"}}); nameOf(p) != "from-env" {
		t.Errorf("COMPOSE_PROJECT_NAME 应优先于文件, got %q", nameOf(p))
	}
	if p, _ = load(LoadOptions{ProjectName: "Explicit Name!"}); nameOf(p) != "explicitname" {
		t.Errorf("-p 应最优先且被规范化, got %q", nameOf(p))
	}
	if _, err = load(LoadOptions{ProjectName: "!!!"}); err == nil {
		t.Error("无法规范化出有效名字时应报错")
	}
}

func nameOf(p *types.Project) string {
	if p == nil {
		return ""
	}
	return p.Name
}

func TestLoadEnvPrecedence(t *testing.T) {
	// 进程环境 > .env；变量缺省值语法由 compose-go 负责，这里只验证我们接线正确。
	files := map[string]string{
		"compose.yaml": "services:\n  a:\n    image: ${IMG:-fallback}:${TAG}\n    environment:\n      V: ${V}\n",
		".env":         "TAG=from-dotenv\nV=from-dotenv\n",
	}
	dir := writeProject(t, files)
	p, err := loadProject(context.Background(), LoadOptions{Dir: dir, Environ: []string{"V=from-process"}})
	if err != nil {
		t.Fatal(err)
	}
	s := p.Services["a"]
	if s.Image != "fallback:from-dotenv" {
		t.Errorf("image = %q", s.Image)
	}
	if v := s.Environment["V"]; v == nil || *v != "from-process" {
		t.Errorf("进程环境应覆盖 .env, V = %v", v)
	}

	// --env-file 取代默认的 .env。
	if err := os.WriteFile(filepath.Join(dir, "other.env"), []byte("TAG=from-other\nV=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err = loadProject(context.Background(), LoadOptions{Dir: dir, EnvFile: "other.env", Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Services["a"].Image; got != "fallback:from-other" {
		t.Errorf("--env-file 未生效, image = %q", got)
	}
}

func TestLoadEnvFileMergedIntoEnvironment(t *testing.T) {
	// env_file 的内容由 compose-go 并入 environment，所以我们不需要再处理 --env-file；
	// environment 里显式写的值优先于 env_file。
	p := loadYAML(t, "services:\n  a:\n    image: x\n    env_file: [e.env]\n    environment:\n      B: explicit\n",
		map[string]string{"e.env": "A=1\nB=from-file\n"})
	env := p.Services["a"].Environment
	if env["A"] == nil || *env["A"] != "1" || env["B"] == nil || *env["B"] != "explicit" {
		t.Errorf("environment = %v", env)
	}
}

func TestLoadDiscoversParentDirectory(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"compose.yaml":          "services:\n  a:\n    image: x\n",
		"sub/deeper/keep":       "",
		"compose.override.yaml": "services:\n  a:\n    environment:\n      O: '1'\n",
	})
	p, err := loadProject(context.Background(), LoadOptions{Dir: filepath.Join(dir, "sub", "deeper"), Environ: []string{}})
	if err != nil {
		t.Fatalf("应向上查找父目录: %v", err)
	}
	if len(p.ComposeFiles) != 2 {
		t.Errorf("compose.override.yaml 应一并叠加, files = %v", p.ComposeFiles)
	}
	if p.Services["a"].Environment["O"] == nil {
		t.Error("override 文件的内容没有叠加进来")
	}
}

func TestLoadNoComposeFile(t *testing.T) {
	_, err := loadProject(context.Background(), LoadOptions{Dir: t.TempDir(), Environ: []string{}})
	if err == nil || !strings.Contains(err.Error(), "no compose file found") {
		t.Errorf("错误 = %v", err)
	}
}

func TestLoadMultipleFilesAndAllProfiles(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"a.yaml": "services:\n  web:\n    image: nginx:1\n    ports: ['80:80']\n  dbg:\n    image: busybox\n    profiles: [debug]\n",
		"b.yaml": "services:\n  web:\n    image: nginx:2\n    ports: ['90:90']\n",
	})
	p, err := loadProject(context.Background(), LoadOptions{Dir: dir, Files: []string{"a.yaml", "b.yaml"}, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Services["web"].Image; got != "nginx:2" {
		t.Errorf("后面的文件应覆盖前面的, image = %q", got)
	}
	if len(p.Services["web"].Ports) != 2 {
		t.Errorf("ports 应拼接, got %d", len(p.Services["web"].Ports))
	}
	// 加载时启用全部 profile：界面要列出所有服务。
	if _, ok := p.Services["dbg"]; !ok {
		t.Error("带 profile 的服务也应被加载")
	}
}

func TestLoadValidationErrorNamesTheField(t *testing.T) {
	dir := writeProject(t, map[string]string{"compose.yaml": "services:\n  a:\n    image: x\n    ports: [not-a-port]\n"})
	_, err := loadProject(context.Background(), LoadOptions{Dir: dir, Environ: []string{}})
	if err == nil {
		t.Fatal("非法端口应报错")
	}
}

func TestSortedServices(t *testing.T) {
	p := loadYAML(t, `
services:
  web:
    image: x
    depends_on: [api, cache]
  api:
    image: x
    depends_on: [db]
  db:
    image: x
  cache:
    image: x
  opt:
    image: x
  uses_opt:
    image: x
    depends_on:
      opt:
        condition: service_started
        required: false
`)
	names := func(ss []types.ServiceConfig, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s.Name
		}
		return out
	}

	// 全部：按字母序遍历，每个服务先深入访问自己的依赖。
	// 保证的性质：被依赖的在前，而且每次输出都一样（map 遍历顺序是随机的，这里跑 20 次验证）。
	for i := 0; i < 20; i++ {
		got := names(sortedServices(p, nil))
		want := []string{"db", "api", "cache", "opt", "uses_opt", "web"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 次: 顺序 = %v, 期望 %v", i, got, want)
		}
	}
	// 点名 web：带上传递依赖。
	if got, want := names(sortedServices(p, []string{"web"})), []string{"db", "api", "cache", "web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("点名 web = %v, 期望 %v", got, want)
	}
	// required:false 的依赖不会因为被依赖而被拉起。
	if got, want := names(sortedServices(p, []string{"uses_opt"})), []string{"uses_opt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("点名 uses_opt = %v, 期望 %v", got, want)
	}
}

func TestSelectServicesProfiles(t *testing.T) {
	p := loadYAML(t, `
services:
  app:
    image: x
  debug:
    image: x
    profiles: [debug]
  tools:
    image: x
    profiles: [tools]
    depends_on: [app]
`)
	pick := func(names, profiles []string) []string {
		t.Helper()
		ss, err := selectServices(p, names, profiles)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s.Name
		}
		sort.Strings(out)
		return out
	}
	if got := pick(nil, nil); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("默认只选无 profile 的服务, got %v", got)
	}
	if got := pick(nil, []string{"debug"}); !reflect.DeepEqual(got, []string{"app", "debug"}) {
		t.Errorf("启用 debug, got %v", got)
	}
	if got := pick(nil, []string{"*"}); len(got) != 3 {
		t.Errorf("通配符应启用全部, got %v", got)
	}
	// 显式点名的服务即使带 profile 也会被选中，它的依赖随之带上。
	if got := pick([]string{"tools"}, nil); !reflect.DeepEqual(got, []string{"app", "tools"}) {
		t.Errorf("点名 tools, got %v", got)
	}
	if _, err := selectServices(p, []string{"ghost"}, nil); err == nil || !strings.Contains(err.Error(), "no such service") {
		t.Errorf("未知服务应报错, got %v", err)
	}
}
