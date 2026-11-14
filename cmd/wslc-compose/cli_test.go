package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/arno/wslc-manager/internal/compose"
)

// 这组测试用一个记录调用的假引擎，验证命令行本身的责任：
// 选项怎么映射成引擎选项、输出写到哪个流、退出码是多少。
// 引擎的行为（up 的流程、依赖等待……）在 internal/compose 里测，这里不重复。

const testDir = `E:\work\proj`

type call struct {
	kind    string // action / ps / logs / exec / config / wslc
	action  compose.Action
	dir     string
	opt     compose.ActionOptions
	service string
	command []string
	exec    compose.ExecOptions
	logs    compose.LogsOptions
}

type fakeEngine struct {
	mu    sync.Mutex
	calls []call

	// 预设行为
	actionLines  []string
	actionResult compose.Result
	actionErr    error
	psEntries    []compose.PSEntry
	psErr        error
	logLines     []string
	logsErr      error
	execResult   compose.Result
	execErr      error
	yaml         string
	warnings     []string
	configErr    error
	wslcVersion  string
	wslcErr      error
}

func (f *fakeEngine) record(c call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeEngine) RunActionStream(_ context.Context, dir string, action compose.Action, opt compose.ActionOptions, onLine func(string)) (compose.Result, error) {
	f.record(call{kind: "action", action: action, dir: dir, opt: opt})
	for _, l := range f.actionLines {
		onLine(l)
	}
	return f.actionResult, f.actionErr
}

func (f *fakeEngine) PS(_ context.Context, dir string, opt compose.ActionOptions) ([]compose.PSEntry, error) {
	f.record(call{kind: "ps", dir: dir, opt: opt})
	return f.psEntries, f.psErr
}

func (f *fakeEngine) Logs(_ context.Context, dir string, opt compose.ActionOptions, lo compose.LogsOptions, onLine func(string)) error {
	f.record(call{kind: "logs", dir: dir, opt: opt, logs: lo})
	for _, l := range f.logLines {
		onLine(l)
	}
	return f.logsErr
}

func (f *fakeEngine) Exec(_ context.Context, dir string, opt compose.ActionOptions, service string, command []string, eo compose.ExecOptions) (compose.Result, error) {
	f.record(call{kind: "exec", dir: dir, opt: opt, service: service, command: command, exec: eo})
	return f.execResult, f.execErr
}

func (f *fakeEngine) ConfigYAML(_ context.Context, dir string, opt compose.ActionOptions) (string, []string, error) {
	f.record(call{kind: "config", dir: dir, opt: opt})
	return f.yaml, f.warnings, f.configErr
}

func (f *fakeEngine) WslcVersion(context.Context) (string, error) {
	f.record(call{kind: "wslc"})
	return f.wslcVersion, f.wslcErr
}

// only 断言恰好发生了一次调用，并返回它。
func (f *fakeEngine) only(t *testing.T, kind string) call {
	t.Helper()
	if len(f.calls) != 1 || f.calls[0].kind != kind {
		t.Fatalf("期望恰好一次 %s 调用, 实际 %+v", kind, f.calls)
	}
	return f.calls[0]
}

func (f *fakeEngine) kinds() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.kind)
	}
	return out
}

// cliRun 是一次命令行调用的结果。
type cliRun struct {
	code           int
	stdout, stderr string
}

func runWith(ctx context.Context, eng engine, tty bool, args ...string) cliRun {
	var out, errb bytes.Buffer
	code := run(ctx, args, &env{
		stdout: &out, stderr: &errb, engine: eng,
		getwd:           func() (string, error) { return testDir, nil },
		stdinIsTerminal: func() bool { return tty },
	})
	return cliRun{code, out.String(), errb.String()}
}

func runCLI(eng engine, args ...string) cliRun {
	return runWith(context.Background(), eng, false, args...)
}

