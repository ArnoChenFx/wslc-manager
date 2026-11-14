package compose

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// container 是项目里一个容器的当前状态：`wslc list` 的一行，加上 inspect 的详细数据。
type container struct {
	ID      string
	Name    string
	Image   string
	Command string
	Service string
	// Index 是副本序号（从 1 开始）。
	Index   int
	Hash    string
	Running bool
	Created string
	Status  string
	// Ports 是 `wslc list` 给出的端口文本。
	Ports string

	// 以下来自 inspect，用来判断「现有容器与期望配置是否一致」。
	Networks      map[string]any
	NetworkMode   string
	RestartPolicy string
	CapAdd        []string
	CapDrop       []string
}

// field 从 inspect 数据里按路径取值，路径上任何一层缺失都返回 nil。
func field(data map[string]any, path ...string) any {
	var cur any = data
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strList(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// labelsOf 取出 inspect 数据里的标签。不同位置都可能有：Config.Labels 或顶层 Labels。
func labelsOf(data map[string]any) map[string]string {
	out := map[string]string{}
	for _, v := range []any{field(data, "Config", "Labels"), data["Labels"]} {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, x := range m {
				out[k] = str(x)
			}
			return out
		}
	}
	return out
}

// listProjectContainers 返回项目名下的容器，按（服务名，序号）排序。
// allStates 为 true 时包含已停止的容器——up 要靠它发现「已存在但没在跑」的容器，
// 否则会和它占用的名字冲突；logs 也要看崩溃容器的日志。
func (e *engine) listProjectContainers(ctx context.Context, project string, allStates bool) ([]container, error) {
	args := []string{"list", "--format", "json", "-f", "label=" + LabelProject + "=" + project}
	if allStates {
		args = []string{"list", "-a", "--format", "json", "-f", "label=" + LabelProject + "=" + project}
	}
	entries, err := e.queryJSON(ctx, args...)
	if err != nil {
		return nil, err
	}
	var out []container
	for _, entry := range entries {
		id, name := str(entry["ID"]), str(entry["Names"])
		ident := id
		if ident == "" {
			ident = name
		}
		if ident == "" {
			continue
		}
		data, err := e.inspect(ctx, "", ident)
		if err != nil {
			return nil, err
		}
		if data == nil {
			data = map[string]any{}
		}
		labels := labelsOf(data)
		idx, _ := strconv.Atoi(labels[LabelIndex])
		if idx == 0 {
			idx = 1
		}
		state, _ := field(data, "State").(map[string]any)
		running, _ := state["Running"].(bool)
		status := str(entry["Status"])
		if status == "" {
			status = str(state["Status"])
		}
		if name == "" {
			name = strings.TrimPrefix(str(data["Name"]), "/")
		}
		out = append(out, container{
			ID: id, Name: name,
			Image:         firstNonEmpty(str(entry["Image"]), str(data["Image"])),
			Command:       str(entry["Command"]),
			Service:       labels[LabelService],
			Index:         idx,
			Hash:          labels[LabelConfigHash],
			Running:       running,
			Created:       humanCreated(firstNonEmpty(str(entry["CreatedAt"]), str(data["Created"]))),
			Status:        firstNonEmpty(status, "unknown"),
			Ports:         str(entry["Ports"]),
			Networks:      asMap(field(data, "NetworkSettings", "Networks")),
			NetworkMode:   str(field(data, "HostConfig", "NetworkMode")),
			RestartPolicy: str(field(data, "HostConfig", "RestartPolicy", "Name")),
			CapAdd:        strList(field(data, "HostConfig", "CapAdd")),
			CapDrop:       strList(field(data, "HostConfig", "CapDrop")),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Index < out[j].Index
	})
	return out, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func firstNonEmpty(items ...string) string {
	for _, s := range items {
		if s != "" {
			return s
		}
	}
	return ""
}

// createdLayouts 是 wslc 可能给出的创建时间格式：
// `wslc list` 是 "2026-11-14 16:49:53 +0800 GMT+8"（末尾有时区缩写），inspect 是 RFC3339。
var createdLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05 -0700"}

// humanCreated 把创建时间换算成「3 hours ago」这样的相对时间；解析不了就原样返回。
func humanCreated(value string) string { return humanCreatedAt(value, time.Now()) }

func humanCreatedAt(value string, now time.Time) string {
	if value == "" {
		return ""
	}
	candidates := []string{value}
	// 去掉末尾的时区缩写（"GMT+8"）再试一次。
	if i := strings.LastIndex(value, " "); i > 0 {
		candidates = append(candidates, value[:i])
	}
	for _, c := range candidates {
		for _, layout := range createdLayouts {
			if t, err := time.Parse(layout, c); err == nil {
				secs := int(now.Sub(t).Seconds())
				if secs < 0 {
					secs = 0
				}
				return humanDuration(secs) + " ago"
			}
		}
	}
	return value
}

// humanDuration 把秒数换算成人话，规则与 `docker compose ps` 使用的 go-units 相同。
func humanDuration(seconds int) string {
	switch {
	case seconds < 1:
		return "Less than a second"
	case seconds == 1:
		return "1 second"
	case seconds < 60:
		return fmt.Sprintf("%d seconds", seconds)
	}
	minutes := seconds / 60
	switch {
	case minutes == 1:
		return "About a minute"
	case minutes < 60:
		return fmt.Sprintf("%d minutes", minutes)
	}
	hours := int(float64(seconds)/3600 + 0.5)
	switch {
	case hours == 1:
		return "About an hour"
	case hours < 48:
		return fmt.Sprintf("%d hours", hours)
	case hours < 24*7*2:
		return fmt.Sprintf("%d days", hours/24)
	case hours < 24*30*2:
		return fmt.Sprintf("%d weeks", hours/24/7)
	case hours < 24*365*2:
		return fmt.Sprintf("%d months", hours/24/30)
	}
	return fmt.Sprintf("%d years", seconds/3600/24/365)
}
