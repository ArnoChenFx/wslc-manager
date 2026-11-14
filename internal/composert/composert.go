// Package composert 把 wslc.Client 适配成 compose.Runtime。
//
// 为什么需要适配层：compose 包刻意不依赖 wslc 包（两个都是底层包，互不引用），
// 所以两边各有一个 Result 类型。Go 的接口要求返回类型完全一致，字段相同也不行，
// 只能在把它们组装起来的地方做一次转换。图形界面和命令行都要做这件事，
// 所以单独成包，而不是各写一份。
//
// 好处是 compose 的测试里完全不需要 wslc——只要给它一个假 Runtime。
package composert

import (
	"context"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/wslc"
)

// New 把 wslc 客户端包装成 Compose 引擎要用的 Runtime。
func New(c *wslc.Client) compose.Runtime { return adapter{c: c} }

type adapter struct{ c *wslc.Client }

func convert(r wslc.Result) compose.Result {
	return compose.Result{Args: r.Args, Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode, Duration: r.Duration}
}

// Run 实现 compose.Runtime。
func (a adapter) Run(ctx context.Context, args ...string) (compose.Result, error) {
	res, err := a.c.Run(ctx, args...)
	return convert(res), err
}

// RunStream 实现 compose.Runtime。
func (a adapter) RunStream(ctx context.Context, onLine func(string), args ...string) (compose.Result, error) {
	res, err := a.c.RunStream(ctx, onLine, args...)
	return convert(res), err
}

// Follow 实现 compose.Runtime。它不占串行锁，见 wslc.Client.Follow。
func (a adapter) Follow(ctx context.Context, onLine func(string), args ...string) (compose.Result, error) {
	res, err := a.c.Follow(ctx, onLine, args...)
	return convert(res), err
}

// Attach 实现 compose.Runtime。子进程直接接到当前进程的标准流，见 wslc.Client.Attach。
func (a adapter) Attach(ctx context.Context, args ...string) (compose.Result, error) {
	res, err := a.c.Attach(ctx, args...)
	return convert(res), err
}

var _ compose.Runtime = adapter{}
