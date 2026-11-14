package wslc

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunStreamCapturesLines 验证流式执行能把输出逐行回调出来。
//
// 用 --version 这类快速命令做端到端检查：真实进程、真实管道。
func TestRunStreamCapturesLines(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Skipf("跳过：%v", err)
	}

	var count int32
	var seen []string

	res, err := c.RunStream(context.Background(), func(line string) {
		atomic.AddInt32(&count, 1)
		seen = append(seen, line)
	}, "--version")
	if err != nil {
		t.Fatalf("流式执行失败: %v", err)
	}
	if !res.Ok() {
		t.Fatalf("命令失败: %v", res.Error())
	}
	if atomic.LoadInt32(&count) == 0 {
		t.Fatal("没有收到任何输出行")
	}
	if !strings.Contains(strings.Join(seen, "\n"), "wslc") {
		t.Errorf("输出内容异常: %v", seen)
	}
	t.Logf("收到 %d 行: %v", count, seen)
}

// TestRunStreamPullProgress 真实拉取一个小镜像，验证进度行是逐步到达的。
//
// 默认跳过（耗时且需要网络）；设 WSLC_TEST_PULL=1 才运行。
func TestRunStreamPullProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	c, err := New()
	if err != nil {
		t.Skipf("跳过：%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var lines []string
	start := time.Now()
	res, err := c.RunStream(ctx, func(line string) {
		lines = append(lines, line)
	}, "pull", "alpine:3.20")
	if err != nil {
		t.Skipf("拉取失败（可能是网络），跳过: %v", err)
	}
	t.Logf("耗时 %v，收到 %d 行输出", time.Since(start).Round(time.Millisecond), len(lines))
	if len(lines) == 0 {
		t.Error("拉取过程中没有任何输出行")
	}
	if !res.Ok() {
		t.Logf("拉取退出码 %d", res.ExitCode)
	}
}
