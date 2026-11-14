//go:build windows

package envsetup

import (
	"os/exec"
	"syscall"
)

// hideWindow 让子进程不弹出控制台窗口。
//
// 这是个 GUI 应用，调用 wsl.exe 时若弹出黑框会很突兀。
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
