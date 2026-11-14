package main

import (
	"context"
	"errors"
	"sync"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/composert"
	"github.com/arno/wslc-manager/internal/wslc"
)

// errWslcNotFound 是找不到 wslc 时给用户看的提示。
//
// wslc 包自己的错误信息是中文（图形界面用），命令行面向的是英文文档和脚本，
// 所以在这里换成英文，并直接说明怎么办。
var errWslcNotFound = errors.New("wslc not found: install WSL (winget install Microsoft.WSL), or set WSLC_BIN to the path of wslc.exe")

// lazyRuntime 在第一次真正需要 wslc 时才去定位它。
//
// 为什么惰性：`config`、`--version` 这类命令根本不碰 wslc，
// 在没装 WSL 的机器上（比如 CI）也应该能用；如果启动时就要求 wslc 存在，它们就全废了。
// 找不到 wslc 的错误由每个真正需要它的调用自然带出来。
type lazyRuntime struct {
	once sync.Once
	rt   compose.Runtime
	err  error
}

func (l *lazyRuntime) get() (compose.Runtime, error) {
	l.once.Do(func() {
		c, err := wslc.New()
		if err != nil {
			if errors.Is(err, wslc.ErrNotFound) {
				err = errWslcNotFound
			}
			l.err = err
			return
		}
		// 去掉 wslc 客户端的默认 60 秒超时：它只在调用方没有 deadline 时才生效，
		// 而命令行里没有——不去掉的话，几分钟的 pull / build 会在一分钟时被截断。
		c.SetTimeout(0)
		l.rt = composert.New(c)
	})
	return l.rt, l.err
}

// Run 实现 compose.Runtime。
func (l *lazyRuntime) Run(ctx context.Context, args ...string) (compose.Result, error) {
	rt, err := l.get()
	if err != nil {
		return compose.Result{Args: args, ExitCode: -1}, err
	}
	return rt.Run(ctx, args...)
}

// RunStream 实现 compose.Runtime。
func (l *lazyRuntime) RunStream(ctx context.Context, onLine func(string), args ...string) (compose.Result, error) {
	rt, err := l.get()
	if err != nil {
		return compose.Result{Args: args, ExitCode: -1}, err
	}
	return rt.RunStream(ctx, onLine, args...)
}

// Follow 实现 compose.Runtime。
func (l *lazyRuntime) Follow(ctx context.Context, onLine func(string), args ...string) (compose.Result, error) {
	rt, err := l.get()
	if err != nil {
		return compose.Result{Args: args, ExitCode: -1}, err
	}
	return rt.Follow(ctx, onLine, args...)
}

// Attach 实现 compose.Runtime。
func (l *lazyRuntime) Attach(ctx context.Context, args ...string) (compose.Result, error) {
	rt, err := l.get()
	if err != nil {
		return compose.Result{Args: args, ExitCode: -1}, err
	}
	return rt.Attach(ctx, args...)
}

var _ compose.Runtime = (*lazyRuntime)(nil)
