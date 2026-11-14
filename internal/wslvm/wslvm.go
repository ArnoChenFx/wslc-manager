// Package wslvm 在 Windows 宿主机一侧观测 wslc 会话的虚拟机：它在不在跑，占了多少内存。
//
// 边界：
//   - 只读。启动和关闭虚拟机是 wslc 命令（internal/wslc 的 StartSession 与
//     TerminateSession），不在这里；
//   - 不调用 wslc。这是刻意的：wslc 的容器类命令（list、images……）在默认会话
//     不存在时会把它建起来、把虚拟机拉起来。拿它们来「看看虚拟机在不在」，
//     一看就把它叫醒了。这里只问操作系统：有没有对应的 vmmem 进程。
//
// 观测的依据：
//   - 每个 wslc 会话有自己的虚拟机，它在宿主机上表现为名叫 vmmem<会话名> 的进程，
//     例如会话 wslc-cli-Alice 对应 vmmemwslc-cli-Alice，任务管理器里看到的就是它；
//   - 这个进程的工作集，就是虚拟机在 Windows 上占用的内存；
//   - 虚拟机因空闲被关掉时（wslc 的 session.idleTimeout，默认 30 秒），这个进程会消失，
//     而会话本身还在，下一条 wslc 命令会把虚拟机重新建起来。所以「没有这个进程」
//     就是「虚拟机没在运行」，不管会话还在不在；
//   - 普通 WSL2 的虚拟机叫 vmmemWSL，是另一个东西，绝不能和它混淆。
package wslvm

import (
	"errors"
	"strings"
)

// State 是虚拟机的运行状态。
type State int

const (
	// Unknown 表示没能判断：枚举进程失败、算不出会话名，或者不是 Windows。
	//
	// 调用方遇到它应当「照常行事」，不能据此拦住任何操作——不知道，不等于没在跑。
	Unknown State = iota
	// Stopped 表示宿主机上没有对应的 vmmem 进程。
	Stopped
	// Running 表示宿主机上有对应的 vmmem 进程。
	Running
)

// String 返回状态的英文名，用于日志。
func (s State) String() string {
	switch s {
	case Stopped:
		return "stopped"
	case Running:
		return "running"
	default:
		return "unknown"
	}
}

// Snapshot 是某一时刻对虚拟机的一次观测。
//
// 它是普通数据（可比较），可以放心存进应用状态、在视图里反复读取。
type Snapshot struct {
	State State
	// Session 是被观测的会话名，例如 wslc-cli-Alice。
	Session string
	// Process 是该会话的虚拟机在宿主机上的进程名，例如 vmmemwslc-cli-Alice。
	// 界面拿它告诉用户「在任务管理器里找哪一行」。
	Process string
	// PID 是该进程的 ID，只在 Running 时有值。
	PID uint32
	// Memory 是虚拟机在 Windows 上占用的内存，单位字节，只在 Running 时有值。
	//
	// 取进程的工作集，和任务管理器、tasklist 里的数字是同一个口径。
	// 不取 PrivateMemorySize：那是提交量，实测比工作集大好几倍，用户拿它去和
	// 任务管理器对，会觉得「数字不对」。
	Memory uint64
}

// Process 是宿主机上的一个进程，只留这里用得到的字段。
type Process struct {
	// Name 是映像名。vmmem 进程没有 .exe 后缀。
	Name string
	PID  uint32
	// WorkingSet 是工作集，单位字节。
	WorkingSet uint64
}

// Prober 观测某个会话的虚拟机。
type Prober struct {
	// Session 是要观测的会话名。
	Session string
	// List 枚举宿主机进程。做成字段，是为了让测试换成假的，不用真去读系统进程表。
	List func() ([]Process, error)

	// err 是构造时就已经知道的失败原因（例如算不出会话名）。
	err error
}

// NewProber 返回观测「当前进程所用的默认会话」的探测器。
//
// 默认会话名随用户和是否提权而不同（见 DefaultSessionName）：本程序按什么身份运行，
// wslc 就为它建哪一个默认会话，观测的也得是那一个。算不出会话名时，返回的探测器
// 其 Probe 给出 Unknown 和原因，不会 panic。
func NewProber() *Prober {
	name, err := CurrentSessionName()
	return &Prober{Session: name, List: hostProcesses, err: err}
}

// Probe 观测一次。
//
// 出错时返回 Unknown 的快照和原因。其余情况下 error 恒为 nil，
// 「没找到进程」是 Stopped，不是错误。
func (p *Prober) Probe() (Snapshot, error) {
	snap := Snapshot{Session: p.Session}
	if p.err != nil {
		return snap, p.err
	}
	if p.Session == "" || p.List == nil {
		return snap, errors.New("wslvm: 没有可观测的会话")
	}
	snap.Process = ProcessName(p.Session)

	procs, err := p.List()
	if err != nil {
		return snap, err
	}

	// 整名匹配，不做前缀匹配：vmmemWSL（普通 WSL2）、别的用户的会话、
	// 管理员会话（wslc-cli-admin-<用户>）的进程名都以 vmmem 开头，
	// 前缀匹配会把它们错当成我们的虚拟机。
	var best *Process
	for i := range procs {
		if !sameProcessName(procs[i].Name, snap.Process) {
			continue
		}
		// 正常只会有一个。万一有重名（上一个实例还没退干净），取占内存最大的那个。
		if best == nil || procs[i].WorkingSet > best.WorkingSet {
			best = &procs[i]
		}
	}
	if best == nil {
		snap.State = Stopped
		return snap, nil
	}
	snap.State, snap.PID, snap.Memory = Running, best.PID, best.WorkingSet
	return snap, nil
}

// sameProcessName 判断进程映像名是不是 want：不区分大小写，容忍 .exe 后缀。
func sameProcessName(name, want string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	return name == strings.TrimSuffix(strings.ToLower(want), ".exe")
}

// 默认会话名的前缀，取自 WSL 源码（WSLCSessionManager.cpp 的 ResolveDefaultSessionName）：
// 普通身份是 wslc-cli-<用户名>，管理员身份是 wslc-cli-admin-<用户名>。
const (
	defaultSessionBase      = "wslc-cli"
	defaultAdminSessionBase = "wslc-cli-admin"
)

// DefaultSessionName 返回 wslc 为某个用户建的默认会话的名字。
//
// 为什么要自己算：观测虚拟机要靠进程名，进程名里带着会话名；而「问 wslc 有哪些会话」
// 恰恰是我们要绕开的那类命令。普通终端和管理员终端是两个互相隔离的会话，
// 各有各的虚拟机、容器和镜像，所以是否提权也要算进去。
func DefaultSessionName(elevated bool, user string) string {
	base := defaultSessionBase
	if elevated {
		base = defaultAdminSessionBase
	}
	return base + "-" + user
}

// ProcessName 返回某个会话的虚拟机在宿主机上的进程名。
func ProcessName(session string) string {
	return "vmmem" + session
}
