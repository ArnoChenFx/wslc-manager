package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func hashOf(t *testing.T, p *types.Project, name string) string {
	t.Helper()
	h, err := serviceHash(p, p.Services[name], testEnv)
	if err != nil {
		t.Fatalf("serviceHash: %v", err)
	}
	return h
}

// 指纹的性质：该变的变，不该变的不变。
//
// 为什么用性质而不是黄金值：指纹的具体数值没有意义，重要的是「改了什么会触发重建」。
// 每一行都是一个产品决定：用户改了这个字段，容器要不要重建。
func TestHashProperties(t *testing.T) {
	const base = `
name: demo
services:
  a:
    image: nginx:1
    command: ["run"]
    environment: {A: "1", B: "2"}
    ports: ["8080:80", "9090:90"]
    volumes: ["data:/data", "C:\\x:/x"]
    labels: {k: v}
    depends_on: [b]
    networks: [front]
  b:
    image: busybox
networks:
  front: {}
volumes:
  data: {}
`
	baseHash := hashOf(t, loadYAML(t, base), "a")

	tests := []struct {
		name    string
		yaml    string
		changed bool
	}{
		{"完全相同", base, false},
		{"环境变量换顺序", strings.Replace(base, `{A: "1", B: "2"}`, `{B: "2", A: "1"}`, 1), false},
		{"端口换顺序", strings.Replace(base, `["8080:80", "9090:90"]`, `["9090:90", "8080:80"]`, 1), false},
		{"依赖关系变了（只影响启动顺序）", strings.Replace(base, "depends_on: [b]", "depends_on: []", 1), false},
		{"副本数变了（单独处理）", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    deploy: {replicas: 3}", 1), false},
		{"profile 变了", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    profiles: [x]", 1), false},
		{"镜像变了", strings.Replace(base, "nginx:1", "nginx:2", 1), true},
		{"命令变了", strings.Replace(base, `["run"]`, `["run", "-v"]`, 1), true},
		{"环境变量值变了", strings.Replace(base, `A: "1"`, `A: "9"`, 1), true},
		{"新增端口", strings.Replace(base, `"9090:90"]`, `"9090:90", "7070:70"]`, 1), true},
		{"挂载只读变了", strings.Replace(base, `"data:/data"`, `"data:/data:ro"`, 1), true},
		{"标签变了", strings.Replace(base, "{k: v}", "{k: w}", 1), true},
		{"新增 restart: always", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    restart: always", 1), true},
		{"新增 cap_add", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    cap_add: [NET_ADMIN]", 1), true},
		{"新增 extra_hosts", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    extra_hosts: ['h:10.0.0.1']", 1), true},
		{"新增健康检查", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    healthcheck: {test: [CMD, 'true']}", 1), true},
		{"网络换成 internal", strings.Replace(base, "front: {}", "front: {internal: true}", 1), true},
		{"卷换了 driver", strings.Replace(base, "  data: {}", "  data: {driver: local}", 1), true},
		{"command 显式清空", strings.Replace(base, `command: ["run"]`, "command: []", 1), true},
		// wslc 不认的字段不会进入命令行，改它们不该让容器重建。
		{"privileged（被忽略）", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    privileged: true", 1), false},
		{"restart: unless-stopped（被忽略）", strings.Replace(base, "    depends_on: [b]", "    depends_on: [b]\n    restart: unless-stopped", 1), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hashOf(t, loadYAML(t, tc.yaml), "a")
			if changed := got != baseHash; changed != tc.changed {
				t.Errorf("指纹变化 = %v, 期望 %v", changed, tc.changed)
			}
		})
	}
}

// 指纹格式：带版本前缀，固定长度；同一份配置每次结果相同。
func TestHashFormatAndStability(t *testing.T) {
	p := loadYAML(t, "name: demo\nservices:\n  a:\n    image: x\n    environment: {A: '1', B: '2', C: '3'}\n")
	first := hashOf(t, p, "a")
	if !strings.HasPrefix(first, "g1:") || len(first) != len("g1:")+32 {
		t.Errorf("指纹格式不对: %q", first)
	}
	// map 遍历顺序是随机的，多算几次才能暴露「指纹依赖遍历顺序」的缺陷。
	for i := 0; i < 50; i++ {
		if got := hashOf(t, p, "a"); got != first {
			t.Fatalf("第 %d 次指纹不同: %s vs %s", i, got, first)
		}
	}
}

// secrets / configs 文件的内容变了，容器要重建（只看路径会漏掉这种情况）。
func TestHashCoversFileContents(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"compose.yaml": "secrets:\n  s: {file: ./s.txt}\nservices:\n  a:\n    image: x\n    secrets: [s]\n",
		"s.txt":        "v1",
	})
	load := func() *types.Project { return mustLoadDir(t, dir) }

	before := hashOf(t, load(), "a")
	if again := hashOf(t, load(), "a"); again != before {
		t.Fatal("内容没变，指纹不应变")
	}
	if err := os.WriteFile(filepath.Join(dir, "s.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := hashOf(t, load(), "a"); after == before {
		t.Error("secret 文件内容变了，指纹应该变")
	}
}

// env_file 的内容由 compose-go 并入 environment，所以改文件内容自然改变指纹。
func TestHashCoversEnvFileContents(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"compose.yaml": "services:\n  a:\n    image: x\n    env_file: [e.env]\n",
		"e.env":        "A=1\n",
	})
	before := hashOf(t, mustLoadDir(t, dir), "a")
	if err := os.WriteFile(filepath.Join(dir, "e.env"), []byte("A=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := hashOf(t, mustLoadDir(t, dir), "a"); after == before {
		t.Error("env_file 内容变了，指纹应该变")
	}
}

// 构建设置变了要重建（镜像不会自动重建，但构建参数属于容器的「来源」）。
func TestHashCoversBuildSettings(t *testing.T) {
	mk := func(extra string) string {
		return "name: demo\nservices:\n  a:\n    build:\n      context: .\n" + extra
	}
	base := hashOf(t, loadYAML(t, mk("")), "a")
	if hashOf(t, loadYAML(t, mk("      target: prod\n")), "a") == base {
		t.Error("build.target 变了，指纹应该变")
	}
	if hashOf(t, loadYAML(t, mk("      args: {X: '1'}\n")), "a") == base {
		t.Error("build.args 变了，指纹应该变")
	}
}

func mustLoadDir(t *testing.T, dir string) *types.Project {
	t.Helper()
	p, err := loadProject(t.Context(), LoadOptions{Dir: dir, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
