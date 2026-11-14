package wslvm

import (
	"encoding/binary"
	"errors"
	"testing"
	"unicode/utf16"
)

// fakeList 返回一个固定的进程表，充当 Prober.List。
func fakeList(procs ...Process) func() ([]Process, error) {
	return func() ([]Process, error) { return procs, nil }
}

func TestDefaultSessionName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		elevated bool
		user     string
		want     string
	}{
		{"普通身份", false, "Alice", "wslc-cli-Alice"},
		{"管理员身份", true, "Bob", "wslc-cli-admin-Bob"},
		// 用户名里可以有空格和连字符，原样拼进去，不做任何转义。
		{"带空格的用户名", false, "Mary Jane", "wslc-cli-Mary Jane"},
		{"带连字符的用户名", true, "a-b", "wslc-cli-admin-a-b"},
	} {
		if got := DefaultSessionName(tc.elevated, tc.user); got != tc.want {
			t.Errorf("%s: DefaultSessionName(%v, %q) = %q，期望 %q", tc.name, tc.elevated, tc.user, got, tc.want)
		}
	}
}

func TestProcessName(t *testing.T) {
	if got := ProcessName("wslc-cli-Alice"); got != "vmmemwslc-cli-Alice" {
		t.Errorf("ProcessName = %q", got)
	}
}

// TestProbe 过一遍各种进程表，确认状态判断和内存取值。
//
// 其中几项是回归测试，防的是「把别的 vmmem 当成我们的虚拟机」：
// 本机上通常同时有 vmmemWSL（普通 WSL2）、管理员会话、别的用户的会话，
// 它们的进程名都以 vmmem 开头。若按前缀匹配，用户明明关掉了 wslc 的虚拟机，
// 界面还显示「运行中」，而且「关闭」按钮对着一个根本不存在的会话。
func TestProbe(t *testing.T) {
	const mb = 1 << 20
	const session = "wslc-cli-Alice"
	ours := Process{Name: "vmmemwslc-cli-Alice", PID: 100, WorkingSet: 474 * mb}

	for _, tc := range []struct {
		name  string
		procs []Process
		want  State
		pid   uint32
		mem   uint64
	}{
		{"运行中", []Process{{Name: "explorer.exe", PID: 1, WorkingSet: 9}, ours}, Running, 100, 474 * mb},
		{"没有任何 vmmem", []Process{{Name: "explorer.exe", PID: 1}}, Stopped, 0, 0},
		{"空进程表", nil, Stopped, 0, 0},
		{"只有普通 WSL2 的虚拟机", []Process{{Name: "vmmemWSL", PID: 7, WorkingSet: 700 * mb}}, Stopped, 0, 0},
		{"只有别的用户的会话", []Process{{Name: "vmmemwslc-cli-Bob", PID: 8, WorkingSet: 300 * mb}}, Stopped, 0, 0},
		{"只有管理员会话", []Process{{Name: "vmmemwslc-cli-admin-Alice", PID: 9, WorkingSet: 300 * mb}}, Stopped, 0, 0},
		{"名字是我们的前缀的更长名字", []Process{{Name: "vmmemwslc-cli-Alice2", PID: 10}}, Stopped, 0, 0},
		{"和普通 WSL2 的虚拟机并存", []Process{{Name: "vmmemWSL", PID: 7, WorkingSet: 700 * mb}, ours}, Running, 100, 474 * mb},
		{"大小写不同", []Process{{Name: "VMMEMWSLC-CLI-ALICE", PID: 11, WorkingSet: 5 * mb}}, Running, 11, 5 * mb},
		{"带 .exe 后缀", []Process{{Name: "vmmemwslc-cli-Alice.exe", PID: 12, WorkingSet: 6 * mb}}, Running, 12, 6 * mb},
		{"重名时取占内存最大的", []Process{
			{Name: "vmmemwslc-cli-Alice", PID: 20, WorkingSet: 1 * mb},
			{Name: "vmmemwslc-cli-Alice", PID: 21, WorkingSet: 9 * mb},
			{Name: "vmmemwslc-cli-Alice", PID: 22, WorkingSet: 3 * mb},
		}, Running, 21, 9 * mb},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Prober{Session: session, List: fakeList(tc.procs...)}
			snap, err := p.Probe()
			if err != nil {
				t.Fatalf("不该出错: %v", err)
			}
			if snap.State != tc.want {
				t.Fatalf("State = %v，期望 %v（%+v）", snap.State, tc.want, snap)
			}
			if snap.PID != tc.pid || snap.Memory != tc.mem {
				t.Errorf("PID/Memory = %d/%d，期望 %d/%d", snap.PID, snap.Memory, tc.pid, tc.mem)
			}
			if snap.Session != session || snap.Process != "vmmemwslc-cli-Alice" {
				t.Errorf("Session/Process = %q/%q", snap.Session, snap.Process)
			}
		})
	}
}

