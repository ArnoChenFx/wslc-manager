//go:build windows

// Package wslctest 提供测试用的「假 wslc」：一个批处理文件，
// 记录每次被调用的参数，并按命令前缀返回预设的输出。
//
// 为什么需要它：有些行为是「有没有发出某条命令」——例如虚拟机被用户关掉之后，
// 后台刷新不能再去问容器列表，否则就把虚拟机叫醒了。真实的 wslc 做不到这种断言，
// 也不能在测试里去关用户正在跑容器的虚拟机。
//
// 只给测试用：包里引用了 testing，不要从非测试代码里 import 它。
package wslctest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arno/wslc-manager/internal/wslc"
)

// Reply 是对某类命令的预设回答。
type Reply struct {
	// Prefix 是命令（不含 wslc 自己）的前缀，如 "system session list"，不区分大小写。
	// 按声明顺序匹配，先命中的生效；都没命中时输出为空、退出码为 0。
	Prefix string
	// Stdout 输出到标准输出的文本。
	Stdout string
	// Stderr 输出到标准错误的文本，配合 Exit 模拟失败。
	Stderr string
	// Exit 是退出码。
	Exit int
}

// Fake 是一个假的 wslc 及其调用记录。
type Fake struct {
	// Client 指向这个假 wslc，可以直接交给被测代码。
	Client *wslc.Client
	log    string
}

// New 在测试的临时目录里造一个假 wslc。
//
// 参数里不要出现 % ! ^ & | < > 这些批处理的特殊字符：它们会被批处理解释掉，
// 测试看到的就不是真正传进来的参数了。应用发给 wslc 的命令里没有这些字符。
func New(t testing.TB, replies ...Reply) *Fake {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")

	var b strings.Builder
	b.WriteString("@echo off\r\nsetlocal EnableDelayedExpansion\r\n")
	fmt.Fprintf(&b, "echo %%*>>\"%s\"\r\n", log)
	b.WriteString("set \"ARGS=%*\"\r\n")
	for i, r := range replies {
		if strings.ContainsAny(r.Prefix, "%!^&|<>\"") {
			t.Fatalf("wslctest: 前缀 %q 含批处理特殊字符", r.Prefix)
		}
		out := filepath.Join(dir, fmt.Sprintf("out%d.txt", i))
		errf := filepath.Join(dir, fmt.Sprintf("err%d.txt", i))
		for path, content := range map[string]string{out: r.Stdout, errf: r.Stderr} {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		fmt.Fprintf(&b, "if /i \"!ARGS:~0,%d!\"==\"%s\" (\r\n", len(r.Prefix), r.Prefix)
		fmt.Fprintf(&b, "  type \"%s\"\r\n  type \"%s\" 1>&2\r\n  exit /b %d\r\n)\r\n", out, errf, r.Exit)
	}
	b.WriteString("exit /b 0\r\n")

	bat := filepath.Join(dir, "wslc.bat")
	if err := os.WriteFile(bat, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Fake{Client: wslc.NewWithBinary(bat), log: log}
}

// Calls 返回到目前为止每次调用的参数，一次一项，例如 "list --all --format json"。
func (f *Fake) Calls() []string {
	data, err := os.ReadFile(f.log)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Count 返回参数以 prefix 开头（不区分大小写）的调用次数。
func (f *Fake) Count(prefix string) int {
	n := 0
	for _, c := range f.Calls() {
		if len(c) >= len(prefix) && strings.EqualFold(c[:len(prefix)], prefix) {
			n++
		}
	}
	return n
}
