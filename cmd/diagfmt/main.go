// Command diagfmt 把 mygo-automation snapshot --json 的输出渲染成缩进树。
//
// 为什么需要它：PowerShell 5.1 的 ConvertFrom-Json 处理不了超长单行 JSON，
// 而每个元素的精确坐标正是诊断布局问题的关键数据。
//
// 用法：
//
//	mygo-automation snapshot --json | go run ./cmd/diagfmt
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/arno/wslc-manager/internal/app"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取标准输入失败:", err)
		os.Exit(1)
	}
	out, err := app.FormatUITree(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(out)
}
