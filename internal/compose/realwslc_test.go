//go:build windows

package compose

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// 这组测试驱动**真实的 wslc**，验证假实现替代不了的事：
// 真实的 JSON 字段名、退出码、输出格式、参数是否被 wslc 接受。
//
// 安全规则（违反任何一条都可能伤到用户的环境）：
//   - 只用本机已有的小镜像（alpine:3.20），绝不联网拉取；
//   - 项目名带随机后缀，只会创建/删除带这个前缀的东西；
//   - 结束时一定清理（t.Cleanup），即使测试失败；
//   - 清理只按本测试的项目标签删，绝不碰其他容器、网络、卷。
//
// 没有 wslc、或本地没有 alpine:3.20 时自动跳过，所以在 CI 上是安全的。

// realClient 用真实 wslc 构造 Runtime。这里不能 import internal/wslc（compose 不依赖它），
// 所以直接用 exec 实现一个最小的 Runtime。
type realRuntime struct {
	bin string
	// attachOut 接住 Attach（交互命令）的输出。真实使用时输出直接写到终端，测试里要断言它。
	attachOut *bytes.Buffer
}

func (r realRuntime) Run(ctx context.Context, args ...string) (Result, error) {
	return r.runCmd(ctx, nil, args)
}

// Attach 实现 Runtime：把输出写进 attachOut，而不是终端。
func (r realRuntime) Attach(ctx context.Context, args ...string) (Result, error) {
	return r.runCmd(ctx, r.attachOut, args)
}

// runCmd 运行 wslc。attach 非 nil 时 stdout 写到它里面，Result.Stdout 留空（与真正的 Attach 一致）。
func (r realRuntime) runCmd(ctx context.Context, attach *bytes.Buffer, args []string) (Result, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, r.bin, args...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if attach != nil {
		cmd.Stdout = attach
	}
	err := cmd.Run()
	res := Result{Args: args, Stdout: out.String(), Stderr: errb.String(), Duration: time.Since(start)}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		res.ExitCode = -1
		return res, err
	}
	return res, nil
}

func (r realRuntime) RunStream(ctx context.Context, onLine func(string), args ...string) (Result, error) {
	res, err := r.Run(ctx, args...)
	if err == nil && onLine != nil {
		for _, l := range strings.Split(strings.TrimRight(res.Combined(), "\n"), "\n") {
			if l != "" {
				onLine(l)
			}
		}
	}
	return res, err
}

func (r realRuntime) Follow(ctx context.Context, onLine func(string), args ...string) (Result, error) {
	return r.RunStream(ctx, onLine, args...)
}

const realImage = "alpine:3.20"

// requireRealWslc 找到真实 wslc，并确认本地有测试镜像；否则跳过。
func requireRealWslc(t *testing.T) (realRuntime, *Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("短模式跳过真实 wslc 测试")
	}
	bin := `C:\Program Files\WSL\wslc.exe`
	if _, err := os.Stat(bin); err != nil {
		if p, lerr := exec.LookPath("wslc"); lerr == nil {
			bin = p
		} else {
			t.Skip("没有安装 wslc")
		}
	}
	rt := realRuntime{bin: bin, attachOut: &bytes.Buffer{}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := rt.Run(ctx, "images", "--format", "json")
	if err != nil || !res.Ok() {
		t.Skipf("wslc 不可用: %v %s", err, res.Stderr)
	}
	if !strings.Contains(res.Stdout, `"Repository":"alpine"`) || !strings.Contains(res.Stdout, `"Tag":"3.20"`) {
		t.Skipf("本地没有 %s，为避免联网拉取而跳过", realImage)
	}
	return rt, New(rt)
}

// cleanupProject 兜底清理：按项目标签删除容器，再删除网络和卷。
// 测试通过时 down 已经清理过了，这里只是防止失败的测试留下垃圾。
func cleanupProject(t *testing.T, rt realRuntime, project string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		e := newEngine(rt, nil)
		if list, err := e.listProjectContainers(ctx, project, true); err == nil {
			for _, c := range list {
				_, _ = rt.Run(ctx, "remove", "-f", "-v", c.Name)
			}
		}
		// 网络和卷只删本项目前缀的，且名字必须以 "<project>_" 开头。
		for _, kind := range []string{"network", "volume"} {
			if names, err := e.names(ctx, kind); err == nil {
				for n := range names {
					if strings.HasPrefix(n, project+"_") {
						_, _ = rt.Run(ctx, kind, "remove", n)
					}
				}
			}
		}
	})
}

