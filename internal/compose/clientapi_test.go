package compose

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// 这组测试覆盖命令行要用到的那部分对外 API：ps 的过滤、exec 的参数与退出码、
// logs 的选项、dry-run 选项、config 的告警分流。

// optsFor 把动作选项里的加载相关字段原样交给加载器：命令行的 -f / -p / --env-file
// 全靠它，漏掉一个，命令行选项就悄悄失效。
func TestOptsForMapsLoadFlags(t *testing.T) {
	got := optsFor("D:/proj", ActionOptions{
		Files: []string{"a.yaml", "b.yaml"}, ProjectName: "name", EnvFile: ".env.prod",
		Services: []string{"ignored"}, Detach: true,
	})
	want := LoadOptions{Dir: "D:/proj", Files: []string{"a.yaml", "b.yaml"}, ProjectName: "name", EnvFile: ".env.prod"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("optsFor = %+v, 期望 %+v", got, want)
	}
}

func TestPSFilterAndProjectOptions(t *testing.T) {
	c, _, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	ctx := context.Background()

	got, err := c.PS(ctx, dir, ActionOptions{Services: []string{"web"}})
	if err != nil || len(got) != 1 || got[0].Service != "web" {
		t.Errorf("只看 web: %+v, %v", got, err)
	}
	if got, _ := c.PS(ctx, dir, ActionOptions{Services: []string{"web", "db"}}); len(got) != 2 {
		t.Errorf("两个服务都点名应看到两个: %+v", got)
	}
	// -p 换了项目名，看到的就是另一个项目：没有容器。
	if got, _ := c.PS(ctx, dir, ActionOptions{ProjectName: "other"}); len(got) != 0 {
		t.Errorf("-p other 不应看到 demo 的容器: %+v", got)
	}
	// -f 指向另一份 compose 文件（相对路径相对 dir），它是另一个项目，同样没有容器。
	if err := os.WriteFile(filepath.Join(dir, "alt.yaml"), []byte("name: alt\nservices:\n  x:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := c.PS(ctx, dir, ActionOptions{Files: []string{"alt.yaml"}, All: true}); err != nil || len(got) != 0 {
		t.Errorf("-f alt.yaml 是另一个项目，应没有容器: %+v, %v", got, err)
	}
	if _, err := c.PS(ctx, dir, ActionOptions{Files: []string{"missing.yaml"}}); err == nil {
		t.Error("-f 指向不存在的文件应报错")
	}
}

func TestExecArguments(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	ctx := context.Background()

	cases := []struct {
		name string
		svc  string
		cmd  []string
		eo   ExecOptions
		want string
	}{
		{"最简：总是带 -i，管道输入才送得进去", "web", []string{"ls", "/"}, ExecOptions{}, "exec -i demo-web-1 ls /"},
		{"终端、用户、目录、环境变量", "web", []string{"sh"},
			ExecOptions{TTY: true, User: "root", WorkDir: "/srv", Env: []string{"A=1", "B=2"}},
			"exec -i -t -u root -w /srv -e A=1 -e B=2 demo-web-1 sh"},
		{"副本序号", "db", []string{"redis-cli"}, ExecOptions{Index: 2}, "exec -i demo-db-2 redis-cli"},
		{"序号小于 1 按 1 处理", "db", []string{"redis-cli"}, ExecOptions{Index: -3}, "exec -i demo-db-1 redis-cli"},
		{"命令里的参数原样传递，不被当成 exec 自己的选项", "web", []string{"sh", "-c", "echo $HOME && ls"}, ExecOptions{},
			"exec -i demo-web-1 sh -c echo $HOME && ls"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mark := len(f.commands())
			if _, err := c.Exec(ctx, dir, ActionOptions{}, tc.svc, tc.cmd, tc.eo); err != nil {
				t.Fatal(err)
			}
			// 加载项目只读文件，不碰 wslc：这一次调用应当恰好多出一条命令，就是 exec 本身。
			if got := f.commands()[mark:]; !reflect.DeepEqual(got, []string{tc.want}) {
				t.Errorf("命令 = %q, 期望 %q", got, []string{tc.want})
			}
		})
	}
}

