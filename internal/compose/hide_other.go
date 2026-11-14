//go:build !windows

package compose

import "os/exec"

// hideWindow 在非 Windows 平台上不需要做任何事。
func hideWindow(cmd *exec.Cmd) {}
