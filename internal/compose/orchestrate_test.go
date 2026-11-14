package compose

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 一个典型的两服务项目：web 依赖 db，带命名卷。
const twoServices = `
name: demo
services:
  web:
    image: nginx:alpine
    ports: ["8080:80"]
    depends_on: [db]
  db:
    image: redis:7
    volumes: ["data:/data"]
volumes:
  data: {}
`

// newTestClient 用假 wslc 构造客户端，并把项目文件写进临时目录。
func newTestClient(t *testing.T, yaml string, extra ...map[string]string) (*Client, *fakeWslc, string) {
	t.Helper()
	files := map[string]string{"compose.yaml": yaml}
	for _, m := range extra {
		for k, v := range m {
			files[k] = v
		}
	}
	dir := writeProject(t, files)
	f := newFake(t)
	f.addImage("nginx:alpine")
	f.addImage("redis:7")
	f.addImage("busybox")
	f.addImage("alpine:3.20")
	return New(f), f, dir
}

// 动作失败时 Result.ExitCode 非零；error 只表示没能开始。这个约定被界面依赖。
func runAction(t *testing.T, c *Client, dir string, a Action, o ActionOptions) (Result, []string) {
	t.Helper()
	var lines []string
	res, err := c.RunActionStream(context.Background(), dir, a, o, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("%s: 意外的 error: %v", a, err)
	}
	return res, lines
}

func TestUpCreatesEverythingInOrder(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	res, lines := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatalf("up 失败: %v\n%s", res.Error(), strings.Join(lines, "\n"))
	}

	// 网络 → 卷 → 被依赖的 db → web。
	order := []string{"network create", "volume create", "run -d --name demo-db-1", "run -d --name demo-web-1"}
	last := -1
	for _, prefix := range order {
		i := f.find(prefix)
		if i < 0 {
			t.Fatalf("缺少命令 %q:\n%s", prefix, strings.Join(f.commands(), "\n"))
		}
		if i < last {
			t.Errorf("%q 的顺序不对，应在前一步之后", prefix)
		}
		last = i
	}
	for _, name := range []string{"demo-db-1", "demo-web-1"} {
		if _, running := f.has(name); !running {
			t.Errorf("%s 应在运行", name)
		}
	}
	if !f.volumes["demo_data"] {
		t.Error("命名卷应带项目前缀 demo_data")
	}
	if _, ok := f.networks["demo_default"]; !ok {
		t.Error("应创建默认网络 demo_default")
	}
	// 网络上要有归属标签，down 靠它确认「这是我建的」。
	if got := f.networks["demo_default"]["Labels"].(map[string]any)[LabelProject]; got != "demo" {
		t.Errorf("网络归属标签 = %v", got)
	}
}

// 幂等：配置没变时再次 up 不应改动任何容器。
func TestUpIsIdempotent(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	before := mutations(f)

	res, lines := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatal(res.Error())
	}
	after := mutations(f)
	if after != before {
		t.Errorf("第二次 up 不应创建/删除/启动/停止任何容器，新增了 %d 条命令:\n%s", after-before, strings.Join(f.commands(), "\n"))
	}
	if n := countContaining(lines, "is up-to-date"); n != 2 {
		t.Errorf("应提示两个容器 up-to-date, got %d:\n%s", n, strings.Join(lines, "\n"))
	}
}

func countContaining(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// 配置变了要重建：先停止、删除旧容器，再创建新的。
func TestUpRecreatesChangedService(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})

	changed := strings.Replace(twoServices, "8080:80", "9090:80", 1)
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	mark := len(f.commands())
	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatal(res.Error())
	}
	tail := strings.Join(f.commands()[mark:], "\n")
	for _, want := range []string{"stop -t 10 demo-web-1", "remove -f demo-web-1", "run -d --name demo-web-1"} {
		if !strings.Contains(tail, want) {
			t.Errorf("重建流程缺少 %q:\n%s", want, tail)
		}
	}
	if strings.Contains(tail, "demo-db-1") {
		t.Errorf("没变的 db 不应被动：\n%s", tail)
	}
}

// 已停止但配置没变的容器：直接 start，而不是删了重建。
func TestUpStartsStoppedUnchangedContainer(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	runAction(t, c, dir, ActionStop, ActionOptions{})
	mark := len(f.commands())

	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatal(res.Error())
	}
	tail := strings.Join(f.commands()[mark:], "\n")
	if strings.Contains(tail, "run -d") || strings.Contains(tail, "remove") {
		t.Errorf("不应重建：\n%s", tail)
	}
	if !strings.Contains(tail, "start demo-db-1") || !strings.Contains(tail, "start demo-web-1") {
		t.Errorf("应启动已停止的容器：\n%s", tail)
	}
}

