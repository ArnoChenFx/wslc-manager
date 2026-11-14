//go:build !windows

package envsetup

import "os/exec"

// hideWindow 在非 Windows 上无需处理。
func hideWindow(cmd *exec.Cmd) {}
