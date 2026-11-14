package wslc

import (
	"context"
	"fmt"
	"strings"
)

// ListContainers 列出容器。all 为 true 时包含已停止的容器。
func (c *Client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	args := []string{"list", "--format", "json"}
	if all {
		args = append(args, "--all")
	}
	var out []Container
	if err := c.runJSON(ctx, &out, args...); err != nil {
		return nil, err
	}
	// 让顺序稳定：运行中的排前面，其余按名称排。
	sortContainers(out)
	return out, nil
}

// InspectContainer 返回容器的完整 inspect JSON（原始文本）。
func (c *Client) InspectContainer(ctx context.Context, id string) (string, error) {
	res, err := c.RunOK(ctx, "inspect", "-t", "container", id)
	if err != nil {
		return "", err
	}
	return res.Output(), nil
}

// ContainerAction 是可以在容器上执行的生命周期动作。
type ContainerAction string

// 注意：当前 wslc 预览版没有 pause/unpause 命令，因此不提供这两个动作。
const (
	ActionStart   ContainerAction = "start"
	ActionStop    ContainerAction = "stop"
	ActionRestart ContainerAction = "restart"
	ActionKill    ContainerAction = "kill"
	ActionRemove  ContainerAction = "remove"
)

// ActOnContainer 对容器执行一个生命周期动作。
//
// wslc 中 remove 需要 --force 才能删除运行中的容器，这里统一加上，
// 语义与 Docker Desktop 的「删除并强制停止」一致。
func (c *Client) ActOnContainer(ctx context.Context, action ContainerAction, ids ...string) error {
	if len(ids) == 0 {
		return fmt.Errorf("没有指定容器")
	}
	args := []string{string(action)}
	if action == ActionRemove {
		args = append(args, "--force")
	}
	args = append(args, ids...)
	_, err := c.RunOK(ctx, args...)
	return err
}

// Logs 读取容器日志。tail 为 0 表示不限制行数。
func (c *Client) Logs(ctx context.Context, id string, tail int, timestamps bool) (string, error) {
	args := []string{"logs"}
	if tail > 0 {
		args = append(args, "-n", itoa(tail))
	}
	if timestamps {
		args = append(args, "-t")
	}
	args = append(args, id)
	res, err := c.RunOK(ctx, args...)
	if err != nil {
		return res.Combined(), err
	}
	return res.Combined(), nil
}

// Exec 在运行中的容器里执行命令并返回合并输出。
func (c *Client) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	if len(cmd) == 0 {
		return "", fmt.Errorf("没有指定命令")
	}
	args := append([]string{"exec", id}, cmd...)
	res, err := c.RunOK(ctx, args...)
	if err != nil {
		return res.Combined(), err
	}
	return res.Combined(), nil
}

// Stats 返回容器的资源占用表格文本。
//
// wslc stats 默认是流式输出（类似 top），本应用不需要实时刷新，
// 因此直接用 JSON 格式取一次快照，再渲染成表格文本交给上层。
func (c *Client) Stats(ctx context.Context, all bool) (string, error) {
	args := []string{"stats", "--format", "json"}
	if all {
		args = append(args, "--all")
	}
	res, err := c.RunOK(ctx, args...)
	if err != nil {
		return res.Combined(), err
	}
	return res.Stdout, nil
}

// ListImages 列出镜像。all 为 true 时包含中间镜像。
func (c *Client) ListImages(ctx context.Context, all bool) ([]Image, error) {
	args := []string{"images", "--format", "json"}
	if all {
		args = append(args, "--all")
	}
	var out []Image
	if err := c.runJSON(ctx, &out, args...); err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveImage 删除镜像。force 为 true 时强制删除。
func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	args := []string{"rmi"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, id)
	_, err := c.RunOK(ctx, args...)
	return err
}

// PullImage 拉取镜像并返回进度输出。
func (c *Client) PullImage(ctx context.Context, ref string) (string, error) {
	res, err := c.RunOK(ctx, "pull", ref)
	return res.Combined(), err
}

// BuildImage 从 Dockerfile 构建镜像。
//
// tag 为空时不打标签；contextDir 是构建上下文目录。
func (c *Client) BuildImage(ctx context.Context, contextDir, dockerfile, tag string) (string, error) {
	args := []string{"build"}
	if dockerfile != "" {
		args = append(args, "-f", dockerfile)
	}
	if tag != "" {
		args = append(args, "-t", tag)
	}
	args = append(args, absPath(contextDir))
	res, err := c.RunOK(ctx, args...)
	return res.Combined(), err
}

