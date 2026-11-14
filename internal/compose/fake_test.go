package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWslc 是一个内存里的 wslc：记录每条命令，并按真实 wslc 的输出格式
// 维护容器、网络、卷、镜像的状态，让编排流程可以脱离真实 wslc 验证。
//
// 它只实现引擎用到的那部分命令。遇到没实现的命令会让测试失败，
// 而不是静默返回空——否则引擎新增了一条命令，测试却照样通过，等于没守住。
type fakeWslc struct {
	t  *testing.T
	mu sync.Mutex

	// 命令流水：每条是 "run -d --name x ..." 这样的文本。
	cmds []string

	containers map[string]*fakeContainer // 以容器名为键
	order      []string                  // 创建顺序，list 按此输出
	networks   map[string]map[string]any
	volumes    map[string]bool
	images     map[string][]string // 镜像名 → entrypoint

	// failOn 返回非空串时，该命令以退出码 1 失败，输出为返回的文本。
	failOn func(args []string) string
	// onCommand 在每条命令执行前被调用，用来模拟「命令执行期间世界发生了变化」。
	onCommand func(f *fakeWslc, args []string)
	// runHelp 是 `run --help` 的输出；决定哪些可选选项被认为受支持。
	runHelp string
	nextID  int
}

type fakeContainer struct {
	id, name, image string
	running         bool
	exitCode        int
	health          string // "", starting, healthy, unhealthy
	labels          map[string]string
	args            []string
	networks        []string // 接入的网络（第一个是主网络）
	// aliases 是每个网络上的别名；真实 wslc 会把 --network-alias 和容器短 ID 记录在端点里。
	aliases map[string][]string
	restart string
}

func newFake(t *testing.T) *fakeWslc {
	return &fakeWslc{
		t:          t,
		containers: map[string]*fakeContainer{},
		networks:   map[string]map[string]any{},
		volumes:    map[string]bool{},
		images:     map[string][]string{},
		runHelp:    "--restart --cap-add --cap-drop --health-cmd",
	}
}

// addImage 声明本地已有某个镜像及其 ENTRYPOINT。
func (f *fakeWslc) addImage(name string, entrypoint ...string) {
	f.images[name] = entrypoint
}

// commands 返回到目前为止的命令流水副本。
func (f *fakeWslc) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

