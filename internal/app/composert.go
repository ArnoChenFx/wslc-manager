package app

import (
	"context"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/wslc"
)

// composeRuntime 把 wslc.Client 适配成 compose.Runtime。
//
// 为什么需要适配层：compose 包刻意不依赖 wslc 包（分层约束：它们是平级的两个底层包，
// 互不引用），所以两边各有一个 Result 类型。Go 的接口要求返回类型完全一致，
// 字段相同也不行，只能在组装它们的 app 层做一次转换。
//
// 好处是 compose 的测试里完全不需要 wslc——只要给它一个假 Runtime。
type composeRuntime struct{ c *wslc.Client }

func convertResult(r wslc.Result) compose.Result {
	return compose.Result{Args: r.Args, Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode, Duration: r.Duration}
}

// Run 实现 compose.Runtime。
func (r composeRuntime) Run(ctx context.Context, args ...string) (compose.Result, error) {
	res, err := r.c.Run(ctx, args...)
	return convertResult(res), err
}

// RunStream 实现 compose.Runtime。
func (r composeRuntime) RunStream(ctx context.Context, onLine func(string), args ...string) (compose.Result, error) {
	res, err := r.c.RunStream(ctx, onLine, args...)
	return convertResult(res), err
}

// Follow 实现 compose.Runtime。它不占串行锁，见 wslc.Client.Follow。
func (r composeRuntime) Follow(ctx context.Context, onLine func(string), args ...string) (compose.Result, error) {
	res, err := r.c.Follow(ctx, onLine, args...)
	return convertResult(res), err
}

var _ compose.Runtime = composeRuntime{}
