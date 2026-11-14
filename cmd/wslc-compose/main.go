// Command wslc-compose 是内置 Compose 引擎的命令行版本。
//
// 它和图形界面共用同一份引擎（internal/compose）：读同样的 compose 文件、
// 给容器打同样的标签，所以两边管理的是同一批项目，互相看得见。
//
// 这个程序刻意不依赖图形界面：不能 import internal/app，也不能 import MyGo，
// 这样它才是一个小而独立、没有图形栈的二进制（guard_test.go 守着这一点）。
package main

import (
	"context"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/arno/wslc-manager/internal/compose"
)

// version 是发行版本号。发布流程用 -ldflags "-X main.version=1.2.3" 写入，
// 本地构建是 "dev"。
var version = "dev"

func main() {
	// Ctrl+C 不直接杀掉进程，而是取消 ctx：引擎能借此停在一个干净的点上，
	// `up` 之后的日志跟随也靠它来「分离」（容器继续跑）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	client := compose.New(&lazyRuntime{})
	// 命令行不设总超时：用户就在终端前，等不及了自己按 Ctrl+C。
	// 引擎的默认 15 分钟会把很大的 pull / build 半路掐断。
	client.SetTimeout(0)

	code := run(ctx, os.Args[1:], &env{
		stdout:          os.Stdout,
		stderr:          os.Stderr,
		engine:          client,
		getwd:           os.Getwd,
		stdinIsTerminal: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
	})

	// os.Exit 不会跑 defer，所以显式释放信号处理。
	stop()
	os.Exit(code)
}
