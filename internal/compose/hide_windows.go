//go:build windows

package compose

import (
	"os/exec"
	"syscall"
)

// hideWindow 让 wslc-compose 子进程不弹出控制台窗口。
//
// wslc-compose 是 Python 程序，Windows 上由 python.exe/pipx 启动，
// 如果不设置 CREATE_NO_WINDOW，每次编排操作都会闪一下黑窗口。
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
