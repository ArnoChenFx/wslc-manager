package compose

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// networkConflicts 比较「已存在的网络」与「compose 要求的设置」，返回冲突项。
//
// 已存在的资源**从不被改写**来迎合新的创建参数：悄悄改动用户的网络可能打断别的容器。
// 发现不一致就报错，让用户自己决定换名字还是删掉没用的网络。
func networkConflicts(n types.NetworkConfig, actual map[string]any) []string {
	var conflicts []string
	if n.Driver != "" && str(actual["Driver"]) != n.Driver {
		conflicts = append(conflicts, "driver")
	}
	// 外部网络只有显式要求 internal 时才校验这一项。
	if (!bool(n.External) || n.Internal) && boolOf(actual["Internal"]) != n.Internal {
		conflicts = append(conflicts, "internal")
	}
	opts := asMap(actual["Options"])
	for _, k := range sortedKeys(n.DriverOpts) {
		if str(opts[k]) != n.DriverOpts[k] {
			conflicts = append(conflicts, "driver_opts."+k)
		}
	}
	labels := asMap(actual["Labels"])
	for _, k := range sortedKeys(n.Labels) {
		if str(labels[k]) != n.Labels[k] {
			conflicts = append(conflicts, "labels."+k)
		}
	}
	for _, pool := range n.Ipam.Config {
		if pool == nil || pool.Subnet == "" {
			continue
		}
		var found map[string]any
		for _, rec := range anyList(asMap(actual["IPAM"])["Config"]) {
			if m := asMap(rec); str(m["Subnet"]) == pool.Subnet {
				found = m
			}
		}
		switch {
		case found == nil:
			conflicts = append(conflicts, "ipam.subnet")
		default:
			if pool.Gateway != "" && str(found["Gateway"]) != pool.Gateway {
				conflicts = append(conflicts, "ipam.gateway")
			}
			if pool.IPRange != "" && str(found["IPRange"]) != pool.IPRange {
				conflicts = append(conflicts, "ipam.ip_range")
			}
		}
	}
	return conflicts
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

// verifyNetwork 确认网络存在且设置与 compose 要求一致，返回它的 inspect 数据。
func (e *engine) verifyNetwork(ctx context.Context, n types.NetworkConfig) (map[string]any, error) {
	actual, err := e.inspect(ctx, "network", n.Name)
	if err != nil {
		return nil, err
	}
	if actual == nil || str(actual["Name"]) != n.Name {
		return nil, &WslcError{Msg: fmt.Sprintf("network '%s': inspect did not return a usable network identity", n.Name)}
	}
	if c := networkConflicts(n, actual); len(c) > 0 {
		return nil, &WslcError{Msg: fmt.Sprintf("network '%s' conflicts with the requested settings: %s; "+
			"use another name or remove the unused network explicitly", n.Name, strings.Join(c, ", "))}
	}
	return actual, nil
}

// validateStaticAddresses 检查服务要求的静态 IPv4 在网络里是否可用：
// 必须落在某个子网内，且不是网络地址、广播地址或网关。
// 网络还不存在（dry-run 预览一个将要创建的网络）时跳过。
func validateStaticAddresses(p *types.Project, s types.ServiceConfig, networks map[string]map[string]any) error {
	for _, n := range serviceNetworks(p, s) {
		want := n.ipv4()
		actual, ok := networks[n.name]
		if want == "" || !ok {
			continue
		}
		addr, err := netip.ParseAddr(want)
		if err != nil {
			return &WslcError{Msg: fmt.Sprintf("service '%s': static IPv4 %s is not valid", s.Name, want)}
		}
		usable := false
		for _, rec := range anyList(asMap(actual["IPAM"])["Config"]) {
			m := asMap(rec)
			prefix, err := netip.ParsePrefix(str(m["Subnet"]))
			if err != nil || !prefix.Contains(addr) {
				continue
			}
			netAddr, bcast := subnetBounds(prefix)
			if addr != netAddr && addr != bcast && want != str(m["Gateway"]) {
				usable = true
			}
		}
		if !usable {
			return &WslcError{Msg: fmt.Sprintf("service '%s': static IPv4 %s is not usable in network '%s'", s.Name, want, n.name)}
		}
	}
	return nil
}

// subnetBounds 返回 IPv4 网段的网络地址与广播地址。
func subnetBounds(p netip.Prefix) (network, broadcast netip.Addr) {
	network = p.Masked().Addr()
	b := network.As4()
	for i := 0; i < 32-p.Bits(); i++ {
		b[3-i/8] |= 1 << (i % 8)
	}
	return network, netip.AddrFrom4(b)
}

// networkMatches 判断现有容器的网络接入是否与服务期望一致（别名、静态 IP、主网络）。
// 不一致时 up 会重建容器。
func networkMatches(p *types.Project, s types.ServiceConfig, c container) (bool, error) {
	if s.NetworkMode != "" {
		return true, nil
	}
	nets := serviceNetworks(p, s)
	if len(nets) == 0 {
		return true, nil
	}
	if c.Networks == nil {
		// 取不到端点信息：单网络且没有静态 IP 时没什么可核对的；否则无法确认，报错而不是猜。
		if len(nets) > 1 || nets[0].ipv4() != "" {
			return false, &WslcError{Msg: fmt.Sprintf("cannot verify network settings for '%s'", c.Name)}
		}
		return true, nil
	}
	if c.NetworkMode != "" && c.NetworkMode != nets[0].name {
		return false, nil
	}
	for _, n := range nets {
		endpoint := asMap(c.Networks[n.name])
		if endpoint == nil {
			return false, nil
		}
		have := map[string]bool{}
		for _, a := range strList(endpoint["Aliases"]) {
			have[a] = true
		}
		for _, a := range n.aliases(s) {
			if !have[a] {
				return false, nil
			}
		}
		requested := str(asMap(endpoint["IPAMConfig"])["IPv4Address"])
		if n.ipv4() != requested {
			return false, nil
		}
		if n.ipv4() != "" && c.Running && str(endpoint["IPAddress"]) != n.ipv4() {
			return false, nil
		}
	}
	return true, nil
}

// verifyContainerNetworks 在容器创建/启动之后复核网络设置真的生效了。
// 只有多网络或有静态 IP 时才需要：这两种情况依赖 wslc 对 --network-alias / --ip 的实现，
// 值得确认；单网络没有这些设置时无事可验。
func (e *engine) verifyContainerNetworks(ctx context.Context, p *types.Project, s types.ServiceConfig, name string) error {
	if e.dryRun || s.NetworkMode != "" {
		return nil
	}
	nets := serviceNetworks(p, s)
	hasStatic := false
	for _, n := range nets {
		hasStatic = hasStatic || n.ipv4() != ""
	}
	if len(nets) < 2 && !hasStatic {
		return nil
	}
	data, err := e.inspect(ctx, "", name)
	if err != nil {
		return err
	}
	if data == nil {
		return &WslcError{Msg: fmt.Sprintf("container '%s' did not apply the requested network aliases/static IPv4 settings", name)}
	}
	state := asMap(data["State"])
	cur := container{
		Name:        name,
		Running:     boolOf(state["Running"]),
		Networks:    asMap(field(data, "NetworkSettings", "Networks")),
		NetworkMode: str(field(data, "HostConfig", "NetworkMode")),
	}
	ok, err := networkMatches(p, s, cur)
	if err != nil {
		return err
	}
	if !ok {
		return &WslcError{Msg: fmt.Sprintf("container '%s' did not apply the requested network aliases/static IPv4 settings", name)}
	}
	return nil
}
