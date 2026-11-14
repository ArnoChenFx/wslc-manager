package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// 容器、网络、卷上的标签。键保持 com.wslc-compose.* 不变：
// 界面靠 project / service 判断项目的运行状态，down / ps / start / stop 也靠它们找容器，
// 键一旦发布就不要改：已经创建出来的容器、网络、卷身上带着它们，改了键，旧资源就认不出来了。
const (
	LabelProject         = "com.wslc-compose.project"
	LabelService         = "com.wslc-compose.service"
	LabelIndex           = "com.wslc-compose.container-number"
	LabelConfigHash      = "com.wslc-compose.config-hash"
	LabelVersion         = "com.wslc-compose.version"
	LabelWorkingDir      = "com.wslc-compose.project.working_dir"
	LabelConfigFiles     = "com.wslc-compose.project.config_files"
	LabelEnvFile         = "com.wslc-compose.project.environment_file"
	LabelNetwork         = "com.wslc-compose.network"
	LabelVolume          = "com.wslc-compose.volume"
	LabelCreateOperation = "com.wslc-compose.create-operation"
)

// Version 是内置 wslc-compose 引擎的版本，写进容器标签，排查问题时用。
const Version = "1.0.0"

// argsEnv 提供构造 `wslc run` 参数时需要的两项运行时信息。
// 拆成接口（函数）是为了让参数构造保持纯函数：测试里直接给固定值，不需要 wslc。
type argsEnv struct {
	// supports 报告已安装的 wslc 是否支持某个 run 选项（如 --restart、--cap-add）。
	supports func(flag string) bool
	// imageEntrypoint 返回本地镜像的 ENTRYPOINT；镜像不在本地时 found 为 false。
	imageEntrypoint func(image string) (entrypoint []string, found bool)
}

func (e argsEnv) has(flag string) bool { return e.supports != nil && e.supports(flag) }

// runSpec 描述这次要生成哪种命令。
type runSpec struct {
	// detach 对应 `run -d`；create 为 true 时生成 `create`（多网络容器要先创建、连接、再启动）。
	detach bool
	create bool
	// operation 写进 create-operation 标签，用于失败时确认「这个容器确实是本次创建的」再清理。
	operation string
	// envFile 是显式指定的 --env-file，只用于写标签。
	envFile string
	// forHash 为 true 时生成「用于计算指纹」的版本：去掉每次都会变或与配置无关的部分
	// （-d、容器名、项目元数据标签、镜像 ENTRYPOINT 查询）。
	forHash bool
}

// sortedKeys 返回 map 的键，按字母序。compose-go 的各种映射遍历顺序随机，
// 凡是会进入命令行的都要先排序，参数顺序才稳定（指纹也依赖这一点）。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// projectLabels 是容器和资源共用的项目元数据标签（顺序固定）。
func projectLabels(p *types.Project, envFile string) [][2]string {
	files, _ := json.Marshal(p.ComposeFiles)
	out := [][2]string{
		{LabelProject, p.Name},
		{LabelVersion, Version},
		{LabelWorkingDir, p.WorkingDir},
		{LabelConfigFiles, string(files)},
	}
	if envFile != "" {
		out = append(out, [2]string{LabelEnvFile, envFile})
	}
	return out
}

// resourceLabels 是网络 / 卷上的标签。kind 为 "network" 或 "volume"，key 是它在 compose 文件里的名字。
func resourceLabels(p *types.Project, kind, key, name, envFile string) [][2]string {
	out := projectLabels(p, envFile)
	label := LabelVolume
	if kind == "network" {
		label = LabelNetwork
	}
	sum := sha256.Sum256([]byte(kind + ":" + key + ":" + name))
	return append(out, [2]string{label, key}, [2]string{LabelConfigHash, hex.EncodeToString(sum[:])[:16]})
}