func TestUpForceRecreate(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	mark := len(f.commands())
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, ForceRecreate: true})
	if tail := strings.Join(f.commands()[mark:], "\n"); !strings.Contains(tail, "run -d --name demo-web-1") {
		t.Errorf("--force-recreate 应重建：\n%s", tail)
	}
}

// 点名服务时带上它的依赖，但不碰无关的服务。
func TestUpWithServiceSelection(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices+"  # 加一个无关服务\n")
	yaml := strings.Replace(twoServices, "volumes:\n  data: {}", "  other:\n    image: busybox\nvolumes:\n  data: {}", 1)
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, Services: []string{"web"}})
	if !res.Ok() {
		t.Fatal(res.Error())
	}
	if e, _ := f.has("demo-db-1"); !e {
		t.Error("web 的依赖 db 应被带起来")
	}
	if e, _ := f.has("demo-other-1"); e {
		t.Error("没被选中的 other 不应启动")
	}
}

// profile：带 profile 的服务默认不启动，点名或启用后才启动。
func TestUpProfiles(t *testing.T) {
	yaml := "name: demo\nservices:\n  app:\n    image: alpine:3.20\n  dbg:\n    image: busybox\n    profiles: [debug]\n"
	c, f, dir := newTestClient(t, yaml)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if e, _ := f.has("demo-dbg-1"); e {
		t.Error("默认不应启动带 profile 的服务")
	}
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, Profiles: []string{"debug"}})
	if e, _ := f.has("demo-dbg-1"); !e {
		t.Error("启用 profile 后应启动")
	}
}

// 副本数：scale 覆盖，缩小时删除多余的容器。
func TestUpScale(t *testing.T) {
	yaml := "name: demo\nservices:\n  w:\n    image: alpine:3.20\n"
	c, f, dir := newTestClient(t, yaml)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, Scale: map[string]int{"w": 3}})
	for _, n := range []string{"demo-w-1", "demo-w-2", "demo-w-3"} {
		if e, _ := f.has(n); !e {
			t.Errorf("缺少副本 %s", n)
		}
	}
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, Scale: map[string]int{"w": 1}})
	if e, _ := f.has("demo-w-2"); e {
		t.Error("缩容后 demo-w-2 应被删除")
	}
	if e, _ := f.has("demo-w-1"); !e {
		t.Error("demo-w-1 应保留")
	}
}

func TestUpRejectsBadScale(t *testing.T) {
	yaml := "name: demo\nservices:\n  w:\n    image: alpine:3.20\n    container_name: fixed\n"
	c, f, dir := newTestClient(t, yaml)
	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, Scale: map[string]int{"w": 2}})
	if res.Ok() || !strings.Contains(res.Stderr, "cannot scale a service with container_name set") {
		t.Errorf("应拒绝给固定名字的服务扩容: %+v", res)
	}
	if f.count("run ") != 0 || f.count("network create") != 0 {
		t.Error("校验失败时不应创建任何东西")
	}
}

// 失败不连坐：一个服务起不来，只跳过依赖它的服务，其余照常启动，整体报错。
func TestUpFailureDoesNotCascade(t *testing.T) {
	yaml := `
name: demo
services:
  bad:
    image: alpine:3.20
  child:
    image: alpine:3.20
    depends_on: [bad]
  good:
    image: alpine:3.20
`
	c, f, dir := newTestClient(t, yaml)
	f.failOn = func(args []string) string {
		if args[0] == "run" && strings.Contains(strings.Join(args, " "), "--name demo-bad-1") {
			return "port is already allocated"
		}
		return ""
	}
	res, lines := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if res.Ok() {
		t.Fatal("有服务失败时 up 应报错")
	}
	if e, _ := f.has("demo-good-1"); !e {
		t.Error("无关的 good 仍应启动")
	}
	if e, _ := f.has("demo-child-1"); e {
		t.Error("依赖失败服务的 child 应被跳过")
	}
	if !strings.Contains(res.Stderr, "2 services failed to start") ||
		!strings.Contains(res.Stderr, "bad (") || !strings.Contains(res.Stderr, "child (dependency 'bad' failed to start)") {
		t.Errorf("汇总错误不对: %s", res.Stderr)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Skipping child") {
		t.Errorf("应提示跳过 child:\n%s", strings.Join(lines, "\n"))
	}
}

