package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/arno/wslc-manager/internal/compose"
)

// engine 是命令行需要的编排能力。*compose.Client 满足它。
//
// 抽成接口是为了测试：用一个记录调用的假实现，就能不碰 wslc、
// 直接验证「命令行选项 → 引擎选项」的映射——这是命令行最容易写错的部分。
type engine interface {
	RunActionStream(ctx context.Context, dir string, action compose.Action, opt compose.ActionOptions, onLine func(string)) (compose.Result, error)
	PS(ctx context.Context, dir string, opt compose.ActionOptions) ([]compose.PSEntry, error)
	Logs(ctx context.Context, dir string, opt compose.ActionOptions, lo compose.LogsOptions, onLine func(string)) error
	Exec(ctx context.Context, dir string, opt compose.ActionOptions, service string, command []string, eo compose.ExecOptions) (compose.Result, error)
	ConfigYAML(ctx context.Context, dir string, opt compose.ActionOptions) (string, []string, error)
	WslcVersion(ctx context.Context) (string, error)
}

var _ engine = (*compose.Client)(nil)

// env 是命令行与外部世界的接触面：标准流、引擎、当前目录、终端判断。
// 全部可替换，测试里不需要真的终端、真的目录，也不需要 wslc。
type env struct {
	stdout, stderr  io.Writer
	engine          engine
	getwd           func() (string, error)
	stdinIsTerminal func() bool
}

// globalOptions 是写在命令之前的全局选项。
type globalOptions struct {
	files       []string
	projectName string
	envFile     string
	profiles    []string
	dryRun      bool
}

// options 把全局选项和命令自己的服务列表合成引擎的动作选项。
func (g *globalOptions) options(services []string) compose.ActionOptions {
	return compose.ActionOptions{
		Services:    services,
		Files:       g.files,
		ProjectName: g.projectName,
		EnvFile:     g.envFile,
		Profiles:    g.profiles,
		DryRun:      g.dryRun,
	}
}

type cli struct {
	env *env
	g   globalOptions
	// started 表示命令已经真正开始执行，也就是 cobra 的选项与参数校验都通过了。
	// 开始之前的错误（未知命令、未知选项、参数个数不对）都是用法错误，退出码 2。
	started bool
}

// run 执行一次命令行调用，返回进程退出码：
// 0 成功；1 运行失败；2 用法错误；130 被 Ctrl+C 打断；exec 则是容器内命令的退出码。
func run(ctx context.Context, args []string, e *env) int {
	c := &cli{env: e}
	root := c.root()
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	return c.report(cmd, err)
}

// report 把错误翻译成一行提示和退出码。
func (c *cli) report(cmd *cobra.Command, err error) int {
	var exit exitError
	var usage usageError
	switch {
	case errors.As(err, &exit):
		if exit.msg != "" {
			fmt.Fprintln(c.env.stderr, "wslc-compose: "+exit.msg)
		}
		return exit.code
	case !c.started || errors.As(err, &usage):
		path := "wslc-compose"
		if cmd != nil {
			path = cmd.CommandPath()
		}
		fmt.Fprintf(c.env.stderr, "wslc-compose: %v\nRun '%s --help' for usage.\n", err, path)
		return 2
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(c.env.stderr, "wslc-compose: interrupted")
		return 130
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintln(c.env.stderr, "wslc-compose: timed out")
		return 1
	default:
		fmt.Fprintln(c.env.stderr, "wslc-compose: "+err.Error())
		return 1
	}
}

func versionString() string { return version + " (engine " + compose.Version + ")" }

