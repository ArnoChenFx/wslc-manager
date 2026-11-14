//go:build windows

package wslc

import (
	"os/exec"
	"syscall"
)

// hideWindow 让子进程不弹出控制台窗口。
//
// 这是 Windows 专属需求：GUI 程序（本应用）调用控制台程序（wslc.exe）时，
// 如果不设置 CREATE_NO_WINDOW，系统会为子进程分配一个新的控制台并闪现窗口。
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