func TestGlobalOptionsReachTheEngine(t *testing.T) {
	f := &fakeEngine{}
	r := runCLI(f, "-f", "a.yml", "-f", "b.yml", "-p", "demo", "--env-file", ".env.x", "--profile", "dbg", "--profile", "ops", "--dry-run", "up", "-d", "web")
	if r.code != 0 {
		t.Fatalf("退出码 %d: %s", r.code, r.stderr)
	}
	c := f.only(t, "action")
	want := compose.ActionOptions{
		Services: []string{"web"}, Files: []string{"a.yml", "b.yml"}, ProjectName: "demo", EnvFile: ".env.x",
		Profiles: []string{"dbg", "ops"}, DryRun: true, Detach: true, Timeout: 10,
	}
	if c.action != compose.ActionUp || c.dir != testDir || !reflect.DeepEqual(c.opt, want) {
		t.Errorf("得到 %+v\n期望 up @ %s %+v", c, testDir, want)
	}
}

func TestUpOptions(t *testing.T) {
	f := &fakeEngine{}
	r := runCLI(f, "up", "-d", "--build", "--force-recreate", "--scale", "web=3", "--scale", "db=2", "-t", "5", "--wait-timeout", "30", "web", "db")
	if r.code != 0 {
		t.Fatalf("退出码 %d: %s", r.code, r.stderr)
	}
	o := f.only(t, "action").opt
	if !o.Detach || !o.Build || !o.ForceRecreate || o.Timeout != 5 || o.WaitTimeout.Seconds() != 30 ||
		!reflect.DeepEqual(o.Scale, map[string]int{"web": 3, "db": 2}) || !reflect.DeepEqual(o.Services, []string{"web", "db"}) {
		t.Errorf("选项映射不对: %+v", o)
	}
}

// 子命令自己的 -f（logs 的「跟随」）与全局的 -f（compose 文件）不冲突：
// 全局选项写在命令之前，命令之后的 -f 属于命令。
func TestShortFlagDoesNotCollideWithGlobalFile(t *testing.T) {
	f := &fakeEngine{}
	r := runCLI(f, "-f", "a.yml", "-p", "demo", "logs", "-f", "web")
	if r.code != 0 {
		t.Fatalf("退出码 %d: %s", r.code, r.stderr)
	}
	c := f.only(t, "logs")
	if !reflect.DeepEqual(c.opt.Files, []string{"a.yml"}) || c.opt.ProjectName != "demo" || !c.logs.Follow || !reflect.DeepEqual(c.opt.Services, []string{"web"}) {
		t.Errorf("得到 %+v", c)
	}
}

func TestScaleValidation(t *testing.T) {
	for _, bad := range []string{"web", "web=", "=3", "web=x", "web=-1"} {
		t.Run(bad, func(t *testing.T) {
			f := &fakeEngine{}
			r := runCLI(f, "up", "--scale", bad)
			if r.code != 2 || !strings.Contains(r.stderr, "invalid --scale value") {
				t.Errorf("code=%d stderr=%q，期望用法错误（2）", r.code, r.stderr)
			}
			if len(f.calls) != 0 {
				t.Errorf("校验失败时不应调用引擎: %+v", f.calls)
			}
		})
	}
}