// required:false 的依赖失败只告警，不阻塞下游。
func TestUpOptionalDependencyFailureDoesNotBlock(t *testing.T) {
	yaml := `
name: demo
services:
  opt:
    image: alpine:3.20
  app:
    image: alpine:3.20
    depends_on:
      opt:
        condition: service_started
        required: false
`
	c, f, dir := newTestClient(t, yaml)
	f.failOn = func(args []string) string {
		if args[0] == "run" && strings.Contains(strings.Join(args, " "), "--name demo-opt-1") {
			return "boom"
		}
		return ""
	}
	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if e, _ := f.has("demo-app-1"); !e {
		t.Error("可选依赖失败不应阻塞 app")
	}
	if res.Ok() {
		t.Error("opt 失败，整体仍应报错")
	}
}

// service_healthy：等依赖变健康后才启动下游。
func TestUpWaitsForHealthyDependency(t *testing.T) {
	yaml := `
name: demo
services:
  db:
    image: redis:7
    healthcheck: {test: [CMD, 'true']}
  web:
    image: nginx:alpine
    depends_on:
      db:
        condition: service_healthy
`
	c, f, dir := newTestClient(t, yaml)
	polls := 0
	f.onCommand = func(f *fakeWslc, args []string) {
		// 前两次询问 db 时还在 starting，第三次变成 healthy。
		if args[0] == "inspect" && len(args) > 1 {
			if ex, _ := f.has("demo-db-1"); ex {
				polls++
				if polls < 3 {
					f.setHealth("demo-db-1", "starting")
				} else {
					f.setHealth("demo-db-1", "healthy")
				}
			}
		}
	}
	// 让第一次 run（创建 db）时容器初始为 starting。
	res, lines := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatalf("up 失败: %v\n%s", res.Error(), strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Waiting for db to be healthy") {
		t.Errorf("应提示正在等待:\n%s", strings.Join(lines, "\n"))
	}
	if f.find("run -d --name demo-web-1") < f.find("run -d --name demo-db-1") {
		t.Error("web 必须在 db 之后启动")
	}
}

func TestUpFailsWhenDependencyUnhealthy(t *testing.T) {
	yaml := `
name: demo
services:
  db:
    image: redis:7
    healthcheck: {test: [CMD, 'true']}
  web:
    image: nginx:alpine
    depends_on:
      db:
        condition: service_healthy
`
	c, f, dir := newTestClient(t, yaml)
	f.onCommand = func(f *fakeWslc, args []string) {
		if args[0] == "inspect" {
			f.setHealth("demo-db-1", "unhealthy")
		}
	}
	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if res.Ok() || !strings.Contains(res.Stderr, "web (dependency 'db' is unhealthy") {
		t.Errorf("应因依赖不健康而跳过 web: %+v", res)
	}
	if e, _ := f.has("demo-web-1"); e {
		t.Error("不健康的依赖之后，web 不应启动")
	}
}

func TestUpWaitsForCompletedDependency(t *testing.T) {
	yaml := `
name: demo
services:
  migrate:
    image: alpine:3.20
  app:
    image: nginx:alpine
    depends_on:
      migrate:
        condition: service_completed_successfully
`
	for _, tc := range []struct {
		name     string
		exitCode int
		wantOK   bool
	}{{"成功退出", 0, true}, {"失败退出", 3, false}} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, dir := newTestClient(t, yaml)
			f.onCommand = func(f *fakeWslc, args []string) {
				if args[0] == "inspect" {
					f.markExited("demo-migrate-1", tc.exitCode)
				}
			}
			res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
			if res.Ok() != tc.wantOK {
				t.Errorf("up 结果 = %v, 期望 %v (%s)", res.Ok(), tc.wantOK, res.Stderr)
			}
			if e, _ := f.has("demo-app-1"); e != tc.wantOK {
				t.Errorf("app 是否启动 = %v, 期望 %v", e, tc.wantOK)
			}
		})
	}
}