// runArgs 生成 `wslc run`（或 `create`）的参数。
//
// 这是整个引擎里「compose 语义 → wslc 命令行」的唯一翻译点。
// 注意：参数的顺序和格式也是配置指纹的一部分（见 serviceHash），
// 改动它们会让现有容器在下次 up 时被重建一次——这是预期内的，但要有意为之。
func runArgs(p *types.Project, s types.ServiceConfig, index int, rs runSpec, env argsEnv) ([]string, error) {
	args := []string{"run"}
	if rs.create {
		args[0] = "create"
	} else if rs.detach && !rs.forHash {
		args = append(args, "-d")
	}
	if !rs.forHash {
		args = append(args, "--name", containerName(p, s, index))
	}

	// 用户自己的标签在前；项目元数据在后。
	for _, k := range sortedKeys(s.Labels) {
		args = append(args, "-l", k+"="+s.Labels[k])
	}
	if !rs.forHash {
		hash, err := serviceHash(p, s, env)
		if err != nil {
			return nil, err
		}
		for _, kv := range projectLabels(p, rs.envFile) {
			args = append(args, "-l", kv[0]+"="+kv[1])
		}
		args = append(args,
			"-l", LabelService+"="+s.Name,
			"-l", LabelIndex+"="+strconv.Itoa(index),
			"-l", LabelConfigHash+"="+hash)
		if rs.operation != "" {
			args = append(args, "-l", LabelCreateOperation+"="+rs.operation)
		}
	}

	// env_file 的内容已经由 compose-go 并入 Environment，这里只需要 -e。
	// 值为 nil 表示这个变量在 .env 和进程环境里都没有定义，Compose 的语义是不传。
	for _, k := range sortedKeys(s.Environment) {
		if v := s.Environment[k]; v != nil {
			args = append(args, "-e", k+"="+*v)
		}
	}

	// 端口按字母序输出（并去重）：声明顺序不同不应该产生不同的命令，
	// 更不应该让容器因为「把两行端口换了位置」而被重建。
	ports := make([]string, 0, len(s.Ports))
	for _, pc := range s.Ports {
		ports = append(ports, portFlag(pc))
	}
	sort.Strings(ports)
	for i, port := range ports {
		if i > 0 && port == ports[i-1] {
			continue
		}
		args = append(args, "-p", port)
	}

	mounts, err := mountFlags(p, s)
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	files, err := fileMountFlags(p, s)
	if err != nil {
		return nil, err
	}
	args = append(args, files...)
	for _, t := range s.Tmpfs {
		args = append(args, "--tmpfs", t)
	}

	netArgs, err := networkFlags(p, s)
	if err != nil {
		return nil, err
	}
	args = append(args, netArgs...)

	if s.Hostname != "" {
		args = append(args, "-h", s.Hostname)
	}
	if s.DomainName != "" {
		args = append(args, "--domainname", s.DomainName)
	}
	for _, d := range s.DNS {
		args = append(args, "--dns", d)
	}
	for _, d := range s.DNSSearch {
		args = append(args, "--dns-search", d)
	}
	for _, d := range s.DNSOpts {
		args = append(args, "--dns-option", d)
	}
	if s.User != "" {
		args = append(args, "-u", s.User)
	}
	if s.WorkingDir != "" {
		args = append(args, "-w", s.WorkingDir)
	}

	args = append(args, resourceFlags(s)...)
	for _, name := range sortedKeys(s.Ulimits) {
		if u := s.Ulimits[name]; u != nil {
			if u.Single != 0 {
				args = append(args, "--ulimit", fmt.Sprintf("%s=%d", name, u.Single))
			} else {
				args = append(args, "--ulimit", fmt.Sprintf("%s=%d:%d", name, u.Soft, u.Hard))
			}
		}
	}
	if s.StopSignal != "" {
		args = append(args, "--stop-signal", s.StopSignal)
	}
	if s.StopGracePeriod != nil {
		args = append(args, "--stop-timeout", strconv.Itoa(durationSeconds(time.Duration(*s.StopGracePeriod))))
	}
	// wslc 目前只实现 always 和 no 两种重启策略，其余的在 checkProject 里已经告警。
	if s.Restart == "always" && env.has("--restart") {
		args = append(args, "--restart", "always")
	}
	if env.has("--cap-add") {
		for _, c := range normalizeCaps(s.CapAdd) {
			args = append(args, "--cap-add", c)
		}
		for _, c := range normalizeCaps(s.CapDrop) {
			args = append(args, "--cap-drop", c)
		}
	}
	args = append(args, healthFlags(s.HealthCheck)...)
	if g := gpuSpec(s); g != "" {
		args = append(args, "--gpus", g)
	}
	if s.StdinOpen {
		args = append(args, "-i")
	}
	if s.Tty {
		args = append(args, "-t")
	}

	// `command: []` 清空镜像自带的 CMD。但 wslc 里显式给 --entrypoint 会连带重置 CMD，
	// 所以要把镜像自己的 ENTRYPOINT 重新声明一遍；镜像还没拉下来时查不到，
	// 由 up 在创建前先拉取。
	image := imageName(p, s)
	entrypoint := []string(s.Entrypoint)
	hasEntrypoint := s.Entrypoint != nil
	if !hasEntrypoint && s.Command != nil && len(s.Command) == 0 && !rs.forHash && env.imageEntrypoint != nil {
		if ep, found := env.imageEntrypoint(image); found {
			entrypoint, hasEntrypoint = ep, true
		}
	}
	switch {
	case hasEntrypoint && len(entrypoint) == 0:
		args = append(args, "--entrypoint", "") // `entrypoint: []`：不要任何 entrypoint
	case len(entrypoint) > 0:
		args = append(args, "--entrypoint", entrypoint[0])
	}
	args = append(args, image)
	if len(entrypoint) > 1 {
		args = append(args, entrypoint[1:]...)
	}
	args = append(args, s.Command...)
	return args, nil
}