func TestOtherActionsMapOptions(t *testing.T) {
	tests := []struct {
		args   []string
		action compose.Action
		check  func(o compose.ActionOptions) bool
	}{
		{[]string{"down"}, compose.ActionDown, func(o compose.ActionOptions) bool { return !o.Volumes && o.Timeout == 10 }},
		{[]string{"down", "-v", "-t", "3"}, compose.ActionDown, func(o compose.ActionOptions) bool { return o.Volumes && o.Timeout == 3 }},
		{[]string{"start", "web"}, compose.ActionStart, func(o compose.ActionOptions) bool { return reflect.DeepEqual(o.Services, []string{"web"}) }},
		{[]string{"stop", "-t", "7", "web", "db"}, compose.ActionStop, func(o compose.ActionOptions) bool {
			return o.Timeout == 7 && reflect.DeepEqual(o.Services, []string{"web", "db"})
		}},
		{[]string{"restart"}, compose.ActionRestart, func(o compose.ActionOptions) bool { return o.Timeout == 10 && len(o.Services) == 0 }},
		{[]string{"pull", "db"}, compose.ActionPull, func(o compose.ActionOptions) bool { return reflect.DeepEqual(o.Services, []string{"db"}) }},
		{[]string{"build", "--no-cache", "api"}, compose.ActionBuild, func(o compose.ActionOptions) bool {
			return o.NoCache && reflect.DeepEqual(o.Services, []string{"api"})
		}},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			f := &fakeEngine{}
			if r := runCLI(f, tc.args...); r.code != 0 {
				t.Fatalf("退出码 %d: %s", r.code, r.stderr)
			}
			c := f.only(t, "action")
			if c.action != tc.action || !tc.check(c.opt) {
				t.Errorf("得到 %+v", c)
			}
		})
	}
}

// start 没有 -t：它只启动，用不到停止超时。给了就是用法错误，而不是悄悄忽略。
func TestStartHasNoTimeoutFlag(t *testing.T) {
	if r := runCLI(&fakeEngine{}, "start", "-t", "5"); r.code != 2 {
		t.Errorf("code=%d stderr=%q", r.code, r.stderr)
	}
}

func TestActionProgressGoesToStderrAndDryRunToStdout(t *testing.T) {
	f := &fakeEngine{actionLines: []string{"Creating demo-web-1 ...", "wslc-compose: warning: x"}}
	r := runCLI(f, "up", "-d")
	if r.stdout != "" || r.stderr != "Creating demo-web-1 ...\nwslc-compose: warning: x\n" {
		t.Errorf("进度应写 stderr、stdout 留空: stdout=%q stderr=%q", r.stdout, r.stderr)
	}
	f = &fakeEngine{actionLines: []string{"+ wslc run -d --name demo-web-1 nginx"}}
	r = runCLI(f, "--dry-run", "up", "-d")
	if r.stdout != "+ wslc run -d --name demo-web-1 nginx\n" || r.stderr != "" {
		t.Errorf("dry-run 的预览本身就是结果，应写 stdout: stdout=%q stderr=%q", r.stdout, r.stderr)
	}
}

func TestActionFailureAndExitCodes(t *testing.T) {
	// 动作失败：引擎已经把原因作为最后一行输出，命令行不再重复。
	f := &fakeEngine{
		actionLines:  []string{"wslc-compose: 1 service failed to start: web (boom)"},
		actionResult: compose.Result{ExitCode: 1, Stderr: "boom"},
	}
	r := runCLI(f, "up", "-d")
	if r.code != 1 || strings.Count(r.stderr, "boom") != 1 {
		t.Errorf("code=%d stderr=%q，期望退出码 1 且原因只出现一次", r.code, r.stderr)
	}

	for name, tc := range map[string]struct {
		err  error
		code int
		msg  string
	}{
		"没能开始":     {errors.New("wslc not found"), 1, "wslc-compose: wslc not found"},
		"被 Ctrl+C": {context.Canceled, 130, "wslc-compose: interrupted"},
		"超时":       {context.DeadlineExceeded, 1, "wslc-compose: timed out"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runCLI(&fakeEngine{actionErr: tc.err}, "down")
			if r.code != tc.code || !strings.Contains(r.stderr, tc.msg) {
				t.Errorf("code=%d stderr=%q，期望 %d / %q", r.code, r.stderr, tc.code, tc.msg)
			}
		})
	}
}

