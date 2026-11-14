package compose

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// pollInterval 是等待依赖条件时的轮询间隔。
const pollInterval = time.Second

// evaluateCondition 判断一个依赖容器是否满足条件。
// 返回 (已满足, 还要继续等, 错误)：错误表示「这个条件永远不可能满足了」，不必再等。
func evaluateCondition(condition, dep string, data map[string]any) (satisfied bool, err error) {
	state := asMap(data["State"])
	running := boolOf(state["Running"])
	name := strings.TrimPrefix(firstNonEmpty(str(data["Name"]), str(data["Id"]), "?"), "/")
	exit := state["ExitCode"]

	switch condition {
	case types.ServiceConditionHealthy:
		if !running {
			return false, &WslcError{Msg: fmt.Sprintf("dependency '%s' failed to start: container %s is not running (exit code %v)", dep, name, exit)}
		}
		health := asMap(state["Health"])
		if len(health) == 0 {
			return false, &WslcError{Msg: fmt.Sprintf("dependency '%s' has no healthcheck (container %s); define one in the "+
				"compose file or the image to use condition: service_healthy", dep, name)}
		}
		switch str(health["Status"]) {
		case "healthy":
			return true, nil
		case "unhealthy":
			return false, &WslcError{Msg: fmt.Sprintf("dependency '%s' is unhealthy (container %s)", dep, name)}
		}
		return false, nil // starting：继续等
	case types.ServiceConditionCompletedSuccessfully:
		if running || str(state["Status"]) == "created" || str(state["Status"]) == "starting" {
			return false, nil
		}
		if code, ok := exit.(float64); ok && code == 0 {
			return true, nil
		}
		return false, &WslcError{Msg: fmt.Sprintf("dependency '%s' did not complete successfully: container %s exited with code %v", dep, name, exit)}
	}
	return true, nil
}

// dependencyContainers 返回支撑服务 dep 的容器的 inspect 数据（任何状态）。
func (e *engine) dependencyContainers(ctx context.Context, project, dep string) ([]map[string]any, error) {
	entries, err := e.queryJSON(ctx, "list", "-a", "--format", "json", "-f", "label="+LabelProject+"="+project)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, entry := range entries {
		ident := firstNonEmpty(str(entry["ID"]), str(entry["Names"]))
		if ident == "" {
			continue
		}
		data, err := e.inspect(ctx, "", ident)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue
		}
		if l := labelsOf(data); l[LabelProject] == project && l[LabelService] == dep {
			out = append(out, data)
		}
	}
	return out, nil
}

// waitForDependencies 阻塞到服务 s 的每个非默认依赖条件都满足为止。
//
// 不在 selected 里的依赖（其他 profile、没被选中）和副本数为 0 的依赖会被跳过。
// timeout 限制每个条件的等待时间，0 表示不限。
// 可选依赖（required: false）失败只告警，不影响当前服务。
func (e *engine) waitForDependencies(ctx context.Context, p *types.Project, s types.ServiceConfig,
	selected []types.ServiceConfig, replicas map[string]int, timeout time.Duration) error {
	names := map[string]bool{}
	for _, sel := range selected {
		names[sel.Name] = true
	}
	for _, dep := range sortedKeys(s.DependsOn) {
		cfg := s.DependsOn[dep]
		if cfg.Condition == "" || cfg.Condition == types.ServiceConditionStarted {
			continue
		}
		if !names[dep] || replicas[dep] == 0 {
			continue
		}
		if cfg.Condition == types.ServiceConditionHealthy {
			e.info("Waiting for %s to be healthy ...", dep)
		} else {
			e.info("Waiting for %s to complete ...", dep)
		}
		if err := e.waitOne(ctx, p.Name, dep, cfg.Condition, timeout); err != nil {
			if cfg.Required {
				return err
			}
			e.info("warning: optional dependency '%s' (required: false): %v; continuing", dep, err)
		}
	}
	return nil
}

func (e *engine) waitOne(ctx context.Context, project, dep, condition string, timeout time.Duration) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		containers, err := e.dependencyContainers(ctx, project, dep)
		if err != nil {
			return err
		}
		if len(containers) == 0 {
			return &WslcError{Msg: fmt.Sprintf("dependency '%s' has no container to wait for", dep)}
		}
		all := true
		for _, data := range containers {
			ok, err := evaluateCondition(condition, dep, data)
			if err != nil {
				return err
			}
			all = all && ok
		}
		if all {
			return nil
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return &WslcError{Msg: fmt.Sprintf("timed out after %s waiting for '%s' (%s)", timeout, dep, condition)}
		}
		if err := e.sleep(ctx, pollInterval); err != nil {
			return err
		}
	}
}