// portFlag 把一条端口映射写成 `-p` 使用的形式：[宿主地址:][宿主端口:]容器端口[/协议]。
func portFlag(pc types.ServicePortConfig) string {
	spec := strconv.FormatUint(uint64(pc.Target), 10)
	hostIP := pc.HostIP
	if strings.Contains(hostIP, ":") && !strings.HasPrefix(hostIP, "[") {
		hostIP = "[" + hostIP + "]" // IPv6 地址要加方括号
	}
	switch {
	case hostIP != "" && pc.Published != "":
		spec = hostIP + ":" + pc.Published + ":" + spec
	case hostIP != "":
		spec = hostIP + "::" + spec // 该地址上的随机宿主端口
	case pc.Published != "":
		spec = pc.Published + ":" + spec
	}
	if pc.Protocol != "" && pc.Protocol != "tcp" {
		spec += "/" + pc.Protocol
	}
	return spec
}

// mountFlags 生成卷、绑定挂载和 tmpfs 的参数。
// 没有特殊选项时用简洁的 -v / --tmpfs；带选项时用 --mount（键由实测 wslc 3.0.5 得出）。
func mountFlags(p *types.Project, s types.ServiceConfig) ([]string, error) {
	var args []string
	for _, m := range s.Volumes {
		switch m.Type {
		case types.VolumeTypeBind:
			if m.Bind != nil && m.Bind.Recursive != "" {
				parts := []string{"type=bind", "source=" + m.Source, "target=" + m.Target}
				if m.ReadOnly {
					parts = append(parts, "readonly")
				}
				parts = append(parts, "bind-recursive="+m.Bind.Recursive)
				args = append(args, "--mount", strings.Join(parts, ","))
				continue
			}
			args = append(args, "-v", mountSpec(m.Source, m.Target, m.ReadOnly))
		case types.VolumeTypeVolume:
			vol, ok := p.Volumes[m.Source]
			if !ok {
				return nil, fmt.Errorf("service '%s' references undefined volume '%s'", s.Name, m.Source)
			}
			args = append(args, "-v", mountSpec(vol.Name, m.Target, m.ReadOnly))
		case types.VolumeTypeTmpfs:
			if m.Tmpfs != nil && (m.Tmpfs.Size != 0 || m.Tmpfs.Mode != 0) {
				parts := []string{"type=tmpfs", "target=" + m.Target}
				if m.Tmpfs.Size != 0 {
					parts = append(parts, "tmpfs-size="+strconv.FormatInt(int64(m.Tmpfs.Size), 10))
				}
				if m.Tmpfs.Mode != 0 {
					parts = append(parts, "tmpfs-mode="+strconv.FormatUint(uint64(m.Tmpfs.Mode), 8))
				}
				args = append(args, "--mount", strings.Join(parts, ","))
				continue
			}
			args = append(args, "--tmpfs", m.Target)
		default:
			return nil, fmt.Errorf("service '%s': volume type '%s' is not supported by wslc", s.Name, m.Type)
		}
	}
	return args, nil
}

func mountSpec(source, target string, readOnly bool) string {
	spec := source + ":" + target
	if readOnly {
		spec += ":ro"
	}
	return spec
}

// fileMountTarget 返回 secret / config 在容器内的路径：绝对路径原样使用；
// 没写或写的是相对文件名时，放到 dir 下（secrets 是 /run/secrets/，configs 是 /）。
//
// compose-go 只填了 secret 的默认目标，并不会给用户写的相对名字补前缀，所以这里要自己处理。
func fileMountTarget(dir, source, target string) string {
	switch {
	case target == "":
		return dir + source
	case strings.HasPrefix(target, "/"):
		return target
	}
	return dir + target
}