func TestUpFollowsLogsUnlessDetached(t *testing.T) {
	// 不带 -d：启动之后跟随日志，日志写 stdout，提示写 stderr。
	f := &fakeEngine{logLines: []string{"demo-web-1 | hello"}}
	r := runCLI(f, "up", "web")
	if r.code != 0 || !reflect.DeepEqual(f.kinds(), []string{"action", "logs"}) {
		t.Fatalf("code=%d calls=%v", r.code, f.kinds())
	}
	logs := f.calls[1]
	if !logs.logs.Follow || !reflect.DeepEqual(logs.opt.Services, []string{"web"}) || logs.dir != testDir {
		t.Errorf("跟随日志的调用不对: %+v", logs)
	}
	if r.stdout != "demo-web-1 | hello\n" || !strings.Contains(r.stderr, "Attaching to logs") {
		t.Errorf("stdout=%q stderr=%q", r.stdout, r.stderr)
	}

	for name, tc := range map[string]struct {
		args []string
		eng  *fakeEngine
	}{
		"-d 不跟随":      {[]string{"up", "-d"}, &fakeEngine{}},
		"失败不跟随":       {[]string{"up"}, &fakeEngine{actionResult: compose.Result{ExitCode: 1}}},
		"dry-run 不跟随": {[]string{"--dry-run", "up"}, &fakeEngine{}},
	} {
		t.Run(name, func(t *testing.T) {
			runCLI(tc.eng, tc.args...)
			for _, k := range tc.eng.kinds() {
				if k == "logs" {
					t.Errorf("不应跟随日志: %v", tc.eng.kinds())
				}
			}
		})
	}

	// 日志本身出错：失败。
	if r := runCLI(&fakeEngine{logsErr: errors.New("no containers found")}, "up"); r.code != 1 || !strings.Contains(r.stderr, "no containers found") {
		t.Errorf("code=%d stderr=%q", r.code, r.stderr)
	}
	// 被 Ctrl+C 打断是「分离」，不是失败：容器继续跑，退出码 0。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := runWith(ctx, &fakeEngine{logsErr: context.Canceled}, false, "up"); r.code != 0 {
		t.Errorf("Ctrl+C 分离应退出 0: code=%d stderr=%q", r.code, r.stderr)
	}
}

func TestPS(t *testing.T) {
	entries := []compose.PSEntry{
		{ID: "id-web", Name: "demo-web-1", Image: "nginx:alpine", Command: `"/docker-entrypoint.…"`, Service: "web", Created: "2 hours ago", Status: "Up 2 hours", Ports: "0.0.0.0:8080->80/tcp"},
		{ID: "", Name: "demo-db-1", Image: "redis:7", Service: "db", Created: "2 hours ago", Status: "Exited (0)"},
	}

	f := &fakeEngine{psEntries: entries}
	r := runCLI(f, "ps", "-a", "web")
	c := f.only(t, "ps")
	if !c.opt.All || !reflect.DeepEqual(c.opt.Services, []string{"web"}) {
		t.Errorf("选项 = %+v", c.opt)
	}
	lines := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("应有表头和两行:\n%s", r.stdout)
	}
	// 列按表头对齐：每行的 IMAGE 列起点相同。
	col := strings.Index(lines[0], "IMAGE")
	if strings.Index(lines[1], "nginx:alpine") != col || strings.Index(lines[2], "redis:7") != col {
		t.Errorf("列没有对齐:\n%s", r.stdout)
	}
	for _, l := range lines {
		if l != strings.TrimRight(l, " ") {
			t.Errorf("行尾不应有空白: %q", l)
		}
	}
	if !strings.HasSuffix(lines[1], "0.0.0.0:8080->80/tcp") {
		t.Errorf("端口应在行尾: %q", lines[1])
	}

	// -q 只输出 ID；没有 ID 时退而用容器名。
	r = runCLI(&fakeEngine{psEntries: entries}, "ps", "-q")
	if r.stdout != "id-web\ndemo-db-1\n" {
		t.Errorf("ps -q = %q", r.stdout)
	}
	// 没有容器：只有表头，不是错误。
	r = runCLI(&fakeEngine{}, "ps")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "NAME") || strings.Count(r.stdout, "\n") != 1 {
		t.Errorf("空列表: code=%d stdout=%q", r.code, r.stdout)
	}
	if r := runCLI(&fakeEngine{psErr: errors.New("boom")}, "ps"); r.code != 1 || !strings.Contains(r.stderr, "wslc-compose: boom") {
		t.Errorf("code=%d stderr=%q", r.code, r.stderr)
	}
}