func (c *cli) root() *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:   "wslc-compose [options] <command>",
		Short: "Docker Compose style orchestration for WSL containers (wslc)",
		Long: strings.TrimSpace(`
wslc-compose runs Compose projects on WSL containers (wslc).

It reads compose.yaml (or docker-compose.yml) from the current directory or one
of its parents. It manages the same projects as the WSLC Manager app: both use
the same engine and label their containers the same way.

Global options go before the command:

  wslc-compose -f other.yaml -p demo up -d`),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		// 全局选项（-f、-p ……）是根命令的本地选项，必须写在子命令之前。
		// 这样子命令才能有自己的 -f（logs -f 是「跟随」），两者互不冲突；
		// docker compose 也是这么做的。
		TraverseChildren:  true,
		PersistentPreRunE: func(*cobra.Command, []string) error { c.started = true; return nil },
	}
	root.SetOut(c.env.stdout)
	root.SetErr(c.env.stderr)

	f := root.Flags()
	f.StringArrayVarP(&c.g.files, "file", "f", nil,
		"Compose file; repeat to overlay several (default: compose.yaml or docker-compose.yml in this or a parent directory, or $COMPOSE_FILE)")
	f.StringVarP(&c.g.projectName, "project-name", "p", "", "Project name (default: the name: key, or the directory name)")
	f.StringVar(&c.g.envFile, "env-file", "", "Read variables from this file instead of .env")
	f.StringArrayVar(&c.g.profiles, "profile", nil, "Enable a profile; repeat for several")
	f.BoolVar(&c.g.dryRun, "dry-run", false, "Print the wslc commands instead of running them (up, down, start, stop, restart, pull, build, exec)")
	f.BoolVar(&showVersion, "version", false, "Print the version and exit")

	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if showVersion {
			fmt.Fprintln(c.env.stdout, "wslc-compose "+versionString())
			return nil
		}
		return cmd.Help()
	}

	root.AddCommand(
		c.upCmd(), c.downCmd(),
		c.lifecycleCmd(compose.ActionStart, "Start existing containers", false),
		c.lifecycleCmd(compose.ActionStop, "Stop running containers", true),
		c.lifecycleCmd(compose.ActionRestart, "Restart containers", true),
		c.psCmd(), c.logsCmd(), c.execCmd(),
		c.pullCmd(), c.buildCmd(),
		c.configCmd(), c.versionCmd(),
	)
	return root
}

// runAction 执行一个编排动作并把进度逐行输出。
func (c *cli) runAction(ctx context.Context, action compose.Action, opt compose.ActionOptions) error {
	dir, err := c.env.getwd()
	if err != nil {
		return err
	}
	// 进度写 stderr，stdout 留给 ps、config、logs 这类真正的数据，管道里才好用；
	// dry-run 的预览本身就是结果，所以写 stdout。
	out := c.env.stderr
	if opt.DryRun {
		out = c.env.stdout
	}
	res, err := c.env.engine.RunActionStream(ctx, dir, action, opt, (&lineWriter{w: out}).line)
	if err != nil {
		return err // 没能开始，或者被取消
	}
	if !res.Ok() {
		// 失败原因引擎已经作为最后一行输出过了，不再重复打印。
		return exitError{code: 1}
	}
	return nil
}

// followLogs 跟随日志，直到用户按 Ctrl+C。容器不受影响，继续运行。
func (c *cli) followLogs(ctx context.Context, opt compose.ActionOptions) error {
	dir, err := c.env.getwd()
	if err != nil {
		return err
	}
	fmt.Fprintln(c.env.stderr, "Attaching to logs (Ctrl+C to detach; containers keep running)")
	err = c.env.engine.Logs(ctx, dir, opt, compose.LogsOptions{Follow: true}, (&lineWriter{w: c.env.stdout}).line)
	if err != nil && ctx.Err() == nil {
		return err
	}
	return nil // 被 Ctrl+C 打断是「分离」，不是失败
}

func (c *cli) upCmd() *cobra.Command {
	var (
		detach, build, forceRecreate bool
		scale                        []string
		timeout, waitTimeout         int
	)
	cmd := &cobra.Command{
		Use:   "up [options] [SERVICE...]",
		Short: "Create and start services",
		Long: strings.TrimSpace(`
Create the project's networks and volumes, then create and start the containers
in dependency order. Containers whose configuration has not changed are kept;
changed ones are recreated.

A service that fails to start does not stop the others: only the services that
depend on it are skipped, and the command exits with status 1 at the end.

Without -d the logs are followed after the services start. Ctrl+C detaches; the
containers keep running.`),
		Example: "  wslc-compose up -d\n  wslc-compose up --build web\n  wslc-compose -f compose.yaml -f override.yaml up -d",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, services []string) error {
			counts, err := parseScale(scale)
			if err != nil {
				return usageError{err}
			}
			opt := c.g.options(services)
			opt.Detach, opt.Build, opt.ForceRecreate = detach, build, forceRecreate
			opt.Scale = counts
			opt.Timeout = timeout
			opt.WaitTimeout = time.Duration(waitTimeout) * time.Second
			if err := c.runAction(cmd.Context(), compose.ActionUp, opt); err != nil {
				return err
			}
			if detach || c.g.dryRun {
				return nil
			}
			return c.followLogs(cmd.Context(), opt)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&detach, "detach", "d", false, "Run in the background instead of following the logs")
	f.BoolVar(&build, "build", false, "Build images before starting")
	f.BoolVar(&forceRecreate, "force-recreate", false, "Recreate containers even if their configuration has not changed")
	f.StringArrayVar(&scale, "scale", nil, "Run N replicas of a service, SERVICE=N; repeat for several")
	f.IntVarP(&timeout, "timeout", "t", 10, "Seconds to wait for a container to stop before killing it")
	f.IntVar(&waitTimeout, "wait-timeout", 0, "Seconds to wait for each depends_on condition (service_healthy, service_completed_successfully); 0 waits forever")
	return cmd
}

