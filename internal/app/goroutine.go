package app

import (
	"bytes"
	"runtime"
	"strconv"
)

// goroutineID 返回当前协程的 ID。
//
// Go 没有公开 API，只能从 runtime.Stack 的头部解析：
// "goroutine 123 [running]:"。这里仅用于「无窗口模式下识别重入」，
// 属于调试/测试路径，性能不重要。
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// 头部形如 "goroutine 18 [running]:"，取第二个字段。
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