func TestEvaluateCondition(t *testing.T) {
	mk := func(running bool, status string, exit float64, health string) map[string]any {
		state := map[string]any{"Running": running, "Status": status, "ExitCode": exit}
		if health != "" {
			state["Health"] = map[string]any{"Status": health}
		}
		return map[string]any{"Name": "/c", "State": state}
	}
	tests := []struct {
		name      string
		cond      string
		data      map[string]any
		satisfied bool
		errPart   string
	}{
		{"healthy", "service_healthy", mk(true, "running", 0, "healthy"), true, ""},
		{"starting 继续等", "service_healthy", mk(true, "running", 0, "starting"), false, ""},
		{"unhealthy 报错", "service_healthy", mk(true, "running", 0, "unhealthy"), false, "is unhealthy"},
		{"没有健康检查", "service_healthy", mk(true, "running", 0, ""), false, "has no healthcheck"},
		{"没在运行", "service_healthy", mk(false, "exited", 1, ""), false, "is not running"},
		{"completed：已成功退出", "service_completed_successfully", mk(false, "exited", 0, ""), true, ""},
		{"completed：还在运行", "service_completed_successfully", mk(true, "running", 0, ""), false, ""},
		{"completed：刚创建", "service_completed_successfully", mk(false, "created", 0, ""), false, ""},
		{"completed：失败退出", "service_completed_successfully", mk(false, "exited", 2, ""), false, "exited with code 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := evaluateCondition(tc.cond, "dep", tc.data)
			if ok != tc.satisfied {
				t.Errorf("satisfied = %v, 期望 %v", ok, tc.satisfied)
			}
			if (err != nil) != (tc.errPart != "") || (err != nil && !strings.Contains(err.Error(), tc.errPart)) {
				t.Errorf("err = %v, 期望包含 %q", err, tc.errPart)
			}
		})
	}
}

// 等待依赖有超时：永远 starting 的依赖不能把 up 卡死。
func TestWaitTimeout(t *testing.T) {
	f := newFake(t)
	e, _ := fastEngine(f)
	f.networks["demo_default"] = map[string]any{}
	f.create([]string{"run", "--name", "demo-db-1", "-l", LabelProject + "=demo", "-l", LabelService + "=db"}, true)
	f.containers["demo-db-1"].health = "starting"

	// 用一个会推进的假时钟太绕；这里直接让 sleep 真的耗掉一点时间，超时设得很短。
	e.sleep = func(ctx context.Context, d time.Duration) error { time.Sleep(15 * time.Millisecond); return ctx.Err() }
	err := e.waitOne(context.Background(), "demo", "db", "service_healthy", 40*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("应超时: %v", err)
	}
}

func TestWaitForMissingDependencyContainer(t *testing.T) {
	f := newFake(t)
	e, _ := fastEngine(f)
	err := e.waitOne(context.Background(), "demo", "ghost", "service_healthy", 0)
	if err == nil || !strings.Contains(err.Error(), "has no container to wait for") {
		t.Errorf("err = %v", err)
	}
}

// 多网络服务：create → connect 次要网络 → start，次要网络必须在启动之前接入。
func TestUpMultiNetworkOrdering(t *testing.T) {
	yaml := `
name: demo
services:
  web:
    image: nginx:alpine
    networks:
      front: {priority: 10}
      back: {}
networks:
  front: {}
  back: {}
`
	c, f, dir := newTestClient(t, yaml)
	res, lines := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatalf("up 失败: %v\n%s", res.Error(), strings.Join(lines, "\n"))
	}
	create, connect, start := f.find("create --name demo-web-1"), f.find("network connect"), f.find("start ")
	if create < 0 || connect < 0 || start < 0 || !(create < connect && connect < start) {
		t.Errorf("顺序应为 create → connect → start:\n%s", strings.Join(f.commands(), "\n"))
	}
	if !strings.Contains(f.commands()[connect], "demo_back") {
		t.Errorf("次要网络应是 demo_back: %s", f.commands()[connect])
	}
}

// 多网络容器启动失败时，只清理本次创建的半成品，并报告原始错误。
func TestUpMultiNetworkCleansUpPartialContainer(t *testing.T) {
	yaml := "name: demo\nservices:\n  web:\n    image: nginx:alpine\n    networks: [front, back]\nnetworks:\n  front: {}\n  back: {}\n"
	c, f, dir := newTestClient(t, yaml)
	f.failOn = func(args []string) string {
		if args[0] == "start" {
			return "ERROR_FATAL: cannot start"
		}
		return ""
	}
	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if res.Ok() {
		t.Fatal("start 失败时 up 应报错")
	}
	if e, _ := f.has("demo-web-1"); e {
		t.Error("半成品容器应被清理")
	}
	if f.find("remove -f -v ") < 0 {
		t.Errorf("清理应使用 remove -f -v:\n%s", strings.Join(f.commands(), "\n"))
	}
}