// parseScale 解析 --scale SERVICE=N。
func parseScale(specs []string) (map[string]int, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make(map[string]int, len(specs))
	for _, spec := range specs {
		name, count, ok := strings.Cut(spec, "=")
		n, err := strconv.Atoi(count)
		if !ok || name == "" || err != nil || n < 0 {
			return nil, fmt.Errorf("invalid --scale value %q (expected SERVICE=N)", spec)
		}
		out[name] = n
	}
	return out, nil
}

func (c *cli) downCmd() *cobra.Command {
	var (
		volumes bool
		timeout int
	)
	cmd := &cobra.Command{
		Use:   "down [options]",
		Short: "Stop and remove the project's containers and networks",
		Long: strings.TrimSpace(`
Stop and remove every container of the project (including ones left by services
that are no longer in the compose file), then remove its networks. A network
that was not created by this project is kept.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opt := c.g.options(nil)
			opt.Volumes, opt.Timeout = volumes, timeout
			return c.runAction(cmd.Context(), compose.ActionDown, opt)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&volumes, "volumes", "v", false, "Also remove the project's named volumes (their data is lost)")
	f.IntVarP(&timeout, "timeout", "t", 10, "Seconds to wait for a container to stop before killing it")
	return cmd
}

// lifecycleCmd 构造 start / stop / restart：它们只操作已有的容器，不创建新的。
func (c *cli) lifecycleCmd(action compose.Action, short string, withTimeout bool) *cobra.Command {
	var timeout int
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [options] [SERVICE...]", action),
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, services []string) error {
			opt := c.g.options(services)
			opt.Timeout = timeout
			return c.runAction(cmd.Context(), action, opt)
		},
	}
	if withTimeout {
		cmd.Flags().IntVarP(&timeout, "timeout", "t", 10, "Seconds to wait for a container to stop before killing it")
	}
	return cmd
}

func (c *cli) pullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull [SERVICE...]",
		Short: "Pull service images",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, services []string) error {
			return c.runAction(cmd.Context(), compose.ActionPull, c.g.options(services))
		},
	}
}

func (c *cli) buildCmd() *cobra.Command {
	var noCache bool
	cmd := &cobra.Command{
		Use:   "build [options] [SERVICE...]",
		Short: "Build images for services that have a build section",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, services []string) error {
			opt := c.g.options(services)
			opt.NoCache = noCache
			return c.runAction(cmd.Context(), compose.ActionBuild, opt)
		},
	}
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "Do not use the build cache")
	return cmd
}

func (c *cli) psCmd() *cobra.Command {
	var all, quiet bool
	cmd := &cobra.Command{
		Use:   "ps [options] [SERVICE...]",
		Short: "List the project's containers",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, services []string) error {
			dir, err := c.env.getwd()
			if err != nil {
				return err
			}
			opt := c.g.options(services)
			opt.All = all
			entries, err := c.env.engine.PS(cmd.Context(), dir, opt)
			if err != nil {
				return err
			}
			if quiet {
				for _, e := range entries {
					id := e.ID
					if id == "" {
						id = e.Name // wslc 偶尔只给名字
					}
					fmt.Fprintln(c.env.stdout, id)
				}
				return nil
			}
			return printTable(c.env.stdout, entries)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&all, "all", "a", false, "Include stopped containers")
	f.BoolVarP(&quiet, "quiet", "q", false, "Print only container IDs")
	return cmd
}

func (c *cli) logsCmd() *cobra.Command {
	var (
		follow, timestamps bool
		tail               int
	)
	cmd := &cobra.Command{
		Use:   "logs [options] [SERVICE...]",
		Short: "View the containers' logs",
		Long: strings.TrimSpace(`
Print the logs of the project's containers, each line prefixed with the
container name. Stopped and crashed containers are included: that is when
their logs matter most.`),
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, services []string) error {
			dir, err := c.env.getwd()
			if err != nil {
				return err
			}
			lo := compose.LogsOptions{Follow: follow, Tail: tail, Timestamps: timestamps}
			return c.env.engine.Logs(cmd.Context(), dir, c.g.options(services), lo, (&lineWriter{w: c.env.stdout}).line)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&follow, "follow", "f", false, "Follow the output (Ctrl+C to stop)")
	f.IntVarP(&tail, "tail", "n", 0, "Only the last N lines of each container (0 = all)")
	f.BoolVarP(&timestamps, "timestamps", "t", false, "Show timestamps")
	return cmd
}

func (c *cli) execCmd() *cobra.Command {
	var (
		index         int
		user, workdir string
		envVars       []string
		noTTY         bool
	)
	cmd := &cobra.Command{
		Use:   "exec [options] SERVICE COMMAND [ARG...]",
		Short: "Run a command in a running service container",
		Long: strings.TrimSpace(`
Run COMMAND in the container of SERVICE. Standard input, output and error are
connected straight to your terminal, so interactive commands work, and so do
pipes. The exit status is the command's own.

A terminal is allocated when standard input is one; use -T to turn that off.
Options must come before SERVICE: everything after it belongs to the command.`),
		Example: "  wslc-compose exec web sh\n  wslc-compose exec -T db psql -U postgres -c 'select 1'",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := c.env.getwd()
			if err != nil {
				return err
			}
			res, err := c.env.engine.Exec(cmd.Context(), dir, c.g.options(nil), args[0], args[1:], compose.ExecOptions{
				Index: index, User: user, WorkDir: workdir, Env: envVars,
				// 只在标准输入真的是终端时才分配：在管道里强行要终端，wslc 会直接报错。
				TTY: !noTTY && c.env.stdinIsTerminal(),
			})
			if err != nil {
				return err
			}
			if c.g.dryRun {
				// 预览不执行：exec 在容器里做的事无法撤销，--dry-run 绝不能真的跑。
				fmt.Fprintln(c.env.stdout, "+ wslc "+strings.Join(res.Args, " "))
				return nil
			}
			if res.ExitCode != 0 {
				code := res.ExitCode
				if code < 0 {
					code = 1
				}
				return exitError{code: code}
			}
			return nil
		},
	}
	f := cmd.Flags()
	// 第一个位置参数之后的一切都属于容器里的命令（包括 -c、--help 之类），不能当成本命令的选项。
	f.SetInterspersed(false)
	f.IntVar(&index, "index", 1, "Replica number, when the service runs several")
	f.StringVarP(&user, "user", "u", "", "Run as this user (name or uid[:gid])")
	f.StringVarP(&workdir, "workdir", "w", "", "Working directory inside the container")
	f.StringArrayVarP(&envVars, "env", "e", nil, "Set an environment variable, KEY=VALUE; repeat for several")
	f.BoolVarP(&noTTY, "no-tty", "T", false, "Do not allocate a terminal")
	return cmd
}

func (c *cli) configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration",
		Long: strings.TrimSpace(`
Print the project as it will run: variables interpolated, .env applied, files
merged, named volumes and networks prefixed with the project name. Settings that
wslc cannot honour are reported as warnings on standard error.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := c.env.getwd()
			if err != nil {
				return err
			}
			yaml, warnings, err := c.env.engine.ConfigYAML(cmd.Context(), dir, c.g.options(nil))
			if err != nil {
				return err
			}
			for _, w := range warnings {
				fmt.Fprintln(c.env.stderr, "wslc-compose: warning: "+w)
			}
			_, err = io.WriteString(c.env.stdout, yaml)
			return err
		},
	}
}

func (c *cli) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the wslc-compose and wslc versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(c.env.stdout, "wslc-compose "+versionString())
			// 取不到 wslc 的版本不算失败：版本命令最常用来排查问题，
			// 此时 wslc 恰好缺失正是值得告诉用户的信息，而不是让整条命令报错。
			v, err := c.env.engine.WslcVersion(cmd.Context())
			if err != nil {
				fmt.Fprintln(c.env.stderr, "wslc-compose: "+err.Error())
				return nil
			}
			fmt.Fprintln(c.env.stdout, v)
			return nil
		},
	}
}
