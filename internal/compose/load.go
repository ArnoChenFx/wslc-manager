package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
)

// LoadOptions 控制一次加载，对应命令行里的全局选项。
type LoadOptions struct {
	// Dir 是「当前目录」：自动探测 compose 文件从这里开始，向上逐级查找父目录；
	// 相对路径也相对于它。留空用进程当前目录。
	//
	// 为什么要显式传：本应用是 GUI 进程，当前目录没有意义，
	// 而且要同时加载很多个项目，不能靠 os.Chdir。
	Dir string
	// Files 是显式指定的 compose 文件（-f），可以多个，后面的叠加在前面的上面。
	// 留空时先看环境变量 COMPOSE_FILE，再自动探测（同目录的 compose.override.yaml 会一并叠加）。
	Files []string
	// ProjectName 覆盖项目名（-p）；留空时依次取 COMPOSE_PROJECT_NAME、文件里的 name:、目录名。
	ProjectName string
	// EnvFile 指定 .env 文件（--env-file）；留空时用 Dir 下的 .env（如果有）。
	EnvFile string
	// Environ 是用于变量插值的环境（"KEY=VALUE" 列表）。nil 表示用进程环境。
	// 测试里传一个固定列表，结果才不受运行机器影响。
	Environ []string
}

// loadProject 用 compose-go 加载并规范化 compose 项目。
//
// 这是应用里**唯一**读取 compose 文件的地方：变量插值、.env、include、extends、
// 多文件叠加、命名卷前缀这些语义全部由 compose-go 负责，界面和引擎都从这里拿结果，
// 所以「界面所见」与「实际执行」不会分叉。
//
// 加载时**启用所有 profile**：界面要列出项目的全部服务；
// 真正执行命令时再按 profile 筛选（见 selectServices）。
func loadProject(ctx context.Context, o LoadOptions) (*types.Project, error) {
	fns := []cli.ProjectOptionsFn{cli.WithWorkingDirectory(o.Dir)}
	// 顺序有讲究：WithOsEnv 不会覆盖已经设置的变量，所以显式给定的环境要先设。
	if o.Environ != nil {
		fns = append(fns, cli.WithEnv(o.Environ))
	} else {
		fns = append(fns, cli.WithOsEnv)
	}
	fns = append(fns, cli.WithConfigFileEnv, cli.WithDefaultConfigPath)
	if o.EnvFile != "" {
		fns = append(fns, cli.WithEnvFiles(absIn(o.Dir, o.EnvFile)))
	} else {
		fns = append(fns, cli.WithEnvFiles())
	}
	fns = append(fns, cli.WithDotEnv)
	if o.ProjectName != "" {
		// WithName 要求传入已规范化的名字；这里把用户输入规范化，规范化后为空才算无效。
		name := loader.NormalizeProjectName(o.ProjectName)
		if name == "" {
			return nil, fmt.Errorf("cannot derive a valid project name from '%s'", o.ProjectName)
		}
		fns = append(fns, cli.WithName(name))
	}
	fns = append(fns, cli.WithProfiles([]string{"*"}))

	files := make([]string, 0, len(o.Files))
	for _, f := range o.Files {
		files = append(files, absIn(o.Dir, f))
	}
	opts, err := cli.NewProjectOptions(files, fns...)
	if err != nil {
		return nil, err
	}
	if len(opts.ConfigPaths) == 0 {
		return nil, fmt.Errorf("no compose file found (looked for compose.yaml / compose.yml / " +
			"docker-compose.yml / docker-compose.yaml in this directory and parents)")
	}
	return cli.ProjectFromOptions(ctx, opts)
}

// absIn 把相对路径解析到 dir 下；dir 为空时相对进程当前目录。
func absIn(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if dir == "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return p
		}
		return abs
	}
	return filepath.Join(dir, p)
}

// imageName 返回服务使用的镜像名。只写了 build 的服务用 <项目>-<服务> 作为镜像名，
// 这是 `docker compose` 的约定。
func imageName(p *types.Project, s types.ServiceConfig) string {
	if s.Image != "" {
		return s.Image
	}
	return p.Name + "-" + s.Name
}

// containerName 返回服务第 index 个副本的容器名。
func containerName(p *types.Project, s types.ServiceConfig, index int) string {
	if s.ContainerName != "" {
		return s.ContainerName
	}
	return fmt.Sprintf("%s-%s-%d", p.Name, s.Name, index)
}

// serviceNames 返回按字母序排列的服务名。compose-go 的 Services 是 map，遍历顺序随机，
// 凡是会影响输出顺序的地方都必须先排序，否则界面和日志每次都不一样。
func serviceNames(p *types.Project) []string {
	names := make([]string, 0, len(p.Services))
	for n := range p.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sortedServices 按依赖顺序（被依赖的在前）返回服务。
//
// names 非空时只选这些服务，并递归带上它们的依赖——但 `required: false` 的依赖不会因此被拉起，
// 除非它自己也被选中。依赖成环时返回错误。
//
// 为什么不用 compose-go 的 ForEachService：它对彼此无依赖的服务按 map 顺序遍历，
// 输出不稳定。下面这段拓扑排序只有几十行，换来确定的顺序。
func sortedServices(p *types.Project, names []string) ([]types.ServiceConfig, error) {
	selected := map[string]bool{}
	if len(names) == 0 {
		for n := range p.Services {
			selected[n] = true
		}
	} else {
		stack := append([]string(nil), names...)
		for _, n := range names {
			selected[n] = true
		}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			svc, ok := p.Services[n]
			if !ok {
				continue
			}
			for dep, cfg := range svc.DependsOn {
				if !cfg.Required || selected[dep] {
					continue
				}
				selected[dep] = true
				stack = append(stack, dep)
			}
		}
	}

	var order []types.ServiceConfig
	state := map[string]int{} // 0 访问中，1 已完成
	var visit func(name string, chain []string) error
	visit = func(name string, chain []string) error {
		svc, ok := p.Services[name]
		if !ok || !selected[name] {
			return nil
		}
		if st, seen := state[name]; seen {
			if st == 0 {
				return fmt.Errorf("circular depends_on: %s", strings.Join(append(append([]string(nil), chain...), name), " -> "))
			}
			return nil
		}
		state[name] = 0
		deps := make([]string, 0, len(svc.DependsOn))
		for d := range svc.DependsOn {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			if err := visit(dep, append(append([]string(nil), chain...), name)); err != nil {
				return err
			}
		}
		state[name] = 1
		order = append(order, svc)
		return nil
	}

	sorted := make([]string, 0, len(selected))
	for n := range selected {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		if err := visit(n, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// selectServices 决定一次命令要处理哪些服务：按名字选择（空表示全部）、带上依赖、
// 再按 profile 筛选。显式点名的服务即使带 profile 也会被选中。
func selectServices(p *types.Project, names, profiles []string) ([]types.ServiceConfig, error) {
	for _, n := range names {
		if _, ok := p.Services[n]; !ok {
			return nil, fmt.Errorf("no such service: %s", n)
		}
	}
	ordered, err := sortedServices(p, names)
	if err != nil {
		return nil, err
	}
	var out []types.ServiceConfig
	for _, s := range ordered {
		if len(s.Profiles) > 0 && !s.HasProfile(profiles) && !contains(names, s.Name) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// FindComposeFile 返回目录里第一个命中的 compose 文件（完整路径），没有返回空串。
func FindComposeFile(dir string) string {
	for _, name := range cli.DefaultFileNames {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// IsComposeDir 判断目录下是否有 compose 文件。
func IsComposeDir(dir string) bool { return FindComposeFile(dir) != "" }
