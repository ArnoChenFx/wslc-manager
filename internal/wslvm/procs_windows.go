//go:build windows

package wslvm

// 这个文件是 Windows 上的实现：枚举进程、算出当前身份对应的默认会话名。
//
// 只用标准库的 syscall，不引入 golang.org/x/sys：它现在只是间接依赖，
// 直接 import 会让 go.mod 的依赖状态变化，而这里只需要三个系统调用。

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modNtdll                     = syscall.NewLazyDLL("ntdll.dll")
	procNtQuerySystemInformation = modNtdll.NewProc("NtQuerySystemInformation")
	modAdvapi32                  = syscall.NewLazyDLL("advapi32.dll")
	procGetUserNameW             = modAdvapi32.NewProc("GetUserNameW")
)

const (
	systemProcessInformation = 5 // SYSTEM_INFORMATION_CLASS: SystemProcessInformation
	tokenElevation           = 20

	// NTSTATUS：缓冲区不够。进程数在两次调用之间可能增加，所以要重试。
	statusInfoLengthMismatch = 0xC0000004
	statusBufferTooSmall     = 0xC0000023
)

// hostProcesses 枚举宿主机上的全部进程，带工作集。
func hostProcesses() ([]Process, error) {
	// 偏移常量只对 64 位成立（见 sysproc.go）。
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return nil, errors.New("wslvm: 只支持 64 位 Windows")
	}

	// 一般几百 KB 到 1 MB；从 1 MB 起步，不够就按系统告诉的长度加一点余量再来。
	size := 1 << 20
	for attempt := 0; attempt < 6; attempt++ {
		buf := make([]byte, size)
		var returned uint32
		r, _, _ := procNtQuerySystemInformation.Call(
			systemProcessInformation,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
			uintptr(unsafe.Pointer(&returned)),
		)
		switch status := uint32(r); status {
		case 0:
			n := int(returned)
			if n <= 0 || n > len(buf) {
				n = len(buf)
			}
			return parseProcessInfo(buf[:n], uint64(uintptr(unsafe.Pointer(&buf[0]))))
		case statusInfoLengthMismatch, statusBufferTooSmall:
			size = int(returned) + 128<<10
			if size < 2<<20 {
				size = 2 << 20
			}
		default:
			return nil, fmt.Errorf("wslvm: NtQuerySystemInformation 失败，NTSTATUS=0x%08X", status)
		}
	}
	return nil, errors.New("wslvm: 进程表一直在变大，缓冲区总是不够")
}

// CurrentSessionName 返回当前进程所用的 wslc 默认会话名。
//
// 取的是进程令牌的用户名（GetUserNameW，与 WSL 自己的端到端测试取法一致）和是否提权：
// 同一个人，普通终端和管理员终端是两个默认会话，各有各的虚拟机。
func CurrentSessionName() (string, error) {
	user, err := userName()
	if err != nil {
		return "", fmt.Errorf("wslvm: 取用户名失败: %w", err)
	}
	elevated, err := isElevated()
	if err != nil {
		return "", fmt.Errorf("wslvm: 判断是否提权失败: %w", err)
	}
	return DefaultSessionName(elevated, user), nil
}

// userName 返回当前登录用户名（不带域）。
//
// 不用 os/user：它在域账户上会去联系域控制器，可能卡住好几秒，
// 而这里是在启动路径上，GetUserNameW 只读本地令牌。
func userName() (string, error) {
	var buf [257]uint16
	n := uint32(len(buf))
	r, _, err := procGetUserNameW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return "", err
	}
	name := strings.TrimSpace(syscall.UTF16ToString(buf[:]))
	if name == "" {
		return "", errors.New("用户名为空")
	}
	return name, nil
}

// isElevated 判断当前进程是否以提升的权限（管理员）运行。
func isElevated() (bool, error) {
	proc, err := syscall.GetCurrentProcess()
	if err != nil {
		return false, err
	}
	var tok syscall.Token
	if err := syscall.OpenProcessToken(proc, syscall.TOKEN_QUERY, &tok); err != nil {
		return false, err
	}
	defer tok.Close()

	var elevated, n uint32
	if err := syscall.GetTokenInformation(tok, tokenElevation,
		(*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)), &n); err != nil {
		return false, err
	}
	return elevated != 0, nil
}
