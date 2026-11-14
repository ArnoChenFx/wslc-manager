//go:build windows

// 外部测试包：wslctest 自己 import 了 wslc，放在 package wslc 里会形成循环引用。
package wslc_test

import (
	"context"
	"strings"
	"testing"

	"github.com/arno/wslc-manager/internal/wslc/wslctest"
)

// TestStartSessionRunsContainerList 验证「启动虚拟机」发的是 list。
//
// 这条命令的选择有讲究：只有要和虚拟机里的 Docker 引擎对话的命令才会把虚拟机拉起来，
// info、system session list 这类只问会话管理器的命令不会。若有人为了「更轻」把它换成 info，
// 界面上的「启动」按钮就变成什么都不做，而且不报错。
func TestStartSessionRunsContainerList(t *testing.T) {
	f := wslctest.New(t)
	if err := f.Client.StartSession(context.Background()); err != nil {
		t.Fatalf("StartSession 失败: %v", err)
	}
	calls := f.Calls()
	if len(calls) != 1 || calls[0] != "list --format json" {
		t.Errorf("调用 = %q，期望恰好一次 \"list --format json\"", calls)
	}
}

func TestStartSessionReportsFailure(t *testing.T) {
	f := wslctest.New(t, wslctest.Reply{Prefix: "list", Stderr: "virtualization is not enabled", Exit: 1})
	err := f.Client.StartSession(context.Background())
	if err == nil {
		t.Fatal("wslc 退出码为 1，StartSession 应当返回错误")
	}
	if !strings.Contains(err.Error(), "virtualization is not enabled") {
		t.Errorf("错误里应带着 wslc 的原话，实际: %v", err)
	}
}

// TestTerminateSessionRunsSessionTerminate 验证「关闭虚拟机」只终止 wslc 的会话。
//
// 防的是图省事改成 wsl --shutdown：那会把用户其它的 WSL2 发行版也一起关掉，
// 而且 wslc 的代码里根本不会调用 wsl.exe——这里断言命令就是 wslc 自己的 system session terminate。
func TestTerminateSessionRunsSessionTerminate(t *testing.T) {
	f := wslctest.New(t)
	if err := f.Client.TerminateSession(context.Background()); err != nil {
		t.Fatalf("TerminateSession 失败: %v", err)
	}
	calls := f.Calls()
	if len(calls) != 1 || calls[0] != "system session terminate" {
		t.Errorf("调用 = %q，期望恰好一次 \"system session terminate\"", calls)
	}
	for _, c := range calls {
		if strings.Contains(strings.ToLower(c), "shutdown") {
			t.Errorf("不能用 shutdown 之类会波及其它 WSL 虚拟机的命令: %q", c)
		}
	}
}

func TestTerminateSessionReportsFailure(t *testing.T) {
	f := wslctest.New(t, wslctest.Reply{Prefix: "system session terminate", Stderr: "no default session", Exit: 1})
	err := f.Client.TerminateSession(context.Background())
	if err == nil {
		t.Fatal("wslc 退出码为 1，TerminateSession 应当返回错误")
	}
	if !strings.Contains(err.Error(), "no default session") {
		t.Errorf("错误里应带着 wslc 的原话，实际: %v", err)
	}
}

// 假 wslc 自身的行为：按前缀回答、记录每次调用。别的测试都建立在它之上，
// 它出错会让「某条命令没有被发出」这类断言悄悄失去意义。
func TestFakeRecordsAndReplies(t *testing.T) {
	f := wslctest.New(t,
		wslctest.Reply{Prefix: "system session list", Stdout: "ID  PID  NAME\r\n1  2  wslc-cli-X\r\n"},
		wslctest.Reply{Prefix: "info", Stdout: "客户端:\r\n"},
	)
	ctx := context.Background()

	res, err := f.Client.Run(ctx, "system", "session", "list")
	if err != nil || !strings.Contains(res.Stdout, "wslc-cli-X") {
		t.Fatalf("session list 没有拿到预设输出: res=%+v err=%v", res, err)
	}
	res, err = f.Client.Run(ctx, "info")
	if err != nil || !strings.Contains(res.Stdout, "客户端") {
		t.Fatalf("info 没有拿到预设输出: res=%+v err=%v", res, err)
	}
	if _, err := f.Client.Run(ctx, "images", "--format", "json"); err != nil {
		t.Fatal(err)
	}

	if f.Count("system session") != 1 || f.Count("INFO") != 1 || f.Count("images") != 1 || f.Count("list") != 0 {
		t.Errorf("Count 不对: %q", f.Calls())
	}
	if got := len(f.Calls()); got != 3 {
		t.Errorf("应记录 3 次调用，实际 %d: %q", got, f.Calls())
	}
}