// TestProbeUnknown 验证观测不了的情形一律是 Unknown，且带着原因。
//
// 调用方对 Unknown 的约定是「照常行事」，所以这里要保证
// 失败时绝不会被误报成 Stopped——那会让应用把正在运行的虚拟机当成已停止，拦住所有页面。
func TestProbeUnknown(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name string
		p    *Prober
	}{
		{"枚举进程失败", &Prober{Session: "wslc-cli-Alice", List: func() ([]Process, error) { return nil, boom }}},
		{"没有会话名", &Prober{List: fakeList()}},
		{"没有枚举函数", &Prober{Session: "wslc-cli-Alice"}},
		{"构造时就失败", &Prober{Session: "wslc-cli-Alice", List: fakeList(), err: boom}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := tc.p.Probe()
			if err == nil {
				t.Error("应当带着原因返回错误")
			}
			if snap.State != Unknown {
				t.Errorf("State = %v，期望 Unknown", snap.State)
			}
			if snap.PID != 0 || snap.Memory != 0 {
				t.Errorf("Unknown 不该有 PID/Memory: %+v", snap)
			}
		})
	}
}

func TestStateString(t *testing.T) {
	for s, want := range map[State]string{Unknown: "unknown", Stopped: "stopped", Running: "running"} {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q，期望 %q", s, got, want)
		}
	}
}

// ---- 解析 SYSTEM_PROCESS_INFORMATION ----

// testEntrySize 是合成缓冲区里每一项的大小：不小于 spiMinSize，且按 8 对齐，和系统的行为一致。
const testEntrySize = 160

// buildProcessBuffer 按 SYSTEM_PROCESS_INFORMATION 的布局造一块缓冲区：
// 先是所有项，映像名接在后面，Buffer 指针填成 base 加上它在缓冲区里的位置。
func buildProcessBuffer(base uint64, procs []Process) []byte {
	namesStart := len(procs) * testEntrySize
	buf := make([]byte, namesStart)
	var names []byte
	for i, p := range procs {
		e := buf[i*testEntrySize:]
		u := utf16.Encode([]rune(p.Name))
		nameBytes := make([]byte, 2*len(u))
		for j, c := range u {
			binary.LittleEndian.PutUint16(nameBytes[2*j:], c)
		}
		if i < len(procs)-1 {
			binary.LittleEndian.PutUint32(e[spiNextEntryOffset:], testEntrySize)
		}
		binary.LittleEndian.PutUint16(e[spiImageNameLength:], uint16(len(nameBytes)))
		binary.LittleEndian.PutUint16(e[spiImageNameLength+2:], uint16(len(nameBytes)))
		if len(nameBytes) > 0 {
			binary.LittleEndian.PutUint64(e[spiImageNameBuffer:], base+uint64(namesStart+len(names)))
			names = append(names, nameBytes...)
		}
		binary.LittleEndian.PutUint64(e[spiUniqueProcessID:], uint64(p.PID))
		binary.LittleEndian.PutUint64(e[spiWorkingSetSize:], p.WorkingSet)
	}
	return append(buf, names...)
}

