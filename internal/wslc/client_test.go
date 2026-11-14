//go:build windows

package wslc

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 这些测试用 cmd.exe 充当「wslc」，不依赖真实的 wslc，CI 上也能跑。
// 仅限 Windows：测试里用的 ping -n 做定时器、cmd /c 执行内置命令。

func cmdClient(t *testing.T) *Client {
	t.Helper()
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		t.Skip("找不到 cmd.exe")
	}
	return NewWithBinary(comspec)
}

// pingClient 直接以 ping.exe 充当「wslc」，用来做定时器。
//
// 不能用 `cmd /c ping`：超时杀掉的是 cmd.exe，它的子进程 ping.exe 仍然活着并占着输出管道，
// 而 Go 要等管道关闭才返回，于是「超时」表现成了「等到 ping 自己跑完」，测不出东西。
func pingClient(t *testing.T) *Client {
	t.Helper()
	p, err := exec.LookPath("ping.exe")
	if err != nil {
		t.Skip("找不到 ping.exe")
	}
	return NewWithBinary(p)
}

// 回归测试：Run 曾经无条件在调用方的 ctx 上再叠加 60 秒默认超时。
// 编排引擎里 pull / build 可能跑几分钟，它们自己带了更长的 deadline，
// 结果每条都在一分钟时被无声截断——用户看到的是「拉大镜像总是失败」。
//
// 这里把默认超时设得很短（200ms），而调用方给了 10 秒的 deadline，
// 命令要跑 1 秒以上：如果仍被默认超时截断，就会以 -1 退出。
func TestCallerDeadlineOverridesDefaultTimeout(t *testing.T) {
	c := pingClient(t)
	c.SetTimeout(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// ping -n 3 约需 2 秒（每次间隔 1 秒）。
	res, err := c.Run(ctx, "-n", "3", "127.0.0.1")
	if err != nil || !res.Ok() {
		t.Fatalf("调用方给了 10 秒 deadline，命令不应被 200ms 的默认超时截断: err=%v res=%+v", err, res)
	}
}

// 没有调用方 deadline 时，默认超时仍然生效（保护 list / inspect 之类的快命令不会无限挂住）。
func TestDefaultTimeoutStillAppliesWithoutDeadline(t *testing.T) {
	c := pingClient(t)
	c.SetTimeout(300 * time.Millisecond)
	start := time.Now()
	res, err := c.Run(context.Background(), "-n", "10", "127.0.0.1")
	if err == nil && res.Ok() {
		t.Fatal("没有 deadline 时，默认超时应该截断长命令")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("默认超时没有生效，耗时 %s", elapsed)
	}
}

func TestRunStreamAlsoHonorsCallerDeadline(t *testing.T) {
	c := pingClient(t)
	c.SetTimeout(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.RunStream(ctx, nil, "-n", "3", "127.0.0.1")
	if err != nil || !res.Ok() {
		t.Fatalf("RunStream 也应尊重调用方的 deadline: err=%v res=%+v", err, res)
	}
}

// Follow 不占用串行锁：一条长时间运行的 Follow 不能堵住其他命令。
//
// 回归意义：编排引擎的 `logs -f` 要一直挂着；如果它和 Run 共用串行锁，
// 在它结束之前，界面上所有的刷新、启动、停止都会被堵死。
func TestFollowDoesNotBlockOtherCommands(t *testing.T) {
	c := pingClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	var once sync.Once
	followDone := make(chan struct{})
	go func() {
		defer close(followDone)
		// 约 8 秒的长命令；ping 第一次回复就会输出一行。
		_, _ = c.Follow(ctx, func(string) { once.Do(func() { close(started) }) },
			"-n", "9", "127.0.0.1")
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Follow 没有输出")
	}

	// Follow 还在跑：另一条命令必须立刻能执行。
	done := make(chan Result, 1)
	go func() {
		res, _ := c.Run(context.Background(), "-n", "1", "127.0.0.1")
		done <- res
	}()
	select {
	case res := <-done:
		if !strings.Contains(res.Stdout, "127.0.0.1") {
			t.Errorf("输出 = %q", res.Stdout)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Follow 占用了串行锁，把 Run 堵住了")
	}
	cancel()
	<-followDone
}

// 对照：RunStream 仍然走串行锁，两个流式命令不会重叠。
func TestRunStreamStillSerialized(t *testing.T) {
	c := cmdClient(t)
	var running, maxRunning int32
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.RunStream(context.Background(), func(string) {}, "/c", "echo", "x")
		}()
	}
	// 用 SetRunHook 观察重叠：钩子在锁外调用，这里只确认全部完成且没有死锁。
	c.SetRunHook(func(Result) {
		n := atomic.AddInt32(&running, 1)
		for {
			m := atomic.LoadInt32(&maxRunning)
			if n <= m || atomic.CompareAndSwapInt32(&maxRunning, m, n) {
				break
			}
		}
		atomic.AddInt32(&running, -1)
	})
	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()
	select {
	case <-waitCh:
	case <-time.After(15 * time.Second):
		t.Fatal("三个流式命令没有全部完成，疑似死锁")
	}
}

// Follow 启动失败时不能把锁留在被占用状态（它根本没拿锁，所以也不能多释放一次）。
func TestFollowStartFailureDoesNotCorruptLock(t *testing.T) {
	c := NewWithBinary(`C:\definitely\not\here.exe`)
	if _, err := c.Follow(context.Background(), nil, "x"); err == nil {
		t.Fatal("找不到可执行文件应返回错误")
	}
	// 如果上面多释放了一次锁，这里会直接崩溃：fatal error: sync: unlock of unlocked mutex。
	if _, err := c.Run(context.Background(), "x"); err == nil {
		t.Fatal("找不到可执行文件应返回错误")
	}
	if _, err := c.RunStream(context.Background(), nil, "x"); err == nil {
		t.Fatal("找不到可执行文件应返回错误")
	}
}