// fileMountFlags 把 secrets / configs 作为只读文件挂进容器。
func fileMountFlags(p *types.Project, s types.ServiceConfig) ([]string, error) {
	var args []string
	for _, ref := range s.Secrets {
		res, ok := p.Secrets[ref.Source]
		if !ok {
			return nil, fmt.Errorf("service '%s' references undefined secret '%s'", s.Name, ref.Source)
		}
		args = append(args, "-v", mountSpec(res.File, fileMountTarget("/run/secrets/", ref.Source, ref.Target), true))
	}
	for _, ref := range s.Configs {
		res, ok := p.Configs[ref.Source]
		if !ok {
			return nil, fmt.Errorf("service '%s' references undefined config '%s'", s.Name, ref.Source)
		}
		args = append(args, "-v", mountSpec(res.File, fileMountTarget("/", ref.Source, ref.Target), true))
	}
	return args, nil
}

// serviceNet 是服务接入的一个网络。
type serviceNet struct {
	key  string // compose 文件里的名字
	name string // wslc 里的真实名字
	cfg  *types.ServiceNetworkConfig
}

// serviceNetworks 按优先级（高的在前）、同级按名字排序返回服务接入的网络。
// 第一个是 run 时直接接入的主网络，其余的要在创建后用 network connect 接入。
func serviceNetworks(p *types.Project, s types.ServiceConfig) []serviceNet {
	out := make([]serviceNet, 0, len(s.Networks))
	for key, cfg := range s.Networks {
		name := key
		if n, ok := p.Networks[key]; ok && n.Name != "" {
			name = n.Name
		}
		out = append(out, serviceNet{key: key, name: name, cfg: cfg})
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := 0, 0
		if out[i].cfg != nil {
			pi = out[i].cfg.Priority
		}
		if out[j].cfg != nil {
			pj = out[j].cfg.Priority
		}
		if pi != pj {
			return pi > pj
		}
		return out[i].key < out[j].key
	})
	return out
}

// aliases 返回某个网络上的全部别名：配置的别名加上服务名本身，去重排序。
func (n serviceNet) aliases(s types.ServiceConfig) []string {
	set := map[string]bool{s.Name: true}
	if n.cfg != nil {
		for _, a := range n.cfg.Aliases {
			set[a] = true
		}
	}
	return sortedKeys(set)
}

func (n serviceNet) ipv4() string {
	if n.cfg == nil {
		return ""
	}
	return n.cfg.Ipv4Address
}

// networkFlags 生成主网络（或 network_mode）的参数。
func networkFlags(p *types.Project, s types.ServiceConfig) ([]string, error) {
	if s.NetworkMode != "" {
		mode := s.NetworkMode
		if dep, ok := strings.CutPrefix(mode, "service:"); ok {
			target, found := p.Services[dep]
			if !found {
				return nil, fmt.Errorf("network_mode references unknown service '%s'", dep)
			}
			mode = "container:" + containerName(p, target, 1)
		}
		return []string{"--network", mode}, nil
	}
	nets := serviceNetworks(p, s)
	if len(nets) == 0 {
		return nil, nil
	}
	primary := nets[0]
	args := []string{"--network", primary.name}
	if ip := primary.ipv4(); ip != "" {
		args = append(args, "--ip", ip)
	}
	for _, a := range primary.aliases(s) {
		args = append(args, "--network-alias", a)
	}
	return args, nil
}

// networkConnectArgs 生成把容器接入一个次要网络的参数（精确到一个端点）。
func networkConnectArgs(s types.ServiceConfig, n serviceNet, container string) []string {
	args := []string{"network", "connect"}
	if ip := n.ipv4(); ip != "" {
		args = append(args, "--ip", ip)
	}
	for _, a := range n.aliases(s) {
		args = append(args, "--network-alias", a)
	}
	return append(args, n.name, container)
}

// formatBytes 把字节数写成 wslc 习惯的写法：能整除就用 g / m / k 后缀，否则用纯字节数。
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + "g"
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + "m"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + "k"
	}
	return strconv.FormatInt(n, 10)
}

// resourceFlags 生成内存、CPU 和共享内存限制。
// 既支持顶层的 mem_limit / cpus，也支持 deploy.resources.limits。
func resourceFlags(s types.ServiceConfig) []string {
	var limits *types.Resource
	if s.Deploy != nil {
		limits = s.Deploy.Resources.Limits
	}
	var args []string
	mem := int64(s.MemLimit)
	if mem == 0 && limits != nil {
		mem = int64(limits.MemoryBytes)
	}
	if mem > 0 {
		args = append(args, "-m", formatBytes(mem))
	}
	cpus := s.CPUS
	if cpus == 0 && limits != nil {
		cpus = float32(limits.NanoCPUs)
	}
	if cpus > 0 {
		// 按 32 位精度格式化：float32(0.1) 写成 "0.1" 而不是 "0.10000000149011612"。
		args = append(args, "--cpus", strconv.FormatFloat(float64(cpus), 'f', -1, 32))
	}
	if s.ShmSize > 0 {
		args = append(args, "--shm-size", formatBytes(int64(s.ShmSize)))
	}
	return args
}