// find 返回第一条以 prefix 开头的命令的下标，没有返回 -1。
func (f *fakeWslc) find(prefix string) int {
	for i, c := range f.commands() {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// count 统计以 prefix 开头的命令条数。
func (f *fakeWslc) count(prefix string) int {
	n := 0
	for _, c := range f.commands() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// Run 实现 Runtime。
func (f *fakeWslc) Run(ctx context.Context, args ...string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	f.mu.Lock()
	f.cmds = append(f.cmds, strings.Join(args, " "))
	hook := f.onCommand
	f.mu.Unlock()
	if hook != nil {
		hook(f, args)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != nil {
		if msg := f.failOn(args); msg != "" {
			return Result{Args: args, ExitCode: 1, Stderr: msg}, nil
		}
	}
	out, errText := f.dispatch(args)
	if errText != "" {
		return Result{Args: args, ExitCode: 1, Stderr: errText}, nil
	}
	return Result{Args: args, Stdout: out, Duration: time.Millisecond}, nil
}

// RunStream 实现 Runtime：输出整体一次性交给 onLine。
func (f *fakeWslc) RunStream(ctx context.Context, onLine func(string), args ...string) (Result, error) {
	res, err := f.Run(ctx, args...)
	if err == nil && onLine != nil {
		for _, l := range strings.Split(strings.TrimRight(res.Combined(), "\n"), "\n") {
			if l != "" {
				onLine(l)
			}
		}
	}
	return res, err
}

// Follow 实现 Runtime。
func (f *fakeWslc) Follow(ctx context.Context, onLine func(string), args ...string) (Result, error) {
	return f.RunStream(ctx, onLine, args...)
}

// dispatch 在持锁状态下执行一条命令，返回 (stdout, 错误文本)。
func (f *fakeWslc) dispatch(args []string) (string, string) {
	if len(args) == 0 {
		return "", "no command"
	}
	switch args[0] {
	case "run":
		if len(args) == 2 && args[1] == "--help" {
			return f.runHelp, ""
		}
		return f.create(args, true)
	case "create":
		return f.create(args, false)
	case "start":
		c := f.byRef(args[len(args)-1])
		if c == nil {
			return "", "no such container"
		}
		c.running, c.exitCode = true, 0
		return "", ""
	case "stop":
		c := f.byRef(args[len(args)-1])
		if c == nil {
			return "", "no such container"
		}
		c.running = false
		return "", ""
	case "remove":
		c := f.byRef(args[len(args)-1])
		if c == nil {
			return "", "no such container"
		}
		f.drop(c.name)
		return "", ""
	case "list":
		return f.list(args), ""
	case "inspect":
		return f.inspectContainer(args[len(args)-1])
	case "images":
		var b strings.Builder
		for name := range f.images {
			repo, tag := splitImageName(name)
			line, _ := json.Marshal(map[string]string{"Repository": repo, "Tag": tag})
			b.Write(line)
			b.WriteByte('\n')
		}
		return b.String(), ""
	case "image":
		if len(args) >= 3 && args[1] == "inspect" {
			ep, ok := f.images[args[2]]
			if !ok {
				return "", "no such image"
			}
			cfg := map[string]any{}
			if len(ep) > 0 {
				cfg["Entrypoint"] = ep
			}
			out, _ := json.Marshal([]any{map[string]any{"Config": cfg}})
			return string(out), ""
		}
	case "pull":
		f.images[args[len(args)-1]] = nil
		return "pulled " + args[len(args)-1], ""
	case "build":
		// 最后一个参数是构建上下文；-t 后面是镜像名。
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				f.images[args[i+1]] = nil
			}
		}
		return "built", ""
	case "network":
		return f.networkCmd(args[1:])
	case "volume":
		return f.volumeCmd(args[1:])
	case "exec":
		return "", ""
	case "logs":
		return "log line", ""
	}
	f.t.Errorf("fakeWslc 没有实现命令: wslc %s", strings.Join(args, " "))
	return "", "not implemented in fake"
}

func (f *fakeWslc) create(args []string, start bool) (string, string) {
	c := &fakeContainer{args: args, labels: map[string]string{}, restart: "no", aliases: map[string][]string{}}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--name":
			c.name = args[i+1]
		case "-l":
			if k, v, ok := strings.Cut(args[i+1], "="); ok {
				c.labels[k] = v
			}
		case "--network":
			c.networks = append(c.networks, args[i+1])
		case "--network-alias":
			if len(c.networks) > 0 {
				primary := c.networks[len(c.networks)-1]
				c.aliases[primary] = append(c.aliases[primary], args[i+1])
			}
		case "--restart":
			c.restart = args[i+1]
		}
	}
	if c.name == "" {
		return "", "container needs a name"
	}
	if _, dup := f.containers[c.name]; dup {
		return "", fmt.Sprintf("container name %q is already in use", c.name)
	}
	for _, n := range c.networks {
		if _, ok := f.networks[n]; !ok {
			return "", fmt.Sprintf("network %q not found", n)
		}
	}
	f.nextID++
	c.id = fmt.Sprintf("%012d", f.nextID)
	c.running = start
	c.health = "healthy"
	f.containers[c.name] = c
	f.order = append(f.order, c.name)
	return c.id, ""
}

func (f *fakeWslc) byRef(ref string) *fakeContainer {
	for _, c := range f.containers {
		if c.name == ref || c.id == ref {
			return c
		}
	}
	return nil
}

func (f *fakeWslc) drop(name string) {
	delete(f.containers, name)
	for i, n := range f.order {
		if n == name {
			f.order = append(f.order[:i], f.order[i+1:]...)
			break
		}
	}
}

