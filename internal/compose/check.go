package compose

import (
	"fmt"
	"os"
	"sort"

	"github.com/compose-spec/compose-go/v2/types"
)

// checkProject 检查项目里 wslc 做不到的事情。
//
// compose-go 只保证文件符合 Compose 规范；规范里有些写法 wslc 根本实现不了。
// 这里分两档处理：
//   - 能忽略的字段（privileged、devices ……）→ 告警，照常运行；
//   - 忽略了就会悄悄做错事的用法（匿名卷、非文件来源的 secret、IPv6 网络 ……）→ 报错，
//     让用户在运行前就知道，而不是起来之后行为不对。
func checkProject(p *types.Project) (warnings []string, err error) {
	// 网络、secrets、configs 是项目级的，先查，错误信息里不带服务名。
	if err := checkNetworks(p); err != nil {
		return nil, err
	}
	if err := checkFileResources(p); err != nil {
		return nil, err
	}

	for _, name := range serviceNames(p) {
		s := p.Services[name]
		w, err := checkService(p, s)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, w...)
	}
	return warnings, nil
}

// unsupportedFlag 描述一个「接受但忽略」的字段。
type unsupportedFlag struct {
	key     string
	message string
	set     func(s types.ServiceConfig) bool
}

// 顺序固定：告警出现的顺序要稳定。
var unsupportedFlags = []unsupportedFlag{
	{"privileged", "privileged mode is not supported by wslc", func(s types.ServiceConfig) bool { return s.Privileged }},
	{"devices", "device mapping is not supported by wslc (see 'gpus' for GPUs)", func(s types.ServiceConfig) bool { return len(s.Devices) > 0 }},
	{"sysctls", "sysctls are not supported by wslc", func(s types.ServiceConfig) bool { return len(s.Sysctls) > 0 }},
	{"init", "init is not supported by wslc", func(s types.ServiceConfig) bool { return s.Init != nil && *s.Init }},
	{"pid", "pid mode is not supported by wslc", func(s types.ServiceConfig) bool { return s.Pid != "" }},
	{"ipc", "ipc mode is not supported by wslc", func(s types.ServiceConfig) bool { return s.Ipc != "" }},
	{"read_only", "read_only rootfs is not supported by wslc", func(s types.ServiceConfig) bool { return s.ReadOnly }},
	{"security_opt", "security_opt is not supported by wslc", func(s types.ServiceConfig) bool { return len(s.SecurityOpt) > 0 }},
	{"logging", "logging drivers are not configurable with wslc", func(s types.ServiceConfig) bool { return s.Logging != nil || s.LogDriver != "" }},
}

func checkService(p *types.Project, s types.ServiceConfig) (warnings []string, err error) {
	for _, u := range unsupportedFlags {
		if u.set(s) {
			warnings = append(warnings, fmt.Sprintf("%s: %s (ignoring '%s')", s.Name, u.message, u.key))
		}
	}

	if s.Build != nil && len(s.Build.Secrets) > 0 {
		return nil, fmt.Errorf("service '%s': build.secrets is not supported: 'wslc build' has no --secret option "+
			"(do not pass credentials as build args)", s.Name)
	}

	for _, m := range s.Volumes {
		switch m.Type {
		case types.VolumeTypeBind, types.VolumeTypeVolume, types.VolumeTypeTmpfs:
		default:
			return nil, fmt.Errorf("service '%s': volume type '%s' is not supported by wslc (%s)", s.Name, m.Type, m.Target)
		}
		// 没有来源的命名卷就是匿名卷：wslc 不支持。
		if m.Type == types.VolumeTypeVolume && m.Source == "" {
			return nil, fmt.Errorf("service '%s': anonymous volumes are not supported by wslc: '%s'; "+
				"name the volume or use a bind mount", s.Name, m.Target)
		}
		if b := m.Bind; b != nil {
			if b.Propagation != "" {
				warnings = append(warnings, mountWarning(s.Name, m.Target, "bind.propagation"))
			}
			if b.SELinux != "" {
				warnings = append(warnings, mountWarning(s.Name, m.Target, "bind.selinux"))
			}
		}
		if v := m.Volume; v != nil {
			if v.NoCopy {
				warnings = append(warnings, mountWarning(s.Name, m.Target, "volume.nocopy"))
			}
			if v.Subpath != "" {
				warnings = append(warnings, mountWarning(s.Name, m.Target, "volume.subpath"))
			}
		}
	}

	if r := s.Restart; r != "" && r != "no" && r != "always" {
		warnings = append(warnings, fmt.Sprintf("%s: restart policy '%s' is not supported by wslc "+
			"(only 'always' and 'no'); ignoring it", s.Name, r))
	}
	if len(s.ExtraHosts) > 0 {
		warnings = append(warnings, fmt.Sprintf("%s: extra_hosts is applied after start; the entrypoint may not observe it", s.Name))
	}

	// secrets / configs 的属主和权限无法通过文件挂载实现。
	for _, kind := range []struct {
		name string
		refs []types.ServiceSecretConfig
	}{{"secret", s.Secrets}, {"config", configRefsAsSecrets(s.Configs)}} {
		for _, ref := range kind.refs {
			var ignored []string
			if ref.UID != "" {
				ignored = append(ignored, "uid")
			}
			if ref.GID != "" {
				ignored = append(ignored, "gid")
			}
			if ref.Mode != nil {
				ignored = append(ignored, "mode")
			}
			if len(ignored) > 0 {
				warnings = append(warnings, fmt.Sprintf("%s: %s '%s': %s cannot be enforced by wslc file mounts and will be ignored",
					s.Name, kind.name, ref.Source, joinComma(ignored)))
			}
		}
	}

	// 等待 healthy 的依赖不能关掉自己的健康检查，否则永远等不到。
	for dep, cfg := range s.DependsOn {
		if cfg.Condition != types.ServiceConditionHealthy {
			continue
		}
		if target, ok := p.Services[dep]; ok && target.HealthCheck != nil && target.HealthCheck.Disable {
			return nil, fmt.Errorf("service '%s': depends_on '%s' is service_healthy but '%s' disables its healthcheck",
				s.Name, dep, dep)
		}
	}
	return warnings, nil
}