func TestLogs(t *testing.T) {
	f := &fakeEngine{logLines: []string{"demo-web-1 | a", "demo-db-1 | b"}}
	r := runCLI(f, "logs", "-f", "-n", "20", "-t", "web", "db")
	c := f.only(t, "logs")
	if c.logs != (compose.LogsOptions{Follow: true, Tail: 20, Timestamps: true}) || !reflect.DeepEqual(c.opt.Services, []string{"web", "db"}) {
		t.Errorf("得到 %+v", c)
	}
	if r.stdout != "demo-web-1 | a\ndemo-db-1 | b\n" || r.stderr != "" {
		t.Errorf("日志写 stdout: stdout=%q stderr=%q", r.stdout, r.stderr)
	}
	if r := runCLI(&fakeEngine{logsErr: errors.New("no containers found")}, "logs", "ghost"); r.code != 1 {
		t.Errorf("code=%d", r.code)
	}
}

func TestExec(t *testing.T) {
	// 容器内命令里的 -c、--help 都属于那条命令，不能被当成 exec 自己的选项。
	f := &fakeEngine{}
	r := runCLI(f, "exec", "-u", "root", "-w", "/srv", "-e", "A=1", "-e", "B=2", "--index", "2", "web", "sh", "-c", "echo hi", "--help")
	if r.code != 0 {
		t.Fatalf("退出码 %d: %s", r.code, r.stderr)
	}
	c := f.only(t, "exec")
	if c.service != "web" || !reflect.DeepEqual(c.command, []string{"sh", "-c", "echo hi", "--help"}) {
		t.Errorf("服务/命令不对: %+v", c)
	}
	if want := (compose.ExecOptions{Index: 2, User: "root", WorkDir: "/srv", Env: []string{"A=1", "B=2"}}); !reflect.DeepEqual(c.exec, want) {
		t.Errorf("exec 选项 = %+v, 期望 %+v", c.exec, want)
	}

	// 终端：标准输入是终端才分配，-T 强制不分配。
	for name, tc := range map[string]struct {
		tty  bool
		args []string
		want bool
	}{
		"终端里默认分配":    {true, []string{"exec", "web", "sh"}, true},
		"终端里 -T 不分配": {true, []string{"exec", "-T", "web", "sh"}, false},
		"管道里不分配":     {false, []string{"exec", "web", "cat"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeEngine{}
			runWith(context.Background(), f, tc.tty, tc.args...)
			if got := f.only(t, "exec").exec.TTY; got != tc.want {
				t.Errorf("TTY = %v, 期望 %v", got, tc.want)
			}
		})
	}

	// 容器内命令的退出码就是本命令的退出码。
	if r := runCLI(&fakeEngine{execResult: compose.Result{ExitCode: 7}}, "exec", "web", "false"); r.code != 7 || r.stderr != "" {
		t.Errorf("code=%d stderr=%q，期望 7 且不多打印", r.code, r.stderr)
	}
	// 引擎层面的错误（没有这个服务……）：退出码 1 并说明原因。
	if r := runCLI(&fakeEngine{execErr: errors.New("no such service: ghost")}, "exec", "ghost", "ls"); r.code != 1 || !strings.Contains(r.stderr, "no such service: ghost") {
		t.Errorf("code=%d stderr=%q", r.code, r.stderr)
	}
}