func TestExecExitCodeAndErrors(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	ctx := context.Background()

	// 容器内命令的退出码要带回来：脚本靠它判断成败。
	f.attachExit = 7
	res, err := c.Exec(ctx, dir, ActionOptions{}, "web", []string{"false"}, ExecOptions{})
	if err != nil || res.ExitCode != 7 {
		t.Errorf("res=%+v err=%v，期望退出码 7 且无 error", res, err)
	}

	// 这两类错误发生在任何 wslc 命令之前，不应该留下命令。
	mark := len(f.commands())
	if _, err := c.Exec(ctx, dir, ActionOptions{}, "ghost", []string{"ls"}, ExecOptions{}); err == nil || !strings.Contains(err.Error(), "no such service: ghost") {
		t.Errorf("未知服务: %v", err)
	}
	if _, err := c.Exec(ctx, dir, ActionOptions{}, "web", nil, ExecOptions{}); err == nil {
		t.Error("没给命令应报错")
	}
	if len(f.commands()) != mark {
		t.Errorf("出错时不应执行 wslc 命令: %v", f.commands()[mark:])
	}

	// 被取消：以 error 返回，而不是伪装成普通失败。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Exec(cctx, dir, ActionOptions{}, "web", []string{"ls"}, ExecOptions{}); err == nil {
		t.Error("被取消应返回 error")
	}
}

func TestExecHonorsProjectOptions(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	ctx := context.Background()
	// -p 改了项目名，容器名里的项目前缀跟着变。
	if _, err := c.Exec(ctx, dir, ActionOptions{ProjectName: "other"}, "web", []string{"ls"}, ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if f.find("exec -i other-web-1 ls") < 0 {
		t.Errorf("容器名应用 -p 给的项目名:\n%s", strings.Join(f.commands(), "\n"))
	}
}

func TestLogsFollowOnlyRunningContainers(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	f.markExited("demo-web-1", 0)

	// Logs 对每个容器起一个 goroutine，回调会被并发调用（见 Logs 的文档），所以收集要加锁。
	var mu sync.Mutex
	var lines []string
	collect := func(l string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, l)
	}
	if err := c.Logs(context.Background(), dir, ActionOptions{}, LogsOptions{Follow: true}, collect); err != nil {
		t.Fatal(err)
	}
	// 运行中的跟随；已停止的跟随没有意义（也会一直等不到结束），只读已有的日志。
	if f.find("logs -f demo-db-1") < 0 {
		t.Errorf("运行中的 db 应带 -f:\n%s", strings.Join(f.commands(), "\n"))
	}
	if f.find("logs demo-web-1") < 0 || f.find("logs -f demo-web-1") >= 0 {
		t.Errorf("已停止的 web 不应带 -f:\n%s", strings.Join(f.commands(), "\n"))
	}
	if len(lines) != 2 {
		t.Errorf("两个容器各一行日志: %v", lines)
	}

	// 点名了一个没有容器的服务：报错，而不是静默地什么都不输出。
	err := c.Logs(context.Background(), dir, ActionOptions{Services: []string{"ghost"}}, LogsOptions{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "no containers found") {
		t.Errorf("err = %v", err)
	}
}

// DryRun 选项让任何动作只预览、不执行：命令行的全局 --dry-run 靠它，
// 这样预览和真正的执行走同一条路径，输出也在同一个回调里。
func TestDryRunOptionPrintsInsteadOfRunning(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	var lines []string
	res, err := c.RunActionStream(context.Background(), dir, ActionUp, ActionOptions{DryRun: true}, func(l string) { lines = append(lines, l) })
	if err != nil || !res.Ok() {
		t.Fatalf("dry-run 失败: %v %v", err, res.Error())
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"+ wslc network create", "+ wslc run -d --name demo-db-1", "+ wslc run -d --name demo-web-1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("预览缺少 %q:\n%s", want, joined)
		}
	}
	if mutations(f) != 0 || len(f.networks) != 0 || len(f.volumes) != 0 {
		t.Error("dry-run 不应改动任何东西")
	}

	// 同样适用于 down 这类动作。
	runAction(t, c, dir, ActionUp, ActionOptions{Detach: true})
	before := mutations(f)
	lines = nil
	if res, err := c.RunActionStream(context.Background(), dir, ActionDown, ActionOptions{DryRun: true}, func(l string) { lines = append(lines, l) }); err != nil || !res.Ok() {
		t.Fatalf("down dry-run 失败: %v %v", err, res.Error())
	}
	if mutations(f) != before || len(f.containers) != 2 {
		t.Errorf("down 的 dry-run 不应删除容器: %v", f.order)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "+ wslc remove -f demo-web-1") {
		t.Errorf("down 的预览应列出将执行的命令:\n%s", strings.Join(lines, "\n"))
	}
}

