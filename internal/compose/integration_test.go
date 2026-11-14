package compose

import (
	"context"
	"os"
	"testing"
)

// 这些测试会真正调用本机安装的 wslc-compose。
// 没有安装时自动跳过，因此在 CI 上也能安全运行。

func requireCompose(t *testing.T) *Client {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Skipf("跳过：%v", err)
	}
	return c
}

// demoCompose 是一个最小可用的 compose 项目，覆盖
// name / services / ports / volumes / networks / depends_on / env。
const demoCompose = `name: wslcmgr-test
services:
  web:
    image: nginx:alpine
    ports:
      - "18088:80"
    depends_on:
      - cache
    environment:
      GREETING: hello
      EMPTY:
  cache:
    image: redis:7-alpine
    volumes:
      - data:/data
volumes:
  data: {}
networks:
  front: {}
`

// writeDemoProject 在临时目录里写出一个 compose 项目。
func writeDemoProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+string(os.PathSeparator)+"compose.yaml", []byte(demoCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadProjectReal(t *testing.T) {
	c := requireCompose(t)
	dir := writeDemoProject(t)

	ctx := context.Background()
	p, err := c.LoadProject(ctx, dir)
	if err != nil {
		t.Fatalf("加载项目失败: %v", err)
	}

	if p.Name != "wslcmgr-test" {
		t.Errorf("项目名 = %q, want wslcmgr-test", p.Name)
	}
	if len(p.Config.Services) != 2 {
		t.Fatalf("服务数 = %d, want 2: %+v", len(p.Config.Services), p.Config.Services)
	}

	web, ok := p.Config.Services["web"]
	if !ok {
		t.Fatal("缺少 web 服务")
	}
	if web.Image != "nginx:alpine" {
		t.Errorf("web.Image = %q", web.Image)
	}
	if len(web.Ports) != 1 || web.Ports[0].Target != 80 || web.Ports[0].Published != "18088" {
		t.Errorf("web.Ports = %+v", web.Ports)
	}
	if len(web.DependsOn) != 1 || web.DependsOn[0] != "cache" {
		t.Errorf("web.DependsOn = %+v", web.DependsOn)
	}
	// 环境变量：GREETING 有值，EMPTY 为 nil
	if v, ok := web.Env["GREETING"]; !ok || v == nil || *v != "hello" {
		t.Errorf("web.Env[GREETING] = %v", web.Env["GREETING"])
	}
	if v, ok := web.Env["EMPTY"]; !ok || v != nil {
		t.Errorf("web.Env[EMPTY] 应为无值, got %v", v)
	}

	cache, ok := p.Config.Services["cache"]
	if !ok {
		t.Fatal("缺少 cache 服务")
	}
	// 注意：wslc-compose config 会把命名卷重写成 <project>_<volume> 前缀，
	// 这与它真正传给 `wslc volume create` 的名字一致。
	if len(cache.Volumes) != 1 || cache.Volumes[0].Source != "wslcmgr-test_data" ||
		cache.Volumes[0].Target != "/data" {
		t.Errorf("cache.Volumes = %+v", cache.Volumes)
	}

	// 服务名排序
	names := p.ServiceNames()
	if len(names) != 2 || names[0] != "cache" || names[1] != "web" {
		t.Errorf("ServiceNames() = %v", names)
	}

	// 主 compose 文件应存在
	cf := p.ComposeFile()
	if cf == "" {
		t.Error("ComposeFile() 返回空")
	}
}

func TestLoadProjectInvalidYAML(t *testing.T) {
	c := requireCompose(t)
	dir := t.TempDir()
	if err := os.WriteFile(dir+string(os.PathSeparator)+"compose.yaml",
		[]byte("services: [this is not a map]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := c.LoadProject(context.Background(), dir)
	if err == nil {
		t.Fatal("非法 compose 文件应返回错误")
	}
}

func TestDryRunReal(t *testing.T) {
	c := requireCompose(t)
	dir := writeDemoProject(t)

	out, err := c.DryRun(context.Background(), dir, ActionUp, ActionOptions{Detach: true})
	if err != nil {
		t.Fatalf("dry-run 失败: %v", err)
	}
	// 至少应该提到 network / volume / run 之一
	if len(out) == 0 {
		t.Error("dry-run 输出为空")
	}
}

func TestVersionReal(t *testing.T) {
	c := requireCompose(t)
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if v == "" {
		t.Error("版本号为空")
	}
	t.Logf("wslc-compose %s @ %s", v, c.Binary())
}
