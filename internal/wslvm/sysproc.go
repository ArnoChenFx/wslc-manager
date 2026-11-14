package wslvm

// 这个文件解析 NtQuerySystemInformation(SystemProcessInformation) 返回的缓冲区。
//
// 为什么不用 GetProcessMemoryInfo 之类的常规接口：它们要先 OpenProcess，而 vmmem 是受保护的
// 最小进程，普通用户打不开它。任务管理器、tasklist、Get-Process 读到它的内存，
// 走的都是 NtQuerySystemInformation，这里照着做，普通用户权限就够。
//
// 为什么用 encoding/binary 按偏移读，而不是把结构体直接盖到缓冲区上：
//   - 不用 unsafe 指针转换，在 -race（checkptr）下也没有对齐方面的顾虑；
//   - 偏移写成常量，可以在任何平台上用合成的缓冲区测解析逻辑，不依赖真实系统。

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

// SYSTEM_PROCESS_INFORMATION 在 64 位 Windows 上的布局，只列用到的字段。
// 它是未公开但长期稳定的结构，各字段的位置与 Process Hacker 的 phnt 头文件一致：
//
//	偏移  大小  字段
//	   0     4  NextEntryOffset         到下一项的字节数，最后一项为 0
//	  56     2  ImageName.Length        映像名的字节数（UTF-16）
//	  64     8  ImageName.Buffer        指向映像名的指针，落在同一块缓冲区里
//	  80     8  UniqueProcessId
//	 144     8  WorkingSetSize          工作集
//
// 32 位系统的偏移不同，本程序只发布 64 位版本，遇到就报「不支持」（见 hostProcesses）。
const (
	spiNextEntryOffset = 0
	spiImageNameLength = 56
	spiImageNameBuffer = 64
	spiUniqueProcessID = 80
	spiWorkingSetSize  = 144
	// spiMinSize 是读到 WorkingSetSize 至少需要的字节数。
	spiMinSize = spiWorkingSetSize + 8
)

// parseProcessInfo 解析一块 SystemProcessInformation 缓冲区。
//
// base 是这块缓冲区第一个字节在内存里的地址：映像名的指针是绝对地址，
// 减去 base 才是它在缓冲区里的位置。缓冲区本身被截断或偏移不合理时返回错误，
// 绝不死循环、也不越界。
func parseProcessInfo(buf []byte, base uint64) ([]Process, error) {
	var out []Process
	off := 0
	for {
		if off < 0 || off+spiMinSize > len(buf) {
			return out, errors.New("wslvm: 进程信息缓冲区被截断")
		}
		e := buf[off:]
		next := binary.LittleEndian.Uint32(e[spiNextEntryOffset:])
		nameLen := int(binary.LittleEndian.Uint16(e[spiImageNameLength:]))
		namePtr := binary.LittleEndian.Uint64(e[spiImageNameBuffer:])
		pid := binary.LittleEndian.Uint64(e[spiUniqueProcessID:])
		ws := binary.LittleEndian.Uint64(e[spiWorkingSetSize:])

		p := Process{PID: uint32(pid), WorkingSet: ws}
		// 映像名可以为空（比如 Idle 进程），指针不落在缓冲区里也一律当作没有，
		// 而不是去读不属于我们的内存。
		if nameLen > 0 && nameLen%2 == 0 && namePtr >= base {
			start := namePtr - base
			if start <= uint64(len(buf)) && uint64(nameLen) <= uint64(len(buf))-start {
				p.Name = decodeUTF16LE(buf[start : start+uint64(nameLen)])
			}
		}
		out = append(out, p)

		if next == 0 {
			return out, nil
		}
		// 下一项必须在后面、且在缓冲区内：否则是坏数据，停下比死循环强。
		if int(next) < spiMinSize || off+int(next) > len(buf) {
			return out, fmt.Errorf("wslvm: 进程信息里的 NextEntryOffset=%d 不合理", next)
		}
		off += int(next)
	}
}

// decodeUTF16LE 把小端 UTF-16 字节序列解码成字符串。
func decodeUTF16LE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}