// 回归保护：清理只能动「本次操作创建的」容器。同名的别人的容器绝不能被误删。
func TestCreateAndStartDoesNotRemoveForeignContainer(t *testing.T) {
	yaml := "name: demo\nservices:\n  web:\n    image: nginx:alpine\n    networks: [front, back]\nnetworks:\n  front: {}\n  back: {}\n"
	c, f, dir := newTestClient(t, yaml)
	// 容器名已被一个不属于本项目的容器占用：create 会失败，清理时不能把它删掉。
	f.networks["demo_front"] = map[string]any{"Name": "demo_front", "Labels": map[string]any{}}
	f.networks["demo_back"] = map[string]any{"Name": "demo_back", "Labels": map[string]any{}}
	f.create([]string{"run", "--name", "demo-web-1", "-l", "owner=someone-else"}, true)
	foreignID := f.containers["demo-web-1"].id

	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if res.Ok() {
		t.Fatal("名字冲突时 up 应失败")
	}
	got := f.containers["demo-web-1"]
	if got == nil || got.id != foreignID {
		t.Error("别人的同名容器被误删了")
	}
}

// 暂时性错误会重试，持续的错误不会无限重试。
func TestStartRetriesTransientErrors(t *testing.T) {
	f := newFake(t)
	e, out := fastEngine(f)
	f.create([]string{"run", "--name", "c1"}, false)
	attempts := 0
	f.failOn = func(args []string) string {
		if args[0] == "start" {
			attempts++
			if attempts < 3 {
				return "ERROR_SHARING_VIOLATION"
			}
		}
		return ""
	}
	if _, err := e.runRetried(context.Background(), "start", "c1"); err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if attempts != 3 {
		t.Errorf("尝试次数 = %d, 期望 3", attempts)
	}
	if !strings.Contains(strings.Join(*out, "\n"), "transient error") {
		t.Errorf("应提示正在重试: %v", *out)
	}

	attempts = 0
	f.failOn = func(args []string) string { attempts++; return "ERROR_ALREADY_EXISTS" }
	if _, err := e.runRetried(context.Background(), "start", "c1"); err == nil {
		t.Error("一直失败时最终应返回错误")
	}
	if attempts != transientRetries {
		t.Errorf("总尝试次数 = %d, 期望 %d", attempts, transientRetries)
	}

	// 非暂时性错误不重试。
	attempts = 0
	f.failOn = func(args []string) string { attempts++; return "some permanent failure" }
	e.runRetried(context.Background(), "start", "c1")
	if attempts != 1 {
		t.Errorf("永久性错误不应重试, 尝试了 %d 次", attempts)
	}
}

func TestDownRemovesContainersAndNetworks(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	res, lines := runAction(t, c, dir, ActionDown, ActionOptions{})
	if !res.Ok() {
		t.Fatalf("down 失败: %v", res.Error())
	}
	if len(f.containers) != 0 {
		t.Errorf("容器应全部删除: %v", f.order)
	}
	if _, ok := f.networks["demo_default"]; ok {
		t.Error("项目网络应被删除")
	}
	if !f.volumes["demo_data"] {
		t.Error("没带 -v 时不应删除卷")
	}
	if f.find("stop -t 10 ") < 0 {
		t.Errorf("运行中的容器应先停止:\n%s", strings.Join(lines, "\n"))
	}
}

func TestDownRemovesVolumesOnRequest(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	runAction(t, c, dir, ActionDown, ActionOptions{Volumes: true})
	if f.volumes["demo_data"] {
		t.Error("-v 应删除命名卷")
	}
}

// down 删的是项目名下的所有容器，包括 compose 文件里已经删掉的服务留下的。
func TestDownRemovesOrphans(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	f.create([]string{"run", "--name", "demo-old-1", "-l", LabelProject + "=demo", "-l", LabelService + "=old"}, true)

	runAction(t, c, dir, ActionDown, ActionOptions{})
	if e, _ := f.has("demo-old-1"); e {
		t.Error("已从 compose 文件删除的服务留下的容器也应被 down 清掉")
	}
}

// 回归保护：网络的项目标签对不上时，说明它不是我们建的，down 必须保留它。
func TestDownPreservesForeignNetwork(t *testing.T) {
	c, f, dir := newTestClient(t, "name: demo\nservices:\n  a:\n    image: alpine:3.20\n")
	f.networks["demo_default"] = map[string]any{"Name": "demo_default", "Labels": map[string]any{LabelProject: "someone-else"}}
	res, lines := runAction(t, c, dir, ActionDown, ActionOptions{})
	if !res.Ok() {
		t.Fatal(res.Error())
	}
	if _, ok := f.networks["demo_default"]; !ok {
		t.Error("归属不明的网络不应被删除")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "preserving network demo_default") {
		t.Errorf("应提示保留了网络:\n%s", strings.Join(lines, "\n"))
	}
}