func TestParseProcessInfoRoundTrip(t *testing.T) {
	want := []Process{
		{Name: "System Idle Process", PID: 0, WorkingSet: 8192},
		{Name: "vmmemwslc-cli-Alice", PID: 67996, WorkingSet: 485248 << 10},
		{Name: "", PID: 4, WorkingSet: 1 << 20}, // 没有映像名
		{Name: "进程-测试.exe", PID: 4242, WorkingSet: 12345},
	}
	// 基地址取一个典型的堆地址，确保指针换算没有依赖 base 为 0。
	const base = 0x00007FF612340000
	got, err := parseProcessInfo(buildProcessBuffer(base, want), base)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("解析出 %d 项，期望 %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项 = %+v，期望 %+v", i, got[i], want[i])
		}
	}
}

// TestParseProcessInfoRejectsBadData 验证坏数据只会得到错误，不会死循环、越界或 panic。
//
// 缓冲区来自系统调用，理论上是可信的，但「相信」不是不做检查的理由：
// 解析跑在每 2 秒一次的后台轮询里，一次死循环就是一个永远占满的 CPU 核。
func TestParseProcessInfoRejectsBadData(t *testing.T) {
	const base = 0x1000
	two := []Process{{Name: "a", PID: 1}, {Name: "b", PID: 2}}

	t.Run("缓冲区被截断", func(t *testing.T) {
		buf := buildProcessBuffer(base, two)
		if _, err := parseProcessInfo(buf[:testEntrySize+10], base); err == nil {
			t.Error("第二项只剩 10 字节，应当报错")
		}
	})
	t.Run("空缓冲区", func(t *testing.T) {
		if _, err := parseProcessInfo(nil, base); err == nil {
			t.Error("空缓冲区应当报错")
		}
	})
	t.Run("NextEntryOffset 太小", func(t *testing.T) {
		buf := buildProcessBuffer(base, two)
		binary.LittleEndian.PutUint32(buf[spiNextEntryOffset:], 8) // 会原地打转
		if _, err := parseProcessInfo(buf, base); err == nil {
			t.Error("偏移小于一项的最小长度，应当报错而不是死循环")
		}
	})
	t.Run("NextEntryOffset 超出缓冲区", func(t *testing.T) {
		buf := buildProcessBuffer(base, two)
		binary.LittleEndian.PutUint32(buf[spiNextEntryOffset:], 1<<30)
		if _, err := parseProcessInfo(buf, base); err == nil {
			t.Error("偏移超出缓冲区，应当报错")
		}
	})
	t.Run("映像名指针在缓冲区之前", func(t *testing.T) {
		buf := buildProcessBuffer(base, two)
		binary.LittleEndian.PutUint64(buf[spiImageNameBuffer:], 0x10) // < base
		got, err := parseProcessInfo(buf, base)
		if err != nil || len(got) != 2 || got[0].Name != "" || got[1].Name != "b" {
			t.Errorf("坏指针只该让这一项没有名字: got=%+v err=%v", got, err)
		}
	})
	t.Run("映像名指针超出缓冲区", func(t *testing.T) {
		buf := buildProcessBuffer(base, two)
		binary.LittleEndian.PutUint64(buf[spiImageNameBuffer:], base+uint64(len(buf))+100)
		got, err := parseProcessInfo(buf, base)
		if err != nil || got[0].Name != "" {
			t.Errorf("坏指针只该让这一项没有名字: got=%+v err=%v", got, err)
		}
	})
	t.Run("映像名长度是奇数", func(t *testing.T) {
		buf := buildProcessBuffer(base, two)
		binary.LittleEndian.PutUint16(buf[spiImageNameLength:], 3)
		got, err := parseProcessInfo(buf, base)
		if err != nil || got[0].Name != "" {
			t.Errorf("UTF-16 长度必为偶数，奇数视为无名: got=%+v err=%v", got, err)
		}
	})
	t.Run("映像名长度大得离谱", func(t *testing.T) {
		buf := buildProcessBuffer(base, two)
		binary.LittleEndian.PutUint16(buf[spiImageNameLength:], 0xFFFE)
		got, err := parseProcessInfo(buf, base)
		if err != nil || got[0].Name != "" {
			t.Errorf("长度越界只该让这一项没有名字: got=%+v err=%v", got, err)
		}
	})
}
