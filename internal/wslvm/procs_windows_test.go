//go:build windows

package wslvm

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// processMemoryCounters 是 PROCESS_MEMORY_COUNTERS（psapi）。
type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// ownMemory 用 GetProcessMemoryInfo（一个和 NtQuerySystemInformation 无关的接口）
// 读本进程的工作集及其峰值。
func ownMemory(t *testing.T) (workingSet, peak uint64) {
	t.Helper()
	proc, err := syscall.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	fn := syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")
	var c processMemoryCounters
	c.Cb = uint32(unsafe.Sizeof(c))
	if r, _, e := fn.Call(uintptr(proc), uintptr(unsafe.Pointer(&c)), uintptr(c.Cb)); r == 0 {
		t.Fatalf("K32GetProcessMemoryInfo 失败: %v", e)
	}
	return uint64(c.WorkingSetSize), uint64(c.PeakWorkingSetSize)
}

// trimWorkingSet 先占用再释放一大块内存，再让系统收缩本进程的工作集，
// 使「当前工作集」远小于「峰值工作集」。
//
// 为什么要这样：测试进程的峰值和当前值通常很接近，偏移若差了一个字段、读到的是
// PeakWorkingSetSize（紧挨着 WorkingSetSize），数字仍在容差之内，测试照样通过——这个错误就踲过去了。
// 把两个值拉开之后，读错字段就无处藏身。
func trimWorkingSet(t *testing.T) {
	t.Helper()
	buf := make([]byte, 96<<20)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1 // 每页都碰一下，让它们真的进入工作集
	}
	runtime.KeepAlive(buf)
	buf = nil
	runtime.GC()
	debug.FreeOSMemory()

	proc, err := syscall.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	fn := syscall.NewLazyDLL("kernel32.dll").NewProc("K32EmptyWorkingSet")
	if r, _, e := fn.Call(uintptr(proc)); r == 0 {
		t.Fatalf("K32EmptyWorkingSet 失败: %v", e)
	}
}

// TestHostProcessesSeesSelf 用真实的系统进程表验证偏移常量没有写错。
//
// 合成缓冲区只能证明「按我写的布局读是对的」，证明不了「我写的布局就是系统的布局」。
// 这里读真实进程表，找到自己：PID、映像名和工作集都得对得上。
// 工作集要和另一个接口（GetProcessMemoryInfo）读到的接近，而且要远小于峰值工作集——
// 偏移若差一个字段，读到的会是峰值、提交量或换页池，都会被这两条分开。
func TestHostProcessesSeesSelf(t *testing.T) {
	trimWorkingSet(t)
	procs, err := hostProcesses()
	if err != nil {
		t.Fatalf("枚举进程失败: %v", err)
	}
	if len(procs) < 10 {
		t.Fatalf("只枚举到 %d 个进程，不正常", len(procs))
	}

	pid := uint32(os.Getpid())
	var self *Process
	for i := range procs {
		if procs[i].PID == pid {
			self = &procs[i]
			break
		}
	}
	if self == nil {
		t.Fatalf("进程表里找不到自己（PID %d）", pid)
	}

	exe := filepath.Base(os.Args[0])
	if !strings.EqualFold(self.Name, exe) {
		t.Errorf("映像名 = %q，期望 %q", self.Name, exe)
	}

	ws, peak := ownMemory(t)
	if self.WorkingSet == 0 {
		t.Fatal("工作集为 0")
	}
	// 前提：收缩之后峰值必须明显大于当前值，否则这个测试分辨不出「工作集」和「峰值」。
	if peak < ws*3 {
		t.Skipf("系统没有真正收缩工作集（当前 %d，峰值 %d），无法区分这两个字段", ws, peak)
	}

	// 两次读取之间工作集会有波动（枚举本身要分配 1 MB 的缓冲区、运行时还要重新调入代码页），
	// 所以给一个可观的余地：一半，或至少 8 MiB。读到峰值（几十 MiB）或提交量都会远远超出它。
	tol := ws / 2
	if tol < 8<<20 {
		tol = 8 << 20
	}
	if diff := absDiff(self.WorkingSet, ws); diff > tol {
		t.Errorf("读到的工作集 %d 与 GetProcessMemoryInfo 的 %d 相差 %d，超出容差 %d，偏移很可能错了", self.WorkingSet, ws, diff, tol)
	}
	if self.WorkingSet >= peak/2 {
		t.Errorf("工作集 %d 不该接近峰值 %d，读到的可能是 PeakWorkingSetSize", self.WorkingSet, peak)
	}
}

func absDiff(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}

// 用真实的进程表走完整条探测链路。会话名是编出来的，一定没有对应的 vmmem，
// 所以结果必须是 Stopped 且无错——证明链路通，也证明不会凭空报「运行中」。
func TestProbeAgainstRealProcessTable(t *testing.T) {
	p := &Prober{Session: "zz-no-such-session-for-test", List: hostProcesses}
	snap, err := p.Probe()
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if snap.State != Stopped {
		t.Errorf("State = %v，期望 Stopped", snap.State)
	}
}

func TestCurrentSessionName(t *testing.T) {
	name, err := CurrentSessionName()
	if err != nil {
		t.Fatalf("CurrentSessionName 失败: %v", err)
	}
	if !strings.HasPrefix(name, "wslc-cli-") {
		t.Errorf("会话名 %q 应以 wslc-cli- 开头", name)
	}
	// CI 的 runner 可能以管理员运行，所以名字是 wslc-cli-<用户> 还是 wslc-cli-admin-<用户> 都对，
	// 但无论如何结尾必须是登录用户名。
	if user := os.Getenv("USERNAME"); user != "" && !strings.HasSuffix(strings.ToLower(name), strings.ToLower("-"+user)) {
		t.Errorf("会话名 %q 应以 -%s 结尾", name, user)
	}
}

func TestNewProberUsesCurrentSession(t *testing.T) {
	want, err := CurrentSessionName()
	if err != nil {
		t.Skipf("算不出会话名: %v", err)
	}
	if got := NewProber().Session; got != want {
		t.Errorf("NewProber().Session = %q，期望 %q", got, want)
	}
}