func TestStopStartRestart(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})

	res, _ := runAction(t, c, dir, ActionStop, ActionOptions{})
	if !res.Ok() {
		t.Fatal(res.Error())
	}
	for _, n := range []string{"demo-db-1", "demo-web-1"} {
		if _, running := f.has(n); running {
			t.Errorf("%s 应已停止", n)
		}
	}
	runAction(t, c, dir, ActionStart, ActionOptions{Services: []string{"web"}})
	if _, running := f.has("demo-web-1"); !running {
		t.Error("web 应被启动")
	}
	if _, running := f.has("demo-db-1"); running {
		t.Error("只点名了 web，db 不应启动")
	}
	mark := len(f.commands())
	runAction(t, c, dir, ActionRestart, ActionOptions{Services: []string{"web"}})
	tail := strings.Join(f.commands()[mark:], "\n")
	if si, st := strings.Index(tail, "stop -t 10 demo-web-1"), strings.Index(tail, "start demo-web-1"); si < 0 || st < 0 || si > st {
		t.Errorf("restart 应先 stop 再 start:\n%s", tail)
	}
}

func TestLifecycleWithoutContainersFails(t *testing.T) {
	c, _, dir := newTestClient(t, twoServices)
	res, _ := runAction(t, c, dir, ActionStop, ActionOptions{})
	if res.Ok() || !strings.Contains(res.Stderr, "no containers found") {
		t.Errorf("没有容器时应报错: %+v", res)
	}
}

func TestBuildAndPull(t *testing.T) {
	yaml := "name: demo\nservices:\n  api:\n    build: .\n  web:\n    image: nginx:alpine\n"
	c, f, dir := newTestClient(t, yaml)

	res, _ := runAction(t, c, dir, ActionBuild, ActionOptions{NoCache: true})
	if !res.Ok() || f.find("build -t demo-api") < 0 || !strings.Contains(f.commands()[f.find("build ")], "--no-cache") {
		t.Errorf("build 不对: %+v\n%s", res, strings.Join(f.commands(), "\n"))
	}
	if f.count("build ") != 1 {
		t.Error("只有 api 带 build 段，只应构建一次")
	}

	res, _ = runAction(t, c, dir, ActionPull, ActionOptions{})
	if !res.Ok() || f.find("pull nginx:alpine") < 0 {
		t.Errorf("pull 不对: %+v", res)
	}
	if f.find("pull demo-api") >= 0 {
		t.Error("只写了 build 的服务没有镜像可拉")
	}

	// 一个带 build 的服务都没有：报错。
	c2, _, dir2 := newTestClient(t, "services:\n  a:\n    image: alpine:3.20\n")
	if res, _ := runAction(t, c2, dir2, ActionBuild, ActionOptions{}); res.Ok() || !strings.Contains(res.Stderr, "no services with a build section") {
		t.Errorf("没有 build 段时应报错: %+v", res)
	}
}

// up 在目标镜像不存在时自动构建，已存在时不构建（除非 --build）。
func TestUpBuildsMissingImageOnly(t *testing.T) {
	yaml := "name: demo\nservices:\n  api:\n    build: .\n"
	c, f, dir := newTestClient(t, yaml)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if f.count("build ") != 1 {
		t.Errorf("镜像不存在应构建一次, got %d", f.count("build "))
	}
	runAction(t, c, dir, ActionDown, ActionOptions{})
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if f.count("build ") != 1 {
		t.Error("镜像已存在时不应再构建")
	}
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, Build: true})
	if f.count("build ") != 2 {
		t.Error("--build 应强制重新构建")
	}
}

// dry-run：打印将要执行的命令，但不改动任何东西；查询类命令照常执行。
func TestDryRunChangesNothing(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	out, err := c.DryRun(context.Background(), dir, ActionUp, ActionOptions{Detach: true})
	if err != nil {
		t.Fatalf("dry-run 失败: %v", err)
	}
	for _, want := range []string{"+ wslc network create", "+ wslc volume create", "+ wslc run -d --name demo-db-1", "+ wslc run -d --name demo-web-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("预览缺少 %q:\n%s", want, out)
		}
	}
	if len(f.containers) != 0 || len(f.networks) != 0 || len(f.volumes) != 0 {
		t.Error("dry-run 不应改动任何东西")
	}
	for _, cmd := range f.commands() {
		if !isReadOnly(cmd) {
			t.Errorf("dry-run 中真正执行了会改动状态的命令: %s", cmd)
		}
	}
}

func isReadOnly(cmd string) bool {
	for _, p := range []string{"list ", "network list", "volume list", "images ", "inspect ", "image inspect", "network inspect", "run --help"} {
		if strings.HasPrefix(cmd, p) {
			return true
		}
	}
	return false
}

