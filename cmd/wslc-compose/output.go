package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/arno/wslc-manager/internal/compose"
)

// exitError 让命令以指定的退出码结束。
//
// 最典型的是 exec：容器里的命令退出码是 7，wslc-compose 也要以 7 退出，
// 脚本才能据此判断成败。msg 非空时会作为一行错误信息打印；
// 为空表示原因已经输出过了（比如 up 失败时引擎已经把原因作为最后一行输出），不再重复。
type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

// usageError 表示用法错误（选项值不对、参数个数不对等），退出码是 2。
// 它和运行时失败（退出码 1）区分开，脚本才能分辨「命令写错了」与「命令没跑成」。
type usageError struct{ error }

func (u usageError) Unwrap() error { return u.error }

// lineWriter 一次写一整行，并且互斥。
//
// 日志跟随时每个容器一个 goroutine 同时回调；如果各自直接写终端，
// 两行的内容会在中间交错。加一把锁，保证每行完整。
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) line(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, s)
}

// printTable 把容器列表按列对齐输出，列与 docker compose ps 一致。
//
// 先写进缓冲再逐行去掉行尾空白：tabwriter 会为空的末尾列补空格，
// 管道里拿到带尾随空格的行很烦人。
func printTable(w io.Writer, entries []compose.PSEntry) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tIMAGE\tCOMMAND\tSERVICE\tCREATED\tSTATUS\tPORTS")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, e.Image, e.Command, e.Service, e.Created, e.Status, e.Ports)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if _, err := fmt.Fprintln(w, strings.TrimRight(line, " ")); err != nil {
			return err
		}
	}
	return nil
}
