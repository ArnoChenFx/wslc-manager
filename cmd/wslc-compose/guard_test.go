package main

import (
	"os/exec"
	"strings"
	"testing"
)

// 回归保护：命令行必须是一个小而独立的二进制，不能把图形界面的依赖带进来。
//
// 为什么要专门测：发布时 CGO_ENABLED=0 拦不住这件事——MyGo 本身不需要 cgo，
// 有人在命令行里随手 import 了 internal/app（比如想复用某个辅助函数），
// 构建照样成功，只是这个「命令行工具」悄悄变成了几十 MB、带整套界面栈的程序，
// 而且在没有图形环境的机器上可能根本跑不起来。
func TestDoesNotLinkTheGUI(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("找不到 go 命令")
	}
	// go test 的工作目录就是本包目录，"." 即命令行本身。
	out, err := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list 失败: %v\n%s", err, out)
	}
	deps := strings.Fields(string(out))

	has := func(path string) bool {
		for _, d := range deps {
			if d == path || strings.HasPrefix(d, path+"/") {
				return true
			}
		}
		return false
	}
	for _, forbidden := range []string{
		"github.com/egoist/mygo",                           // 界面工具包
		"github.com/arno/wslc-manager/internal/app",        // 界面本身
		"github.com/arno/wslc-manager/internal/selfupdate", // 自更新插件（依赖界面工具包）
	} {
		if has(forbidden) {
			t.Errorf("命令行不应依赖 %s：它会把图形界面栈带进这个小程序", forbidden)
		}
	}
	// 反向确认：这个检查看的确实是真的依赖图（否则对着一个空结果，上面的断言永远通过）。
	for _, required := range []string{"github.com/arno/wslc-manager/internal/compose", "github.com/spf13/cobra"} {
		if !has(required) {
			t.Errorf("依赖图里应当有 %s，检查可能没有看对东西（共 %d 个包）", required, len(deps))
		}
	}
}