// 旧版 wslc 不支持 --restart / --cap-add：不传这些选项，并给出明确提示，而不是让命令整个失败。
func TestUpOnOldWslcWarnsAndSkipsOptions(t *testing.T) {
	yaml := "name: demo\nservices:\n  a:\n    image: alpine:3.20\n    restart: always\n    cap_add: [NET_ADMIN]\n"
	c, f, dir := newTestClient(t, yaml)
	f.runHelp = "--name --detach" // 没有 --restart / --cap-add
	res, lines := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !res.Ok() {
		t.Fatal(res.Error())
	}
	cmd := f.commands()[f.find("run -d")]
	if strings.Contains(cmd, "--restart") || strings.Contains(cmd, "--cap-add") {
		t.Errorf("不应向旧版 wslc 传不支持的选项: %s", cmd)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "'restart: always' needs wslc >= 3.0.5") || !strings.Contains(joined, "cap_add/cap_drop need wslc >= 3.0.5") {
		t.Errorf("应提示需要更新 wslc:\n%s", joined)
	}
	// 并且再次 up 不会陷入「每次都重建」的循环。
	mark := len(f.commands())
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if strings.Contains(strings.Join(f.commands()[mark:], "\n"), "remove") {
		t.Error("旧版 wslc 上，第二次 up 不应重建容器")
	}
}

// 重启策略变了（wslc 支持时）要重建，因为它在指纹之外还要和 inspect 的实际值比对。
func TestUpRecreatesWhenRestartPolicyDiffers(t *testing.T) {
	yaml := "name: demo\nservices:\n  a:\n    image: alpine:3.20\n    restart: always\n"
	c, f, dir := newTestClient(t, yaml)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	f.containers["demo-a-1"].restart = "no" // 模拟被外部改过
	mark := len(f.commands())
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if !strings.Contains(strings.Join(f.commands()[mark:], "\n"), "remove -f demo-a-1") {
		t.Error("重启策略与期望不符，应重建")
	}
}

// 加载失败（文件有误）时，动作以非零退出码结束，错误信息出现在输出里。
func TestActionOnBrokenProject(t *testing.T) {
	c, f, dir := newTestClient(t, "services:\n  a:\n    image: x\n    volumes: ['/anon']\n")
	res, lines := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if res.Ok() || !strings.Contains(res.Stderr, "anonymous volumes are not supported") {
		t.Errorf("res = %+v", res)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "anonymous volumes") {
		t.Errorf("错误应出现在输出里:\n%s", strings.Join(lines, "\n"))
	}
	if len(f.commands()) != 0 {
		t.Error("加载失败时不应执行任何 wslc 命令")
	}
}

// 用户取消（ctx 被取消）：动作以 error 结束，而不是伪装成普通失败。
func TestActionCancelled(t *testing.T) {
	c, _, dir := newTestClient(t, twoServices)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := c.RunActionStream(ctx, dir, ActionUp, ActionOptions{Detach: true}, nil)
	if err == nil {
		t.Errorf("取消应返回 error, res=%+v", res)
	}
	if res.ExitCode != -1 {
		t.Errorf("退出码 = %d, 期望 -1", res.ExitCode)
	}
}

func TestRunHookCalledOncePerAction(t *testing.T) {
	c, _, dir := newTestClient(t, twoServices)
	var got []Result
	c.SetRunHook(func(r Result) { got = append(got, r) })
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	if len(got) != 1 || got[0].Args[0] != "up" || !got[0].Ok() {
		t.Errorf("回调 = %+v", got)
	}
}