// gpuSpec 返回 --gpus 的取值：deploy.resources.reservations.devices 里带 gpu 能力的设备，
// 或简写的 gpus。没有 GPU 需求返回空串。
func gpuSpec(s types.ServiceConfig) string {
	spec := ""
	pick := func(devs []types.DeviceRequest) {
		for _, d := range devs {
			isGPU := len(d.Capabilities) == 0 // 简写 `gpus: all` 不一定带 capabilities
			for _, c := range d.Capabilities {
				if c == "gpu" {
					isGPU = true
				}
			}
			if !isGPU {
				continue
			}
			if d.Count <= 0 { // -1 表示 all；0 或缺省也按 all 处理
				spec = "all"
			} else {
				spec = strconv.FormatInt(int64(d.Count), 10)
			}
		}
	}
	if s.Deploy != nil && s.Deploy.Resources.Reservations != nil {
		pick(s.Deploy.Resources.Reservations.Devices)
	}
	pick(s.Gpus)
	return spec
}

// healthFlags 生成健康检查参数。
func healthFlags(h *types.HealthCheckConfig) []string {
	if h == nil {
		return nil
	}
	if h.Disable || (len(h.Test) > 0 && h.Test[0] == "NONE") {
		return []string{"--no-healthcheck"}
	}
	var args []string
	if len(h.Test) > 0 {
		var cmd string
		if h.Test[0] == "CMD-SHELL" && len(h.Test) > 1 {
			cmd = h.Test[1]
		} else {
			// CMD 形式是参数数组，而 --health-cmd 只收一条 shell 命令，拼接时要保留参数边界。
			cmd = shellJoin(h.Test[1:])
		}
		args = append(args, "--health-cmd", cmd)
	}
	// 没写 test 时，这些选项是在调整镜像自带的健康检查。
	if h.Interval != nil {
		args = append(args, "--health-interval", durationArg(time.Duration(*h.Interval)))
	}
	if h.Timeout != nil {
		args = append(args, "--health-timeout", durationArg(time.Duration(*h.Timeout)))
	}
	if h.StartPeriod != nil {
		args = append(args, "--health-start-period", durationArg(time.Duration(*h.StartPeriod)))
	}
	if h.Retries != nil {
		args = append(args, "--health-retries", strconv.FormatUint(*h.Retries, 10))
	}
	return args
}

// durationArg 把时长写成 wslc 能稳定解析的形式：整秒用 "90s"，不足整秒用 "500ms"。
// 不用 time.Duration.String()：它会写出 "1m30s" 甚至 "2m0s"，不同 CLI 的解析器对这类复合写法的容忍度不一。
func durationArg(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}

// durationSeconds 把时长换算成整秒，至少 1 秒；取整用偶数优先（四舍六入五成双），
// 与 Compose 习惯一致：1.5s → 2，2.5s → 2。
func durationSeconds(d time.Duration) int {
	return int(math.Max(1, math.RoundToEven(d.Seconds())))
}

// normalizeCaps 规范化 capability 名：大写、去掉 CAP_ 前缀、去重（保持首次出现的顺序）。
func normalizeCaps(caps []string) []string {
	var out []string
	for _, c := range caps {
		name := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_")
		if name != "" && !contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// buildArgs 生成 `wslc build` 的参数。
func buildArgs(p *types.Project, s types.ServiceConfig, noCache bool) []string {
	b := s.Build
	args := []string{"build", "-t", imageName(p, s)}
	if b.Dockerfile != "" {
		// wslc 把 -f 相对它自己的当前目录解析，而不是构建上下文，所以给绝对路径。
		df := b.Dockerfile
		if !filepath.IsAbs(df) {
			df = filepath.Join(b.Context, df)
		}
		args = append(args, "-f", df)
	}
	for _, k := range sortedKeys(b.Args) {
		// 只写了名字的构建参数，compose-go 在加载时已经用项目环境解析过；
		// 取不到值的会被直接丢弃，这里仍为 nil 的也跳过。
		if v := b.Args[k]; v != nil {
			args = append(args, "--build-arg", k+"="+*v)
		}
	}
	if b.Target != "" {
		args = append(args, "--target", b.Target)
	}
	if b.Pull {
		args = append(args, "--pull")
	}
	if noCache || b.NoCache {
		args = append(args, "--no-cache")
	}
	return append(args, b.Context)
}