// 回归保护：--dry-run exec 绝不能真的执行（exec 做的事无法撤销）。
// 引擎层只返回将要执行的参数，命令行把它打印出来。
func TestExecDryRunPrintsInsteadOfRunning(t *testing.T) {
	f := &fakeEngine{execResult: compose.Result{Args: []string{"exec", "-i", "demo-web-1", "rm", "-rf", "/data"}}}
	r := runCLI(f, "--dry-run", "exec", "web", "rm", "-rf", "/data")
	if r.code != 0 || r.stdout != "+ wslc exec -i demo-web-1 rm -rf /data\n" {
		t.Errorf("code=%d stdout=%q", r.code, r.stdout)
	}
	if !f.only(t, "exec").opt.DryRun {
		t.Error("引擎必须收到 DryRun，否则它会真的执行")
	}
}

func TestConfig(t *testing.T) {
	f := &fakeEngine{yaml: "name: demo\nservices: {}\n", warnings: []string{"a: privileged mode is not supported by wslc (ignoring 'privileged')"}}
	r := runCLI(f, "-p", "demo", "config")
	if f.only(t, "config").opt.ProjectName != "demo" {
		t.Error("全局选项应传给 config")
	}
	// YAML 在 stdout（可能被别的程序读），告警在 stderr（给人看）。
	if r.stdout != "name: demo\nservices: {}\n" || r.stderr != "wslc-compose: warning: a: privileged mode is not supported by wslc (ignoring 'privileged')\n" {
		t.Errorf("stdout=%q stderr=%q", r.stdout, r.stderr)
	}
	if r := runCLI(&fakeEngine{configErr: errors.New("anonymous volumes are not supported")}, "config"); r.code != 1 || r.stdout != "" {
		t.Errorf("出错时 stdout 应为空: code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
}

func TestVersion(t *testing.T) {
	want := "wslc-compose " + versionString() + "\n"
	if r := runCLI(&fakeEngine{}, "--version"); r.code != 0 || r.stdout != want {
		t.Errorf("--version: code=%d stdout=%q, 期望 %q", r.code, r.stdout, want)
	}
	if !strings.Contains(versionString(), "(engine "+compose.Version+")") {
		t.Errorf("版本串应带引擎版本: %q", versionString())
	}

	r := runCLI(&fakeEngine{wslcVersion: "wslc 3.0.5"}, "version")
	if r.stdout != want+"wslc 3.0.5\n" || r.code != 0 {
		t.Errorf("version: stdout=%q", r.stdout)
	}
	// 取不到 wslc 版本不算失败：版本命令最常用来排查问题，wslc 缺失正是该告诉用户的事。
	r = runCLI(&fakeEngine{wslcErr: errors.New("wslc not found")}, "version")
	if r.code != 0 || r.stdout != want || !strings.Contains(r.stderr, "wslc not found") {
		t.Errorf("version（无 wslc）: code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"nope"},                     // 未知命令
		{"--nope", "up"},             // 未知的全局选项
		{"up", "--nope"},             // 未知的命令选项
		{"exec", "web"},              // 缺命令
		{"exec"},                     // 缺服务和命令
		{"down", "extra"},            // down 不接受服务名
		{"config", "extra"},          // config 不接受参数
		{"up", "-t", "not-a-number"}, // 选项值类型不对
		{"-f"},                       // 选项缺值
		{"up", "-f", "a.yml"},        // 全局选项必须写在命令之前，写在之后就是该命令不认识的选项
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := &fakeEngine{}
			r := runCLI(f, args...)
			if r.code != 2 || !strings.Contains(r.stderr, "--help' for usage") || r.stdout != "" {
				t.Errorf("code=%d stdout=%q stderr=%q，期望用法错误（2）", r.code, r.stdout, r.stderr)
			}
			if len(f.calls) != 0 {
				t.Errorf("用法错误时不应调用引擎: %+v", f.calls)
			}
		})
	}
	// 提示指向出错的那个命令，而不是总指向根。
	if r := runCLI(&fakeEngine{}, "up", "--nope"); !strings.Contains(r.stderr, "Run 'wslc-compose up --help'") {
		t.Errorf("stderr=%q", r.stderr)
	}
}

func TestHelp(t *testing.T) {
	f := &fakeEngine{}
	r := runCLI(f)
	if r.code != 0 || len(f.calls) != 0 {
		t.Fatalf("无参数应打印帮助并退出 0: code=%d calls=%v", r.code, f.calls)
	}
	for _, want := range []string{"Available Commands", "up", "down", "exec", "logs", "config", "--dry-run", "Global options go before the command"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("帮助缺少 %q:\n%s", want, r.stdout)
		}
	}
	if r := runCLI(f, "up", "--help"); r.code != 0 || !strings.Contains(r.stdout, "--force-recreate") || len(f.calls) != 0 {
		t.Errorf("up --help: code=%d calls=%v", r.code, f.calls)
	}
}

