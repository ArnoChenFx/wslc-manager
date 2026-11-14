package compose

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/compose-spec/compose-go/v2/types"
)

// down 停止并删除项目的容器和网络；opt.Volumes 为 true 时同时删除命名卷。
//
// 与 `docker compose down` 一致：删除的是**项目名下所有容器**（包括 compose 文件里已经删掉的服务留下的），
// 而不只是文件里现在有的服务。网络只删归属确认过的：
// 标签里的项目名对得上才删，对不上说明它可能是别人建的同名网络，保留并提示。
func (e *engine) down(ctx context.Context, p *types.Project, opt ActionOptions) error {
	timeout := strconv.Itoa(opt.stopTimeout())
	containers, err := e.listProjectContainers(ctx, p.Name, true)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if c.Running {
			e.info("Stopping %s ...", c.Name)
			if _, err := e.run(ctx, "stop", "-t", timeout, c.Name); err != nil {
				return err
			}
		}
		e.info("Removing %s ...", c.Name)
		if _, err := e.run(ctx, "remove", "-f", c.Name); err != nil {
			return err
		}
	}

	existingNets, err := e.names(ctx, "network")
	if err != nil {
		return err
	}
	for _, key := range sortedKeys(p.Networks) {
		n := p.Networks[key]
		if bool(n.External) || !existingNets[n.Name] {
			continue
		}
		actual, err := e.inspect(ctx, "network", n.Name)
		if err != nil {
			return err
		}
		if actual == nil || str(asMap(actual["Labels"])[LabelProject]) != p.Name {
			e.info("warning: preserving network %s (project ownership is unverified)", n.Name)
			continue
		}
		e.info("Removing network %s", n.Name)
		// 网络还被别的容器占用时删除会失败：不致命，提示即可。
		if _, err := e.run(ctx, "network", "remove", n.Name); err != nil {
			e.info("warning: could not remove network %s (still in use?)", n.Name)
		}
	}

	if opt.Volumes {
		existingVols, err := e.names(ctx, "volume")
		if err != nil {
			return err
		}
		for _, key := range sortedKeys(p.Volumes) {
			v := p.Volumes[key]
			if bool(v.External) || !existingVols[v.Name] {
				continue
			}
			e.info("Removing volume %s", v.Name)
			if _, err := e.run(ctx, "volume", "remove", v.Name); err != nil {
				e.info("warning: could not remove volume %s", v.Name)
			}
		}
	}
	return nil
}

// lifecycle 实现 start / stop / restart：对项目已有的容器操作，不创建新容器。
func (e *engine) lifecycle(ctx context.Context, p *types.Project, action Action, opt ActionOptions) error {
	// stop 只需要运行中的；start / restart 要包括已停止的。
	containers, err := e.listProjectContainers(ctx, p.Name, action == ActionStart || action == ActionRestart)
	if err != nil {
		return err
	}
	if len(opt.Services) > 0 {
		var picked []container
		for _, c := range containers {
			if contains(opt.Services, c.Service) {
				picked = append(picked, c)
			}
		}
		containers = picked
	}
	if len(containers) == 0 {
		return errors.New("no containers found")
	}
	timeout := strconv.Itoa(opt.stopTimeout())
	for _, c := range containers {
		if (action == ActionStop || action == ActionRestart) && c.Running {
			e.info("Stopping %s ...", c.Name)
			if _, err := e.run(ctx, "stop", "-t", timeout, c.Name); err != nil {
				return err
			}
		}
		if action == ActionStart || action == ActionRestart {
			e.info("Starting %s ...", c.Name)
			if _, err := e.runRetried(ctx, "start", c.Name); err != nil {
				return err
			}
			// 已经从 compose 文件里删掉的服务留下的容器没有对应的服务定义，跳过 extra_hosts。
			if s, ok := p.Services[c.Service]; ok {
				if err := e.applyExtraHosts(ctx, c.Name, s.ExtraHosts); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// pull 拉取所选服务的镜像。只有 build 的服务没有镜像可拉，跳过。
func (e *engine) pull(ctx context.Context, p *types.Project, opt ActionOptions) error {
	services, err := selectServices(p, opt.Services, opt.Profiles)
	if err != nil {
		return err
	}
	for _, s := range services {
		if s.Image == "" {
			continue
		}
		e.info("Pulling %s ...", s.Image)
		if _, err := e.runStream(ctx, e.out, "pull", s.Image); err != nil {
			return err
		}
	}
	return nil
}

// build 构建所选服务里带 build 段的镜像。一个都没有时报错，因为用户显然以为会有东西被构建。
func (e *engine) build(ctx context.Context, p *types.Project, opt ActionOptions) error {
	services, err := selectServices(p, opt.Services, opt.Profiles)
	if err != nil {
		return err
	}
	built := 0
	for _, s := range services {
		if s.Build == nil {
			continue
		}
		e.info("Building %s ...", s.Name)
		if _, err := e.runStream(ctx, e.out, buildArgs(p, s, opt.NoCache)...); err != nil {
			return err
		}
		built++
	}
	if built == 0 {
		return fmt.Errorf("no services with a build section")
	}
	return nil
}
