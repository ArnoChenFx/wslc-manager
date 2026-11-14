//go:build windows

package composert

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/arno/wslc-manager/internal/wslc"
)

// 适配层只做一件事：把 wslc.Result 的每个字段原样搬进 compose.Result。
// 搬漏一个字段（比如退出码），引擎就会把失败的命令当成成功，所以逐个核对。
// 用 cmd.exe 充当 wslc，不依赖真实的 wslc，CI 上也能跑。
func TestAdapterConvertsResults(t *testing.T) {
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		t.Skip("找不到 cmd.exe")
	}
	rt := New(wslc.NewWithBinary(comspec))
	ctx := context.Background()

	res, err := rt.Run(ctx, "/c", "echo", "hello")
	if err != nil || !res.Ok() || !strings.Contains(res.Stdout, "hello") || len(res.Args) == 0 || res.Args[0] != "/c" || res.Duration <= 0 {
		t.Errorf("Run: res=%+v err=%v", res, err)
	}
	if res, err = rt.Run(ctx, "/c", "exit", "3"); err != nil || res.ExitCode != 3 {
		t.Errorf("Run 的退出码: res=%+v err=%v，期望 3", res, err)
	}
	if res, err = rt.Run(ctx, "/c", "echo oops 1>&2 & exit 1"); err != nil || res.ExitCode != 1 || !strings.Contains(res.Stderr, "oops") {
		t.Errorf("Run 的 stderr: res=%+v err=%v", res, err)
	}

	for name, call := range map[string]func(func(string)) error{
		"RunStream": func(on func(string)) error { _, err := rt.RunStream(ctx, on, "/c", "echo", "streamed"); return err },
		"Follow":    func(on func(string)) error { _, err := rt.Follow(ctx, on, "/c", "echo", "streamed"); return err },
	} {
		var lines []string
		if err := call(func(l string) { lines = append(lines, l) }); err != nil || len(lines) != 1 || !strings.Contains(lines[0], "streamed") {
			t.Errorf("%s: lines=%q err=%v", name, lines, err)
		}
	}

	if res, err = rt.Attach(ctx, "/c", "exit", "5"); err != nil || res.ExitCode != 5 {
		t.Errorf("Attach 的退出码: res=%+v err=%v，期望 5", res, err)
	}
}