func TestLineWriterWritesWholeLines(t *testing.T) {
	var buf bytes.Buffer
	lw := &lineWriter{w: &buf}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := strings.Repeat(string(rune('a'+g)), 300)
			for i := 0; i < 100; i++ {
				lw.line(s)
			}
		}()
	}
	wg.Wait()
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if len(line) != 300 || strings.Trim(line, line[:1]) != "" {
			t.Fatalf("行内容在中间被别的行打断了: %q", line)
		}
	}
}

// ---- 下面几个用真实的 compose.Client，但没有 wslc ----

// forbiddenRuntime 一旦被调用就让测试失败：用来证明某个命令根本不需要 wslc。
type forbiddenRuntime struct{ t *testing.T }

func (r forbiddenRuntime) fail(args []string) (compose.Result, error) {
	r.t.Errorf("这个命令不应该调用 wslc: %v", args)
	return compose.Result{}, errors.New("no wslc")
}
func (r forbiddenRuntime) Run(_ context.Context, a ...string) (compose.Result, error) {
	return r.fail(a)
}
func (r forbiddenRuntime) RunStream(_ context.Context, _ func(string), a ...string) (compose.Result, error) {
	return r.fail(a)
}
func (r forbiddenRuntime) Follow(_ context.Context, _ func(string), a ...string) (compose.Result, error) {
	return r.fail(a)
}
func (r forbiddenRuntime) Attach(_ context.Context, a ...string) (compose.Result, error) {
	return r.fail(a)
}

func writeProject(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runIn(dir string, eng engine, args ...string) cliRun {
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &env{
		stdout: &out, stderr: &errb, engine: eng,
		getwd:           func() (string, error) { return dir, nil },
		stdinIsTerminal: func() bool { return false },
	})
	return cliRun{code, out.String(), errb.String()}
}