func uniqueProject(t *testing.T) string {
	return fmt.Sprintf("wcgotest%d", time.Now().UnixNano()%1_000_000_000)
}

// 先做只读探针：用真实 wslc 的输出验证解析代码。不改动任何东西。
func TestRealWslcReadOnlyParsing(t *testing.T) {
	rt, _ := requireRealWslc(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e := newEngine(rt, nil)

	// 镜像：真实输出的字段名是 Repository / Tag。
	if !e.imageExists(ctx, realImage) {
		t.Errorf("imageExists(%s) 应为 true", realImage)
	}
	if e.imageExists(ctx, "definitely-not-an-image:nope") {
		t.Error("不存在的镜像应返回 false")
	}
	// 查询镜像 ENTRYPOINT：alpine 没有 ENTRYPOINT，但镜像存在。
	ep, found := e.imageEntrypoint(ctx, realImage)
	if !found || len(ep) != 0 {
		t.Errorf("alpine 的 entrypoint = %v, found=%v；期望 found 且为空", ep, found)
	}
	if _, found := e.imageEntrypoint(ctx, "definitely-not-an-image:nope"); found {
		t.Error("不存在的镜像不应 found")
	}

	// 网络、卷列表：真实输出是 NDJSON，键是 Name。
	nets, err := e.names(ctx, "network")
	if err != nil {
		t.Fatalf("列网络失败: %v", err)
	}
	if !nets["bridge"] {
		t.Errorf("网络列表里应有内置的 bridge: %v", nets)
	}
	if _, err := e.names(ctx, "volume"); err != nil {
		t.Fatalf("列卷失败: %v", err)
	}

	// run --help：解析出受支持的选项。
	for _, flag := range []string{"--restart", "--cap-add", "--health-cmd", "--network-alias", "--mount"} {
		if !e.supportsRunOption(ctx, flag) {
			t.Errorf("真实 wslc 应支持 %s", flag)
		}
	}
	if e.supportsRunOption(ctx, "--definitely-not-an-option") {
		t.Error("不存在的选项不应被认为受支持")
	}

	// 一个不存在的项目：list 过滤不应报错，只是空。
	list, err := e.listProjectContainers(ctx, "no-such-project-xyz", true)
	if err != nil || len(list) != 0 {
		t.Errorf("不存在的项目应返回空列表: %v %v", list, err)
	}
}

// 真实的端到端：up → 幂等 → ps → 改配置重建 → stop/start → down。
func TestRealWslcLifecycle(t *testing.T) {
	rt, c := requireRealWslc(t)
	project := uniqueProject(t)
	cleanupProject(t, rt, project)

	yaml := fmt.Sprintf(`name: %s
services:
  app:
    image: %s
    command: ["sleep", "600"]
    labels: {purpose: wcgo-test}
    environment: {GREETING: hello}
    volumes: ["data:/data"]
    healthcheck:
      test: ["CMD", "true"]
      interval: 2s
      retries: 2
  worker:
    image: %s
    command: ["sleep", "600"]
    depends_on:
      app:
        condition: service_healthy
volumes:
  data: {}
`, project, realImage, realImage)
	dir := writeProject(t, map[string]string{"compose.yaml": yaml})
	ctx := context.Background()

	var lines []string
	up := func(opt ActionOptions) Result {
		t.Helper()
		lines = nil
		res, err := c.RunActionStream(ctx, dir, ActionUp, opt, func(l string) { lines = append(lines, l) })
		if err != nil {
			t.Fatalf("up: %v", err)
		}
		return res
	}

	// ---- 第一次 up ----
	res := up(ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatalf("up 失败: %v\n%s", res.Error(), strings.Join(lines, "\n"))
	}
	t.Logf("第一次 up 输出:\n%s", strings.Join(lines, "\n"))

	entries, err := c.PS(ctx, dir, ActionOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("ps 应有 2 个容器: %+v", entries)
	}
	for _, e := range entries {
		if !e.Running {
			t.Errorf("%s 应在运行: %+v", e.Name, e)
		}
		if e.ID == "" || e.Image == "" || e.Status == "" {
			t.Errorf("ps 字段不全（真实输出的字段名可能与假设不符）: %+v", e)
		}
	}
	if entries[0].Service != "app" || entries[1].Service != "worker" {
		t.Errorf("ps 应按服务名排序: %+v", entries)
	}

	// ---- 幂等：再来一次不应动任何容器 ----
	res = up(ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatalf("第二次 up 失败: %v", res.Error())
	}
	if n := countContaining(lines, "is up-to-date"); n != 2 {
		t.Errorf("第二次 up 应保留两个容器，输出:\n%s", strings.Join(lines, "\n"))
	}

	// ---- 改配置 → 只重建改动的服务 ----
	changed := strings.Replace(yaml, "GREETING: hello", "GREETING: changed", 1)
	if err := os.WriteFile(dir+`\compose.yaml`, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	res = up(ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatalf("改配置后 up 失败: %v\n%s", res.Error(), strings.Join(lines, "\n"))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Recreating "+project+"-app-1") || strings.Contains(joined, "Recreating "+project+"-worker-1") {
		t.Errorf("只有 app 的配置变了，只应重建 app:\n%s", joined)
	}

	// ---- stop / start ----
	if res, _ := c.RunActionStream(ctx, dir, ActionStop, ActionOptions{Timeout: 2}, nil); !res.Ok() {
		t.Fatalf("stop 失败: %v", res.Error())
	}
	if running, _ := c.PS(ctx, dir, ActionOptions{}); len(running) != 0 {
		t.Errorf("stop 之后不应有运行中的容器: %+v", running)
	}
	if res, _ := c.RunActionStream(ctx, dir, ActionStart, ActionOptions{}, nil); !res.Ok() {
		t.Fatalf("start 失败: %v", res.Error())
	}
	if running, _ := c.PS(ctx, dir, ActionOptions{}); len(running) != 2 {
		t.Errorf("start 之后应有 2 个运行中的容器: %+v", running)
	}

	// ---- exec 与 logs ----
	rt.attachOut.Reset()
	res, err = c.Exec(ctx, dir, ActionOptions{}, "app", []string{"sh", "-c", "echo $GREETING"}, ExecOptions{})
	if err != nil || !res.Ok() || !strings.Contains(rt.attachOut.String(), "changed") {
		t.Errorf("exec 输出 = %q, res=%+v, err=%v（环境变量应是改过之后的值）", rt.attachOut.String(), res, err)
	}
	// exec 要把容器内命令的退出码带回来，脚本才能据此判断成败。
	res, err = c.Exec(ctx, dir, ActionOptions{}, "app", []string{"sh", "-c", "exit 7"}, ExecOptions{})
	if err != nil || res.ExitCode != 7 {
		t.Errorf("exec exit 7 的结果 = %+v, err=%v，期望退出码 7", res, err)
	}

	// ---- down ----
	var downLines []string
	res, err = c.RunActionStream(ctx, dir, ActionDown, ActionOptions{Volumes: true, Timeout: 2}, func(l string) { downLines = append(downLines, l) })
	if err != nil || !res.Ok() {
		t.Fatalf("down 失败: %v %v\n%s", err, res.Error(), strings.Join(downLines, "\n"))
	}
	e := newEngine(rt, nil)
	if left, _ := e.listProjectContainers(ctx, project, true); len(left) != 0 {
		t.Errorf("down 之后不应剩容器: %+v", left)
	}
	for _, kind := range []string{"network", "volume"} {
		names, _ := e.names(ctx, kind)
		for n := range names {
			if strings.HasPrefix(n, project+"_") {
				t.Errorf("down 之后不应剩 %s %s", kind, n)
			}
		}
	}
}

// dry-run 对真实 wslc：会执行只读查询，但不改动任何东西，并给出 wslc 命令预览。
func TestRealWslcDryRun(t *testing.T) {
	rt, c := requireRealWslc(t)
	project := uniqueProject(t)
	cleanupProject(t, rt, project)
	dir := writeProject(t, map[string]string{"compose.yaml": fmt.Sprintf(
		"name: %s\nservices:\n  a:\n    image: %s\n    ports: ['18099:80']\n    volumes: ['cache:/v']\nvolumes:\n  cache: {}\n", project, realImage)})

	out, err := c.DryRun(context.Background(), dir, ActionUp, ActionOptions{Detach: true})
	if err != nil {
		t.Fatalf("dry-run 失败: %v", err)
	}
	for _, want := range []string{"+ wslc network create", "+ wslc volume create", "+ wslc run -d --name " + project + "-a-1", "-p 18099:80"} {
		if !strings.Contains(out, want) {
			t.Errorf("预览缺少 %q:\n%s", want, out)
		}
	}
	e := newEngine(rt, nil)
	if list, _ := e.listProjectContainers(context.Background(), project, true); len(list) != 0 {
		t.Errorf("dry-run 不应创建容器: %+v", list)
	}
	if nets, _ := e.names(context.Background(), "network"); nets[project+"_default"] {
		t.Error("dry-run 不应创建网络")
	}
}
