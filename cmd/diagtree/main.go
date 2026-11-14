// Command diagtree 通过 automation socket 抓取界面元素树并写成可读文本。
//
// 为什么不直接用 mygo-automation 的输出：
//   - PowerShell 会把超长单行 JSON 截断，重定向时还会加 UTF-8 BOM；
//   - PowerShell 5.1 的 ConvertFrom-Json 处理不了那份 JSON。
//
// 这个小工具用 Go 直接把 socket 数据读进内存，因此没有上述限制。
//
// 用法：
//
//	go run ./cmd/diagtree -socket <path> [-out tree.txt]
//	go run ./cmd/diagtree -report report.md
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/arno/wslc-manager/internal/app"
)

func main() {
	socket := flag.String("socket", os.Getenv("MYGO_AUTOMATION"), "automation socket 路径（默认读 MYGO_AUTOMATION）")
	out := flag.String("out", "", "把元素树写入该文件")
	report := flag.String("report", "", "生成完整的 Markdown 诊断报告到该文件")
	flag.Parse()

	// -report：生成一份「当前进程能看到」的诊断报告。
	//
	// 注意：这是在另一个进程里新建的 App 实例，它没有运行中实例的内存状态
	// （容器列表、日志、设置等）。因此这里的报告以「环境 + 界面元素树」为主，
	// 内存状态部分会显示为空——这是预期行为。
	// 要连内存状态一起导出，请用应用内「系统信息 → 导出诊断报告」。
	if *report != "" {
		a := app.New()
		a.Quiet()
		a.Init()
		opt := app.DefaultDiagOptions()
		if *socket != "" {
			opt.SocketPath = *socket
		}
		path, err := a.SaveDiagnostic(opt, *report)
		if err != nil {
			fmt.Fprintln(os.Stderr, "生成报告失败:", err)
			os.Exit(1)
		}
		fmt.Println("报告已写入:", path)
		fmt.Println("（此报告来自独立进程，不含目标实例的内存状态；" +
			"完整状态请用应用内「导出诊断报告」）")
		return
	}

	tree, err := app.FetchUITree(*socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "抓取界面树失败:", err)
		os.Exit(1)
	}

	text := app.RenderUITree(tree)
	fmt.Print(text)

	if *out != "" {
		if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写入文件失败:", err)
			os.Exit(1)
		}
	}
}