func (f *fakeWslc) list(args []string) string {
	all, filter := false, ""
	for i, a := range args {
		if a == "-a" {
			all = true
		}
		if a == "-f" && i+1 < len(args) {
			filter = strings.TrimPrefix(args[i+1], "label=")
		}
	}
	var b strings.Builder
	for _, name := range f.order {
		c := f.containers[name]
		if !all && !c.running {
			continue
		}
		if filter != "" {
			k, v, _ := strings.Cut(filter, "=")
			if c.labels[k] != v {
				continue
			}
		}
		status := "Exited (0)"
		if c.running {
			status = "Up 1 minute"
		}
		line, _ := json.Marshal(map[string]string{
			"ID": c.id, "Names": c.name, "Image": c.image, "Status": status,
			"State":     map[bool]string{true: "running", false: "exited"}[c.running],
			"CreatedAt": "2026-01-01 00:00:00 +0000 GMT",
		})
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func (f *fakeWslc) inspectContainer(ref string) (string, string) {
	c := f.byRef(ref)
	if c == nil {
		return "", "no such object"
	}
	labels := map[string]any{}
	for k, v := range c.labels {
		labels[k] = v
	}
	nets := map[string]any{}
	for _, n := range c.networks {
		al := []any{}
		for _, a := range c.aliases[n] {
			al = append(al, a)
		}
		nets[n] = map[string]any{"Aliases": al}
	}
	state := map[string]any{"Running": c.running, "ExitCode": c.exitCode, "Status": map[bool]string{true: "running", false: "exited"}[c.running]}
	if c.health != "" {
		state["Health"] = map[string]any{"Status": c.health}
	}
	primary := ""
	if len(c.networks) > 0 {
		primary = c.networks[0]
	}
	out, _ := json.Marshal([]any{map[string]any{
		"Id": c.id, "Name": "/" + c.name, "State": state,
		"Config":          map[string]any{"Labels": labels},
		"HostConfig":      map[string]any{"NetworkMode": primary, "RestartPolicy": map[string]any{"Name": c.restart}},
		"NetworkSettings": map[string]any{"Networks": nets},
	}})
	return string(out), ""
}

func (f *fakeWslc) networkCmd(args []string) (string, string) {
	switch args[0] {
	case "create":
		name := args[len(args)-1]
		if _, ok := f.networks[name]; ok {
			return "", "network exists"
		}
		labels := map[string]any{}
		for i, a := range args {
			if a == "-l" && i+1 < len(args) {
				if k, v, ok := strings.Cut(args[i+1], "="); ok {
					labels[k] = v
				}
			}
		}
		f.networks[name] = map[string]any{"Name": name, "Driver": "bridge", "Internal": contains(args, "--internal"), "Labels": labels}
		return name, ""
	case "list":
		var b strings.Builder
		for name := range f.networks {
			line, _ := json.Marshal(map[string]string{"Name": name})
			b.Write(line)
			b.WriteByte('\n')
		}
		return b.String(), ""
	case "inspect":
		n, ok := f.networks[args[len(args)-1]]
		if !ok {
			return "", "no such network"
		}
		out, _ := json.Marshal([]any{n})
		return string(out), ""
	case "remove":
		delete(f.networks, args[len(args)-1])
		return "", ""
	case "connect":
		container, network := args[len(args)-1], args[len(args)-2]
		c := f.byRef(container)
		if c == nil {
			return "", "no such container"
		}
		c.networks = append(c.networks, network)
		for i, a := range args {
			if a == "--network-alias" && i+1 < len(args) {
				c.aliases[network] = append(c.aliases[network], args[i+1])
			}
		}
		return "", ""
	}
	f.t.Errorf("fakeWslc 没有实现命令: wslc network %s", strings.Join(args, " "))
	return "", "not implemented in fake"
}

func (f *fakeWslc) volumeCmd(args []string) (string, string) {
	switch args[0] {
	case "create":
		f.volumes[args[len(args)-1]] = true
		return "", ""
	case "list":
		var b strings.Builder
		for name := range f.volumes {
			line, _ := json.Marshal(map[string]string{"Name": name})
			b.Write(line)
			b.WriteByte('\n')
		}
		return b.String(), ""
	case "remove":
		delete(f.volumes, args[len(args)-1])
		return "", ""
	}
	f.t.Errorf("fakeWslc 没有实现命令: wslc volume %s", strings.Join(args, " "))
	return "", "not implemented in fake"
}

// setHealth 修改容器的健康状态，用来模拟「等一会儿之后变成 healthy」。
func (f *fakeWslc) setHealth(name, health string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.containers[name]; c != nil {
		c.health = health
	}
}

// markExited 让容器以给定退出码退出。
func (f *fakeWslc) markExited(name string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.containers[name]; c != nil {
		c.running, c.exitCode = false, code
	}
}

// has 判断容器是否存在、是否在运行。
func (f *fakeWslc) has(name string) (exists, running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.containers[name]
	if c == nil {
		return false, false
	}
	return true, c.running
}

// fastEngine 构造一个不真睡的引擎，供不经过 Client 的测试使用。
func fastEngine(f *fakeWslc) (*engine, *[]string) {
	var out []string
	var mu sync.Mutex
	e := newEngine(f, func(l string) {
		mu.Lock()
		out = append(out, l)
		mu.Unlock()
	})
	e.sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return e, &out
}
