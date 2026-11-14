// Package compose 是内置的 Compose 编排引擎：把 compose 文件翻译成一系列 wslc 命令并执行。
//
// 分工：
//   - compose 文件的读取与规范化交给 compose-go（官方实现，`docker compose` 本身用它），
//     本包只在它之上做 wslc 特有的部分：限制检查、wslc 参数构造、配置指纹、
//     up/down 等编排流程、网络校验与依赖等待；
//   - 所有 wslc 调用都经过 Runtime 接口（生产环境是 internal/wslc.Client，它负责串行化），
//     测试用假实现，编排流程可以脱离真实 wslc 验证；
//   - 本包不依赖 internal/app。
package compose

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client 是编排引擎的入口。可以并发使用：加载（读文件）不占锁，
// 真正动容器的操作经由 Runtime，由它保证 wslc 调用串行。
type Client struct {
	rt Runtime

	mu sync.Mutex
	// timeout 是单个编排动作的总超时（up/pull/build 可能很慢，默认 15 分钟）。
	timeout time.Duration
	// onRun 在每条动作结束后被调用，用于诊断日志。
	onRun func(Result)
}

// New 创建客户端。rt 是执行 wslc 命令的通道。
func New(rt Runtime) *Client {
	return &Client{rt: rt, timeout: 15 * time.Minute}
}

// SetTimeout 设置单个编排动作的总超时；0 表示不限。
func (c *Client) SetTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = d
}

// SetRunHook 设置动作结束后的回调，用于诊断日志。
func (c *Client) SetRunHook(fn func(Result)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onRun = fn
}

func (c *Client) callHook(r Result) {
	c.mu.Lock()
	fn := c.onRun
	c.mu.Unlock()
	if fn != nil {
		fn(r)
	}
}

// Action 是可以在项目上执行的编排动作。
type Action string

const (
	ActionUp      Action = "up"
	ActionDown    Action = "down"
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionPull    Action = "pull"
	ActionBuild   Action = "build"
	ActionPS      Action = "ps"
	ActionLogs    Action = "logs"
)

// ActionOptions 控制动作的额外参数。
type ActionOptions struct {
	// Detach 用于 up：-d。界面只做后台启动，所以 up 总是分离模式；保留字段是为了与界面选项对应。
	Detach bool
	// Build 用于 up：--build，启动前重新构建镜像。
	Build bool
	// ForceRecreate 用于 up：--force-recreate。
	ForceRecreate bool
	// Volumes 用于 down：-v，同时删除命名卷。
	Volumes bool
	// NoCache 用于 build：--no-cache。
	NoCache bool
	// Services 限定操作的服务；为空表示全部。
	Services []string
	// Profiles 启用的 profile；带 profile 的服务默认不启动。
	Profiles []string
	// Scale 覆盖服务的副本数（服务名 → 数量）。
	Scale map[string]int
	// Timeout 是停止容器时等待的秒数，0 表示用默认值 10。
	Timeout int
	// WaitTimeout 是等待每个 depends_on 条件的最长时间，0 不限。
	WaitTimeout time.Duration
	// ProjectName 覆盖项目名。
	ProjectName string
	// EnvFile 指定 .env 文件。
	EnvFile string
	// Files 显式指定 compose 文件（-f）；为空则在项目目录里自动探测。
	Files []string
}

func (o ActionOptions) stopTimeout() int {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 10
}

// LoadProject 加载项目目录，返回界面用的项目视图。
//
// 只读文件，不碰 wslc，也不占用任何锁——项目列表刷新不会被正在运行的 up 挡住。
func (c *Client) LoadProject(ctx context.Context, dir string) (Project, error) {
	p, err := loadProject(ctx, LoadOptions{Dir: dir})
	if err != nil {
		return Project{}, err
	}
	warnings, err := checkProject(p)
	if err != nil {
		return Project{}, err
	}
	return projectView(p, dir, "", warnings), nil
}

// Load 按完整选项加载项目，返回 compose-go 的原始模型。
// 供需要完整配置的调用方使用（命令行风格的 config 输出、测试）。
func (c *Client) Load(ctx context.Context, o LoadOptions) (Project, error) {
	p, err := loadProject(ctx, o)
	if err != nil {
		return Project{}, err
	}
	warnings, err := checkProject(p)
	if err != nil {
		return Project{}, err
	}
	return projectView(p, o.Dir, o.EnvFile, warnings), nil
}