func TestPS(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	f.markExited("demo-web-1", 0)

	all, err := c.PS(context.Background(), dir, ActionOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range all {
		names = append(names, e.Service+":"+e.Name)
	}
	if want := []string{"db:demo-db-1", "web:demo-web-1"}; !reflect.DeepEqual(names, want) {
		t.Errorf("ps -a = %v, 期望 %v", names, want)
	}
	running, _ := c.PS(context.Background(), dir, ActionOptions{})
	if len(running) != 1 || running[0].Service != "db" || !running[0].Running {
		t.Errorf("ps 只应列出运行中的: %+v", running)
	}
}

func TestLoadProjectView(t *testing.T) {
	c, _, dir := newTestClient(t, twoServices)
	p, err := c.LoadProject(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo" || !reflect.DeepEqual(p.ServiceNames(), []string{"db", "web"}) {
		t.Errorf("项目 = %+v", p)
	}
	web := p.Config.Services["web"]
	if len(web.Ports) != 1 || web.Ports[0].String() != "8080:80/tcp" || !reflect.DeepEqual(web.DependsOn, []string{"db"}) {
		t.Errorf("web = %+v", web)
	}
	db := p.Config.Services["db"]
	if len(db.Volumes) != 1 || db.Volumes[0].Source != "demo_data" {
		t.Errorf("命名卷应带项目前缀: %+v", db.Volumes)
	}
}

func TestLoadProjectWarningsReachTheView(t *testing.T) {
	c, _, dir := newTestClient(t, "services:\n  a:\n    image: x\n    privileged: true\n")
	p, err := c.LoadProject(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Config.Warnings) != 1 || !strings.Contains(p.Config.Warnings[0], "privileged") {
		t.Errorf("告警应出现在视图里: %v", p.Config.Warnings)
	}
}

func TestConfigYAML(t *testing.T) {
	c, _, dir := newTestClient(t, twoServices)
	out, _, err := c.ConfigYAML(context.Background(), dir, ActionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: demo", "image: nginx:alpine", "name: demo_data"} {
		if !strings.Contains(out, want) {
			t.Errorf("config 输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestLogs(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})

	var lines []string
	if err := c.Logs(context.Background(), dir, ActionOptions{Services: []string{"web"}}, LogsOptions{Tail: 50, Timestamps: true}, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "demo-web-1 | ") {
		t.Errorf("日志行应带容器名前缀: %v", lines)
	}
	if f.find("logs -n 50 -t demo-web-1") < 0 {
		t.Errorf("logs 参数不对:\n%s", strings.Join(f.commands(), "\n"))
	}
}

func TestVersion(t *testing.T) {
	v, err := New(newFake(t)).Version(context.Background())
	if err != nil || !strings.Contains(v, Version) {
		t.Errorf("version = %q, %v", v, err)
	}
}

func TestHumanCreated(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := map[string]string{
		"2026-01-01 11:59:30 +0000 GMT":   "30 seconds ago",
		"2026-01-01 11:00:00 +0000 UTC":   "About an hour ago",
		"2026-01-01 05:00:00 +0000 GMT+0": "7 hours ago",
		"2025-12-30 12:00:00 +0000 GMT":   "2 days ago",
		// 距 now 是 29 分 59.88 秒，秒数向下取整为 1799 秒，所以是 29 分钟而不是 30。
		"2026-01-01T11:30:00.123456789Z": "29 minutes ago",
		"2026-01-01 20:00:00 +0000 GMT":  "Less than a second ago", // 未来时间按 0 处理，而不是负数
		"not a time":                     "not a time",
		"":                               "",
		// wslc list 的真实格式（末尾带时区缩写）：+0800 的 19:49:53 即 11:49:53 UTC，距 now 恰为 10 分钟。
		"2026-01-01 19:49:53 +0800 GMT+8": "10 minutes ago",
	}
	for in, want := range tests {
		if got := humanCreatedAt(in, now); got != want {
			t.Errorf("humanCreatedAt(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// mutations 统计会改动容器状态的命令条数（创建、删除、启动、停止）。
// `run --help` 只是查询 wslc 支持哪些选项，不算。
func mutations(f *fakeWslc) int {
	n := 0
	for _, c := range f.commands() {
		if c == "run --help" {
			continue
		}
		for _, p := range []string{"run ", "create ", "remove ", "stop ", "start "} {
			if strings.HasPrefix(c, p) {
				n++
				break
			}
		}
	}
	return n
}

// 回归测试：dry-run 曾经跳过「列出现有容器」，对已经在运行的项目也说「Creating」，
// 而真正执行时其实是原样保留（或重建）。预览要反映真实状态。
func TestDryRunReflectsExistingContainers(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	mark := mutations(f)

	// 配置没变：预览应说 up-to-date，且不会有任何改动命令。
	out, err := c.DryRun(context.Background(), dir, ActionUp, ActionOptions{Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Creating") || strings.Count(out, "is up-to-date") != 2 {
		t.Errorf("配置没变时预览应是 up-to-date:\n%s", out)
	}
	if mutations(f) != mark {
		t.Error("dry-run 不应真正改动容器")
	}

	// 改了配置：预览应说 Recreating，并给出 stop / remove / run 命令。
	changed := strings.Replace(twoServices, "8080:80", "9090:80", 1)
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = c.DryRun(context.Background(), dir, ActionUp, ActionOptions{Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Recreating demo-web-1", "+ wslc stop -t 10 demo-web-1", "+ wslc remove -f demo-web-1", "+ wslc run -d --name demo-web-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("预览缺少 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Recreating demo-db-1") {
		t.Errorf("没变的 db 不应出现在重建里:\n%s", out)
	}
	if mutations(f) != mark {
		t.Error("dry-run 不应真正改动容器")
	}
}