// config 的告警和 YAML 分开返回：YAML 可能被别的程序读，告警是给人看的。
func TestConfigYAMLReturnsWarningsSeparately(t *testing.T) {
	c, _, dir := newTestClient(t, "services:\n  a:\n    image: x\n    privileged: true\n")
	yaml, warnings, err := c.ConfigYAML(context.Background(), dir, ActionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "privileged") {
		t.Errorf("warnings = %v", warnings)
	}
	if strings.Contains(yaml, "not supported by wslc") {
		t.Errorf("告警不应混进 YAML:\n%s", yaml)
	}
	// 写错的文件：报错，不返回半截 YAML。
	bad, _, dir2 := newTestClient(t, "services:\n  a:\n    volumes: ['/anon']\n    image: x\n")
	if out, _, err := bad.ConfigYAML(context.Background(), dir2, ActionOptions{}); err == nil || out != "" {
		t.Errorf("匿名卷应报错且没有输出: %q, %v", out, err)
	}
}

func TestWslcVersion(t *testing.T) {
	v, err := New(newFake(t)).WslcVersion(context.Background())
	if err != nil || v != "wslc fake-1.2.3" {
		t.Errorf("WslcVersion = %q, %v", v, err)
	}
}

// 回归保护：--dry-run exec 绝不能真的执行。exec 在容器里做的事（删文件、改数据库……）无法撤销，
// 「预览」若悄悄真跑了，比不提供预览还糟。预览只返回将要执行的 wslc 参数。
func TestExecDryRunDoesNotRun(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	mark := len(f.commands())
	res, err := c.Exec(context.Background(), dir, ActionOptions{DryRun: true}, "web", []string{"rm", "-rf", "/data"}, ExecOptions{User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"exec", "-i", "-u", "root", "demo-web-1", "rm", "-rf", "/data"}; !reflect.DeepEqual(res.Args, want) {
		t.Errorf("预览的参数 = %q, 期望 %q", res.Args, want)
	}
	if len(f.commands()) != mark {
		t.Errorf("dry-run 不应执行任何 wslc 命令: %q", f.commands()[mark:])
	}
}

func TestUpRejectsScaleForUnknownService(t *testing.T) {
	c, f, dir := newTestClient(t, twoServices)
	res, _ := runAction(t, c, dir, ActionUp, ActionOptions{Detach: true, Scale: map[string]int{"wbe": 3}}) // 拼写错误
	if res.Ok() || !strings.Contains(res.Stderr, "--scale: no such service: wbe") {
		t.Errorf("应拒绝给未知服务设置副本数: %+v", res)
	}
	if mutations(f) != 0 || len(f.networks) != 0 {
		t.Error("校验失败时不应创建任何东西")
	}
}
