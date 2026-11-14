//go:build !windows

package wslvm

import "errors"

// errUnsupported 说明：这个包只在 Windows 上能观测，其它平台只为了能编译。
var errUnsupported = errors.New("wslvm: 只支持 Windows")

// hostProcesses 在非 Windows 上总是失败，让探测器给出 Unknown。
func hostProcesses() ([]Process, error) { return nil, errUnsupported }

// CurrentSessionName 在非 Windows 上总是失败。
func CurrentSessionName() (string, error) { return "", errUnsupported }