// ListVolumes 列出命名卷。
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	var out []Volume
	if err := c.runJSON(ctx, &out, "volume", "list", "--format", "json"); err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveVolume 删除命名卷。
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	args := []string{"volume", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, name)
	_, err := c.RunOK(ctx, args...)
	return err
}

// CreateVolume 创建命名卷。
func (c *Client) CreateVolume(ctx context.Context, name string) error {
	_, err := c.RunOK(ctx, "volume", "create", name)
	return err
}

// ListNetworks 列出网络。
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var out []Network
	if err := c.runJSON(ctx, &out, "network", "list", "--format", "json"); err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveNetwork 删除网络。
func (c *Client) RemoveNetwork(ctx context.Context, id string, force bool) error {
	args := []string{"network", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, id)
	_, err := c.RunOK(ctx, args...)
	return err
}

// CreateNetwork 创建 bridge 网络。
func (c *Client) CreateNetwork(ctx context.Context, name string) error {
	_, err := c.RunOK(ctx, "network", "create", name)
	return err
}

// Info 读取 wslc 系统信息。
func (c *Client) Info(ctx context.Context) (Info, error) {
	res, err := c.RunOK(ctx, "info")
	if err != nil {
		return Info{}, err
	}
	return ParseInfo(res.Stdout), nil
}

// Version 读取 wslc 版本字符串（如 "wslc 3.0.5.0"）。
func (c *Client) Version(ctx context.Context) (string, error) {
	res, err := c.RunOK(ctx, "--version")
	if err != nil {
		return "", err
	}
	return res.Output(), nil
}

// ListSessions 列出 wslc 会话。
//
// 当前 wslc 预览版的 system session list 不支持 --format json，
// 只能解析表格文本。表格列：ID / 创建者 / PID / 显示名称。
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	res, err := c.RunOK(ctx, "system", "session", "list")
	if err != nil {
		return nil, err
	}
	return ParseSessionsTable(res.Stdout), nil
}

// ParseSessionsTable 解析 `wslc system session list` 的表格输出。
//
// 实际输出只有三列：ID / 创建者 PID / 显示名称。
// 第一行是表头（不同语言的列名不同），直接跳过。
func ParseSessionsTable(out string) []Session {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return nil
	}
	var sessions []Session
	for _, line := range lines[1:] {
		cols := splitTableRow(line)
		if len(cols) < 3 {
			continue
		}
		sessions = append(sessions, Session{
			ID:          cols[0],
			PID:         cols[1],
			DisplayName: cols[2],
		})
	}
	return sessions
}

// splitTableRow 按两个及以上连续空格切分一行表格，
// 避免单空格出现在单元格内容里的情况。
func splitTableRow(line string) []string {
	var cols []string
	var cur strings.Builder
	spaces := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			cols = append(cols, s)
		}
		cur.Reset()
	}
	for _, r := range line {
		if r == ' ' || r == '\t' {
			spaces++
			if spaces == 1 {
				cur.WriteRune(r)
			}
			continue
		}
		if spaces >= 2 {
			flush()
		}
		spaces = 0
		cur.WriteRune(r)
	}
	flush()
	return cols
}

// PruneReport 描述一次清理的结果。
type PruneReport struct {
	Removed int
	Output  string
}

// PruneContainers 清理所有已停止的容器。
func (c *Client) PruneContainers(ctx context.Context) (PruneReport, error) {
	res, err := c.RunOK(ctx, "container", "prune", "--force")
	return PruneReport{Output: res.Combined()}, err
}

// PruneImages 清理未被使用的镜像。
func (c *Client) PruneImages(ctx context.Context) (PruneReport, error) {
	res, err := c.RunOK(ctx, "image", "prune", "--force")
	return PruneReport{Output: res.Combined()}, err
}

// sortContainers 让容器列表顺序稳定：运行中优先，其次按名称。
// 用插入排序即可，列表通常很小。
func sortContainers(cs []Container) {
	less := func(a, b Container) bool {
		if a.IsRunning() != b.IsRunning() {
			return a.IsRunning()
		}
		return strings.ToLower(a.Names) < strings.ToLower(b.Names)
	}
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && less(cs[j], cs[j-1]); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}