func mountWarning(service, target, option string) string {
	return fmt.Sprintf("%s: mount %s: %s is not supported by wslc (ignoring it)", service, target, option)
}

// configRefsAsSecrets 把 config 引用转成 secret 引用的类型，复用同一段检查。
// 两者字段完全相同，只是 compose-go 定义成了两个类型。
func configRefsAsSecrets(refs []types.ServiceConfigObjConfig) []types.ServiceSecretConfig {
	out := make([]types.ServiceSecretConfig, len(refs))
	for i, r := range refs {
		out[i] = types.ServiceSecretConfig(r)
	}
	return out
}

func joinComma(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ", "
		}
		out += it
	}
	return out
}

// checkNetworks 检查顶层网络声明：wslc 只支持 IPv4、不可 attach、至多一个子网。
func checkNetworks(p *types.Project) error {
	keys := make([]string, 0, len(p.Networks))
	for k := range p.Networks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		n := p.Networks[key]
		if (n.EnableIPv6 != nil && *n.EnableIPv6) || (n.EnableIPv4 != nil && !*n.EnableIPv4) || n.Attachable {
			return fmt.Errorf("networks.%s: WSLC supports IPv4 networks without attachable mode", key)
		}
		if (n.Ipam.Driver != "" && n.Ipam.Driver != "default") || len(n.Ipam.Options) > 0 {
			return fmt.Errorf("networks.%s.ipam: custom IPAM drivers/options are not supported by WSLC", key)
		}
		if len(n.Ipam.Config) > 1 {
			return fmt.Errorf("networks.%s.ipam.config: WSLC supports one IPv4 subnet per network", key)
		}
		for _, pool := range n.Ipam.Config {
			if pool != nil && len(pool.AuxiliaryAddresses) > 0 {
				return fmt.Errorf("networks.%s.ipam.config: unsupported setting (including aux_addresses)", key)
			}
		}
	}
	return nil
}

// checkFileResources 检查顶层 secrets / configs：wslc 只能把文件挂进容器，
// 所以只支持 file 来源；文件必须存在。
func checkFileResources(p *types.Project) error {
	check := func(kind string, label string, res map[string]types.FileObjectConfig) error {
		keys := make([]string, 0, len(res))
		for k := range res {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			r := res[key]
			switch {
			case bool(r.External):
				return fmt.Errorf("%s '%s': external %s are not supported by wslc; use a file", label, key, kind)
			case r.Environment != "" || r.Content != "":
				return fmt.Errorf("%s '%s': only file-backed %s are supported by wslc; write the "+
					"value to a protected host file and use 'file'", label, key, kind)
			case r.File == "":
				return fmt.Errorf("%s '%s': only file-backed %s are supported by wslc", label, key, kind)
			}
			if st, err := os.Stat(r.File); err != nil || st.IsDir() {
				return fmt.Errorf("%s '%s': file not found: %s", label, key, r.File)
			}
		}
		return nil
	}
	secrets := make(map[string]types.FileObjectConfig, len(p.Secrets))
	for k, v := range p.Secrets {
		secrets[k] = types.FileObjectConfig(v)
	}
	if err := check("secrets", "secret", secrets); err != nil {
		return err
	}
	configs := make(map[string]types.FileObjectConfig, len(p.Configs))
	for k, v := range p.Configs {
		configs[k] = types.FileObjectConfig(v)
	}
	return check("configs", "config", configs)
}