// ConfigYAML 返回解析后的完整配置（YAML），等价于 `docker compose config`。
func (c *Client) ConfigYAML(ctx context.Context, dir string, o ActionOptions) (string, error) {
	p, err := loadProject(ctx, optsFor(dir, o))
	if err != nil {
		return "", err
	}
	if _, err := checkProject(p); err != nil {
		return "", err
	}
	out, err := p.MarshalYAML()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func optsFor(dir string, o ActionOptions) LoadOptions {
	return LoadOptions{Dir: dir, Files: o.Files, ProjectName: o.ProjectName, EnvFile: o.EnvFile}
}

// RunAction 在项目目录执行一个编排动作，返回合并后的输出。
func (c *Client) RunAction(ctx context.Context, dir string, action Action, opt ActionOptions) Result {
	var lines []string
	res, err := c.RunActionStream(ctx, dir, action, opt, func(l string) { lines = append(lines, l) })
	if err != nil {
		res.Stderr = strings.TrimSpace(res.Stderr + "\n" + err.Error())
	}
	return res
}

// RunActionStream 执行编排动作，并把每一行输出实时交给 onLine。
//
// up / build / pull 都可能跑很久，界面上必须能看到进度，所以逐行回调而不是等命令结束。
// onLine 可能在后台 goroutine 里被调用，调用方需自行处理线程安全。
//
// 返回约定与原来的命令行封装一致：动作本身失败（容器起不来等）体现为 Result.ExitCode != 0，
// error 只表示「没能开始」（例如超时之前就被取消）。
func (c *Client) RunActionStream(ctx context.Context, dir string, action Action, opt ActionOptions, onLine func(string)) (Result, error) {
	return c.execute(ctx, dir, action, opt, onLine, false)
}

// DryRun 预览 up 将要执行的 wslc 命令，不改动任何东西。
// 查询类命令（list、inspect）照常执行，这样预览反映的是真实状态。
func (c *Client) DryRun(ctx context.Context, dir string, action Action, opt ActionOptions) (string, error) {
	var mu sync.Mutex
	var lines []string
	res, err := c.execute(ctx, dir, action, opt, func(l string) {
		mu.Lock()
		lines = append(lines, l)
		mu.Unlock()
	}, true)
	out := strings.Join(lines, "\n")
	if err != nil {
		return out, err
	}
	return out, res.Error()
}

// execute 是各个动作的统一入口：加载项目、应用超时、分发到具体动作、汇总结果。
func (c *Client) execute(ctx context.Context, dir string, action Action, opt ActionOptions, onLine func(string), dryRun bool) (Result, error) {
	start := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	timeout := c.timeout
	c.mu.Unlock()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var all strings.Builder
	var outMu sync.Mutex
	emit := func(line string) {
		outMu.Lock()
		all.WriteString(line)
		all.WriteString("\n")
		outMu.Unlock()
		if onLine != nil {
			onLine(line)
		}
	}
	finish := func(err error) (Result, error) {
		res := Result{Args: append([]string{string(action)}, opt.Services...), Duration: time.Since(start)}
		outMu.Lock()
		res.Stdout = all.String()
		outMu.Unlock()
		switch {
		case err == nil:
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			res.ExitCode = -1
			res.Stderr = err.Error()
			c.callHook(res)
			return res, err
		default:
			res.ExitCode = 1
			res.Stderr = err.Error()
			// 与命令行一致：失败原因也作为最后一行输出，界面的输出面板里能直接看到。
			emit("wslc-compose: " + err.Error())
			res.Stdout = all.String()
		}
		c.callHook(res)
		return res, nil
	}

	p, err := loadProject(ctx, optsFor(dir, opt))
	if err != nil {
		return finish(err)
	}
	warnings, err := checkProject(p)
	if err != nil {
		return finish(err)
	}
	for _, w := range warnings {
		emit("wslc-compose: warning: " + w)
	}

	e := newEngine(c.rt, emit)
	e.dryRun = dryRun
	var runErr error
	switch action {
	case ActionUp:
		runErr = e.up(ctx, p, upOptions{
			Services: opt.Services, Profiles: opt.Profiles, Build: opt.Build, ForceRecreate: opt.ForceRecreate,
			Scale: opt.Scale, StopTimeout: opt.stopTimeout(), WaitTimeout: opt.WaitTimeout, EnvFile: opt.EnvFile,
		})
	case ActionDown:
		runErr = e.down(ctx, p, opt)
	case ActionStart, ActionStop, ActionRestart:
		runErr = e.lifecycle(ctx, p, action, opt)
	case ActionPull:
		runErr = e.pull(ctx, p, opt)
	case ActionBuild:
		runErr = e.build(ctx, p, opt)
	default:
		runErr = fmt.Errorf("action %q is not supported here", action)
	}
	return finish(runErr)
}

// PS 返回项目当前的容器列表（结构化数据，不再解析表格文本）。
// all 为 false 时只列出运行中的容器。
func (c *Client) PS(ctx context.Context, dir string, all bool) ([]PSEntry, error) {
	p, err := loadProject(ctx, LoadOptions{Dir: dir})
	if err != nil {
		return nil, err
	}
	e := newEngine(c.rt, nil)
	list, err := e.listProjectContainers(ctx, p.Name, all)
	if err != nil {
		return nil, err
	}
	out := make([]PSEntry, 0, len(list))
	for _, ct := range list {
		out = append(out, PSEntry{
			ID: ct.ID, Name: ct.Name, Service: ct.Service, Image: ct.Image, Command: ct.Command,
			Created: ct.Created, Status: ct.Status, Ports: ct.Ports, Running: ct.Running,
		})
	}
	return out, nil
}

// Version 返回内置引擎的版本说明。
func (c *Client) Version(ctx context.Context) (string, error) {
	return "wslc-compose " + Version + " (built-in)", nil
}

// Exec 在服务的容器里执行命令，返回合并输出。index 是副本序号（从 1 开始）。
func (c *Client) Exec(ctx context.Context, dir, service string, index int, command []string) (string, error) {
	if len(command) == 0 {
		return "", errors.New("no command given")
	}
	p, err := loadProject(ctx, LoadOptions{Dir: dir})
	if err != nil {
		return "", err
	}
	s, ok := p.Services[service]
	if !ok {
		return "", fmt.Errorf("no such service: %s", service)
	}
	if index < 1 {
		index = 1
	}
	res, err := c.rt.Run(ctx, append([]string{"exec", containerName(p, s, index)}, command...)...)
	if err != nil {
		return res.Combined(), err
	}
	return res.Combined(), res.Error()
}

// Logs 读取项目内各服务的日志，每行加上容器名前缀；包括已停止或崩溃的容器，那正是日志最有用的时候。
// follow 为 true 时持续输出直到 ctx 被取消。
func (c *Client) Logs(ctx context.Context, dir string, services []string, follow bool, tail int, timestamps bool, onLine func(string)) error {
	p, err := loadProject(ctx, LoadOptions{Dir: dir})
	if err != nil {
		return err
	}
	e := newEngine(c.rt, nil)
	list, err := e.listProjectContainers(ctx, p.Name, true)
	if err != nil {
		return err
	}
	var picked []container
	for _, ct := range list {
		if len(services) == 0 || contains(services, ct.Service) {
			picked = append(picked, ct)
		}
	}
	if len(picked) == 0 {
		return errors.New("no containers found")
	}
	width := 0
	for _, ct := range picked {
		if len(ct.Name) > width {
			width = len(ct.Name)
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, ct := range picked {
		args := []string{"logs"}
		if follow && ct.Running {
			args = append(args, "-f")
		}
		if tail > 0 {
			args = append(args, "-n", strconv.Itoa(tail))
		}
		if timestamps {
			args = append(args, "-t")
		}
		args = append(args, ct.Name)
		prefix := fmt.Sprintf("%-*s | ", width, ct.Name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 用 Follow：不占串行锁，否则一条没结束的 `logs -f` 会堵死之后所有命令。
			res, err := c.rt.Follow(ctx, func(line string) { onLine(prefix + line) }, args...)
			mu.Lock()
			defer mu.Unlock()
			if firstErr == nil && ctx.Err() == nil {
				if err != nil {
					firstErr = err
				} else if !res.Ok() {
					firstErr = res.Error()
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}
