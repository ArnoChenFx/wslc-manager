//go:build e2e

// 这个文件只在 -tags e2e 构建时参与编译。
//
// 它把 MyGo 的 automation 插件链接进程序，让外部进程（mygo-automation
// 或任何 socket 客户端）能读取并驱动正在运行的界面：列出窗口、抓取元素树
// （含每个元素的真实边界与 key）、截图、模拟点击。
//
// 正常构建不包含它，因此发布版不受影响。
package main

import _ "github.com/egoist/mygo/plugins/automation"