// config 不碰 wslc：在没装 WSL 的机器上（比如 CI）也必须能用。
func TestConfigWorksWithoutWslc(t *testing.T) {
	dir := writeProject(t, "name: demo\nservices:\n  web:\n    image: nginx:alpine\n    privileged: true\n")
	eng := compose.New(forbiddenRuntime{t})

	r := runIn(dir, eng, "config")
	if r.code != 0 || !strings.Contains(r.stdout, "image: nginx:alpine") || !strings.Contains(r.stdout, "name: demo") {
		t.Fatalf("config: code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "wslc-compose: warning: web: privileged mode is not supported") || strings.Contains(r.stdout, "not supported") {
		t.Errorf("告警应在 stderr、不在 YAML 里: stdout=%q stderr=%q", r.stdout, r.stderr)
	}
	// 全局选项确实生效：-p 改了项目名。
	if r := runIn(dir, eng, "-p", "other", "config"); !strings.Contains(r.stdout, "name: other") {
		t.Errorf("-p 没有生效: %q", r.stdout)
	}
	// 写错的文件：退出码 1，stdout 为空。
	bad := writeProject(t, "services:\n  a:\n    image: x\n    volumes: ['/anon']\n")
	if r := runIn(bad, eng, "config"); r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "anonymous volumes are not supported") {
		t.Errorf("code=%d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
}

// stubRuntime 对只读查询都回空结果、并记下所有命令。
type stubRuntime struct {
	mu   sync.Mutex
	cmds []string
}

func (s *stubRuntime) Run(_ context.Context, args ...string) (compose.Result, error) {
	s.mu.Lock()
	s.cmds = append(s.cmds, strings.Join(args, " "))
	s.mu.Unlock()
	if len(args) == 2 && args[0] == "run" && args[1] == "--help" {
		return compose.Result{Args: args, Stdout: "--restart --cap-add"}, nil
	}
	return compose.Result{Args: args}, nil
}
func (s *stubRuntime) RunStream(ctx context.Context, _ func(string), a ...string) (compose.Result, error) {
	return s.Run(ctx, a...)
}
func (s *stubRuntime) Follow(ctx context.Context, _ func(string), a ...string) (compose.Result, error) {
	return s.Run(ctx, a...)
}
func (s *stubRuntime) Attach(ctx context.Context, a ...string) (compose.Result, error) {
	return s.Run(ctx, a...)
}

// 命令行 → 真实引擎 → dry-run：预览写 stdout，并且只做只读查询、不改动任何东西。
func TestDryRunUpEndToEnd(t *testing.T) {
	dir := writeProject(t, "name: demo\nservices:\n  web:\n    image: nginx:alpine\n    ports: ['8080:80']\n")
	rt := &stubRuntime{}
	r := runIn(dir, compose.New(rt), "--dry-run", "up", "-d")
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	for _, want := range []string{"+ wslc network create", "+ wslc run -d --name demo-web-1", "-p 8080:80"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("预览缺少 %q:\n%s", want, r.stdout)
		}
	}
	for _, cmd := range rt.cmds {
		if !strings.HasPrefix(cmd, "list ") && !strings.HasPrefix(cmd, "network list") && !strings.HasPrefix(cmd, "volume list") &&
			!strings.HasPrefix(cmd, "images") && cmd != "run --help" {
			t.Errorf("dry-run 中执行了会改动状态的命令: %s", cmd)
		}
	}
}

// 找不到 wslc 时给出能照着做的提示，而且只有真正需要 wslc 的命令才会失败。
func TestMissingWslc(t *testing.T) {
	t.Setenv("WSLC_BIN", filepath.Join(t.TempDir(), "no-such-wslc.exe"))
	dir := writeProject(t, "name: demo\nservices:\n  web:\n    image: nginx\n")
	eng := compose.New(&lazyRuntime{})

	for _, args := range [][]string{{"ps"}, {"up", "-d"}, {"down"}, {"logs"}} {
		r := runIn(dir, eng, args...)
		if r.code != 1 || !strings.Contains(r.stderr, "wslc not found: install WSL") || !strings.Contains(r.stderr, "WSLC_BIN") {
			t.Errorf("%v: code=%d stderr=%q，期望退出码 1 和可操作的提示", args, r.code, r.stderr)
		}
	}
	// 不需要 wslc 的命令照常工作。
	if r := runIn(dir, eng, "config"); r.code != 0 {
		t.Errorf("config 不需要 wslc: code=%d stderr=%q", r.code, r.stderr)
	}
	if r := runIn(dir, eng, "--version"); r.code != 0 {
		t.Errorf("--version 不需要 wslc: code=%d", r.code)
	}
	// version 子命令：wslc 缺失只是在 stderr 提示，不算失败。
	if r := runIn(dir, eng, "version"); r.code != 0 || !strings.Contains(r.stderr, "wslc not found") {
		t.Errorf("version: code=%d stderr=%q", r.code, r.stderr)
	}
}
