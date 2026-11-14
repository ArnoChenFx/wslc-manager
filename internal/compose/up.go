package compose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// upOptions 是 up 的参数。
type upOptions struct {
	Services      []string
	Profiles      []string
	Build         bool
	ForceRecreate bool
	// Scale 覆盖服务的副本数。
	Scale map[string]int
	// StopTimeout 是停止旧容器时等待的秒数。
	StopTimeout int
	// WaitTimeout 是等待每个依赖条件的最长时间，0 不限。
	WaitTimeout time.Duration
	EnvFile     string
}

// argsEnv 为参数构造提供运行时信息。
func (e *engine) argsEnv(ctx context.Context) argsEnv {
	return argsEnv{
		supports:        func(flag string) bool { return e.supportsRunOption(ctx, flag) },
		imageEntrypoint: func(image string) ([]string, bool) { return e.imageEntrypoint(ctx, image) },
	}
}

// runtimeWarnings 提示旧版 wslc 不支持的选项：静默忽略会让用户以为设置生效了。
func (e *engine) runtimeWarnings(ctx context.Context, services []types.ServiceConfig) []string {
	var out []string
	for _, s := range services {
		if s.Restart == "always" && !e.supportsRunOption(ctx, "--restart") {
			out = append(out, fmt.Sprintf("%s: 'restart: always' needs wslc >= 3.0.5; ignoring it", s.Name))
		}
		if (len(s.CapAdd) > 0 || len(s.CapDrop) > 0) && !e.supportsRunOption(ctx, "--cap-add") {
			out = append(out, fmt.Sprintf("%s: cap_add/cap_drop need wslc >= 3.0.5; ignoring them", s.Name))
		}
	}
	return out
}

// up 创建并启动服务。返回 error 表示命令整体失败（部分服务启动失败也算）。
//
// 流程与 `docker compose up -d` 一致：先准备网络和卷，再按依赖顺序逐个服务处理——
// 配置没变就保留，变了就重建，不存在就创建。
// 一个服务失败不会中止其他服务：它的依赖者被跳过，其余照常启动，最后汇总报错。
func (e *engine) up(ctx context.Context, p *types.Project, o upOptions) error {
	services, err := selectServices(p, o.Services, o.Profiles)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return errors.New("no services to start")
	}
	for _, w := range e.runtimeWarnings(ctx, services) {
		e.info("warning: %s", w)
	}

	replicas := func(s types.ServiceConfig) int {
		if n, ok := o.Scale[s.Name]; ok {
			return n
		}
		return s.GetScale()
	}
	// 在创建任何东西之前先校验，免得留下一个启动了一半的项目。
	staticSeen := map[string]bool{}
	for _, s := range services {
		n := replicas(s)
		if s.ContainerName != "" && n > 1 {
			return fmt.Errorf("%s: cannot scale a service with container_name set", s.Name)
		}
		hasStatic := false
		for _, net := range serviceNetworks(p, s) {
			hasStatic = hasStatic || net.ipv4() != ""
		}
		if hasStatic && n > 1 {
			return fmt.Errorf("%s: multiple replicas cannot share a static IPv4 address", s.Name)
		}
		if n > 0 {
			for _, net := range serviceNetworks(p, s) {
				if net.ipv4() == "" {
					continue
				}
				key := net.name + "=" + net.ipv4()
				if staticSeen[key] {
					return fmt.Errorf("%s: another selected service requests the same static IPv4 address", s.Name)
				}
				staticSeen[key] = true
			}
		}
	}

	networkStates, err := e.ensureNetworks(ctx, p, services, o.EnvFile)
	if err != nil {
		return err
	}
	for _, s := range services {
		if err := validateStaticAddresses(p, s, networkStates); err != nil {
			return err
		}
	}
	if err := e.ensureVolumes(ctx, p, services, o.EnvFile); err != nil {
		return err
	}

	// 先构建：显式要求构建，或目标镜像在本地不存在。
	for _, s := range services {
		if s.Build == nil {
			continue
		}
		if o.Build || !e.imageExists(ctx, imageName(p, s)) {
			e.info("Building %s ...", s.Name)
			if _, err := e.runStream(ctx, e.out, buildArgs(p, s, false)...); err != nil {
				return err
			}
		}
	}

	// 把已退出的容器也算进来：up 要能直接启动一个没变的服务，而不是和它占着的名字冲突。
	//
	// dry-run 也要读现有容器（只读查询，不改任何东西）：预览的意义就是告诉用户「真正执行会发生什么」。
	// 如果跳过它，对一个已经在跑的项目，预览会错误地说「Creating」，
	// 而实际上要么原样保留、要么重建。
	list, err := e.listProjectContainers(ctx, p.Name, true)
	if err != nil {
		return err
	}
	existing := make(map[string]container, len(list))
	for _, c := range list {
		existing[c.Name] = c
	}

	env := e.argsEnv(ctx)
	failed := map[string]string{}
	var failedOrder []string
	fail := func(name, reason string) {
		if _, dup := failed[name]; !dup {
			failed[name] = reason
			failedOrder = append(failedOrder, name)
		}
	}
	scales := make(map[string]int, len(services))
	for _, s := range services {
		scales[s.Name] = replicas(s)
	}

	for _, s := range services {
		n := replicas(s)
		if blocked := blockedBy(s, failed); blocked != "" && n > 0 {
			fail(s.Name, fmt.Sprintf("dependency '%s' failed to start", blocked))
			e.info("Skipping %s: %s", s.Name, failed[s.Name])
			continue
		}

		if hasWaitConditions(s) && n > 0 {
			if e.dryRun {
				for _, dep := range sortedKeys(s.DependsOn) {
					if c := s.DependsOn[dep].Condition; c != "" && c != types.ServiceConditionStarted {
						e.info("(dry run) would wait for %s: %s", dep, c)
					}
				}
			} else {
				for _, dep := range sortedKeys(s.DependsOn) {
					if _, bad := failed[dep]; bad { // 能走到这里说明它是 required:false，上面没把本服务挡住
						e.info("warning: %s: optional dependency '%s' failed to start; not waiting for it", s.Name, dep)
					}
				}
				var live []types.ServiceConfig
				for _, other := range services {
					if _, bad := failed[other.Name]; !bad {
						live = append(live, other)
					}
				}
				if err := e.waitForDependencies(ctx, p, s, live, scales, o.WaitTimeout); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					fail(s.Name, err.Error())
					e.info("Skipping %s: %v", s.Name, err)
					continue
				}
			}
		}

		for index := 1; index <= n; index++ {
			cname := containerName(p, s, index)
			current, found := existing[cname]
			delete(existing, cname)
			if err := e.reconcileContainer(ctx, p, s, index, current, found, o, env); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fail(s.Name, err.Error())
				e.info("Container %s failed to start: %v", cname, err)
			}
		}

		// 删除超出目标副本数的多余容器。
		surplus := make([]string, 0)
		for name, c := range existing {
			if c.Service == s.Name && c.Index > n {
				surplus = append(surplus, name)
			}
		}
		sort.Strings(surplus)
		for _, name := range surplus {
			e.info("Removing surplus %s ...", name)
			if _, err := e.run(ctx, "remove", "-f", name); err != nil {
				return err
			}
			delete(existing, name)
		}
	}

	if len(failed) > 0 {
		noun := "service"
		if len(failed) != 1 {
			noun = "services"
		}
		parts := make([]string, 0, len(failed))
		for _, name := range failedOrder {
			parts = append(parts, fmt.Sprintf("%s (%s)", name, failed[name]))
		}
		return fmt.Errorf("%d %s failed to start: %s", len(failed), noun, strings.Join(parts, "; "))
	}
	return nil
}

// blockedBy 返回第一个已失败的必需依赖，没有则返回空串。
func blockedBy(s types.ServiceConfig, failed map[string]string) string {
	for _, dep := range sortedKeys(s.DependsOn) {
		if _, bad := failed[dep]; bad && s.DependsOn[dep].Required {
			return dep
		}
	}
	return ""
}

func hasWaitConditions(s types.ServiceConfig) bool {
	for _, d := range s.DependsOn {
		if d.Condition != "" && d.Condition != types.ServiceConditionStarted {
			return true
		}
	}
	return false
}

// reconcileContainer 让一个副本达到期望状态：保留、启动、重建或新建。
func (e *engine) reconcileContainer(ctx context.Context, p *types.Project, s types.ServiceConfig, index int,
	current container, found bool, o upOptions, env argsEnv) error {
	cname := containerName(p, s, index)

	if found {
		desired, err := serviceHash(p, s, env)
		if err != nil {
			return err
		}
		netOK, err := networkMatches(p, s, current)
		if err != nil {
			return err
		}
		fresh := current.Hash == desired && !o.ForceRecreate && netOK && e.runtimeMatches(ctx, s, current)
		switch {
		case fresh && current.Running:
			e.info("Container %s is up-to-date", cname)
			return nil
		case fresh:
			e.info("Starting %s ...", cname)
			if _, err := e.runRetried(ctx, "start", cname); err != nil {
				return err
			}
			if err := e.verifyContainerNetworks(ctx, p, s, cname); err != nil {
				return err
			}
			return e.applyExtraHosts(ctx, cname, s.ExtraHosts)
		}
		e.info("Recreating %s ...", cname)
		if current.Running {
			if _, err := e.run(ctx, "stop", "-t", strconv.Itoa(o.StopTimeout), cname); err != nil {
				return err
			}
		}
		if _, err := e.run(ctx, "remove", "-f", cname); err != nil {
			return err
		}
	} else {
		e.info("Creating %s ...", cname)
	}

	if err := e.pullForCommandReset(ctx, p, s); err != nil {
		return err
	}
	// 次要网络要在容器创建之后、启动之前接入，这样入口进程运行时所有网络端点都已就绪。
	if len(serviceNetworks(p, s)) > 1 && s.NetworkMode == "" {
		if err := e.createAndStart(ctx, p, s, index, o.EnvFile, env); err != nil {
			return err
		}
	} else {
		args, err := runArgs(p, s, index, runSpec{detach: true, envFile: o.EnvFile}, env)
		if err != nil {
			return err
		}
		if _, err := e.run(ctx, args...); err != nil {
			return err
		}
		if err := e.verifyContainerNetworks(ctx, p, s, cname); err != nil {
			return err
		}
	}
	return e.applyExtraHosts(ctx, cname, s.ExtraHosts)
}

// runtimeMatches 对比重启策略和 capability：它们不在容器标签的指纹里被单独核对，
// 而是和 inspect 的实际值比较。只比较已安装的 wslc 支持的选项，
// 这样旧版 wslc 不会陷入「每次 up 都重建」的循环。
func (e *engine) runtimeMatches(ctx context.Context, s types.ServiceConfig, c container) bool {
	if e.supportsRunOption(ctx, "--restart") {
		want := "no"
		if s.Restart == "always" {
			want = "always"
		}
		have := "no"
		if c.RestartPolicy == "always" {
			have = "always"
		}
		if want != have {
			return false
		}
	}
	if e.supportsRunOption(ctx, "--cap-add") {
		if !sameSet(normalizeCaps(s.CapAdd), normalizeCaps(c.CapAdd)) || !sameSet(normalizeCaps(s.CapDrop), normalizeCaps(c.CapDrop)) {
			return false
		}
	}
	return true
}

func sameSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\x00") == strings.Join(y, "\x00")
}

// pullForCommandReset：`command: []` 要重新声明镜像自己的 ENTRYPOINT，必须先有本地镜像。
func (e *engine) pullForCommandReset(ctx context.Context, p *types.Project, s types.ServiceConfig) error {
	if s.Command == nil || len(s.Command) != 0 || s.Entrypoint != nil || e.dryRun {
		return nil
	}
	image := imageName(p, s)
	if _, found := e.imageEntrypoint(ctx, image); found {
		return nil
	}
	e.info("Pulling %s ...", image)
	_, err := e.runStream(ctx, e.out, "pull", image)
	return err
}

// newOperationID 生成本次创建的唯一标识，写进容器标签，
// 失败清理时据此确认「这个容器确实是我刚创建的」，避免误删同名的其他容器。
func newOperationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// ownedBy 判断 inspect 数据是否是本次操作创建的那个容器。
func ownedBy(data map[string]any, p *types.Project, s types.ServiceConfig, index int, operation string) bool {
	if str(data["Id"]) == "" {
		return false
	}
	l := labelsOf(data)
	return l[LabelProject] == p.Name && l[LabelService] == s.Name &&
		l[LabelIndex] == strconv.Itoa(index) && l[LabelCreateOperation] == operation
}

// createAndStart 用「create → connect 次要网络 → start」创建多网络容器。
// 失败时只清理本次操作创建出来的容器。
func (e *engine) createAndStart(ctx context.Context, p *types.Project, s types.ServiceConfig, index int, envFile string, env argsEnv) (err error) {
	name := containerName(p, s, index)
	operation := newOperationID()
	identifier := ""

	defer func() {
		if err == nil || e.dryRun {
			return
		}
		// 用不依赖调用方 ctx 的短上下文清理：用户取消操作后仍然要把半成品收拾掉。
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		data, ierr := e.inspect(cctx, "", name)
		if ierr != nil || data == nil || !ownedBy(data, p, s, index, operation) {
			return
		}
		if identifier != "" && str(data["Id"]) != identifier {
			err = &WslcError{Msg: fmt.Sprintf("%v; partial container '%s' changed identity; cleanup was not applied", err, name)}
			return
		}
		if _, rerr := e.run(cctx, "remove", "-f", "-v", str(data["Id"])); rerr != nil {
			err = &WslcError{Msg: fmt.Sprintf("%v; cleanup of partial container '%s' failed: %v", err, name, rerr)}
		}
	}()

	args, err := runArgs(p, s, index, runSpec{create: true, operation: operation, envFile: envFile}, env)
	if err != nil {
		return err
	}
	if _, err = e.run(ctx, args...); err != nil {
		return err
	}
	if !e.dryRun {
		created, ierr := e.inspect(ctx, "", name)
		if ierr != nil {
			return ierr
		}
		if created == nil || !ownedBy(created, p, s, index, operation) {
			return &WslcError{Msg: fmt.Sprintf("created container '%s' ownership could not be verified", name)}
		}
		identifier = str(created["Id"])
	}
	target := name
	if identifier != "" {
		target = identifier
	}
	for _, n := range serviceNetworks(p, s)[1:] {
		if _, err = e.run(ctx, networkConnectArgs(s, n, target)...); err != nil {
			return err
		}
	}
	if err = e.verifyContainerNetworks(ctx, p, s, target); err != nil {
		return err
	}
	if _, err = e.runRetried(ctx, "start", target); err != nil {
		return err
	}
	return e.verifyContainerNetworks(ctx, p, s, target)
}

// applyExtraHosts 在容器启动后往 /etc/hosts 追加条目。
//
// wslc 目前没有 --add-host，所以这是尽力而为的替代方案：入口进程可能在条目写入之前就已经运行。
// `host-gateway` 会被解析成容器的默认网关地址（从 /proc/net/route 里读出来）。
func (e *engine) applyExtraHosts(ctx context.Context, container string, hosts types.HostsList) error {
	if len(hosts) == 0 {
		return nil
	}
	var lines []string
	for _, host := range sortedKeys(hosts) {
		for _, addr := range hosts[host] {
			addr = strings.TrimSpace(addr)
			if host == "" || addr == "" {
				continue
			}
			if strings.EqualFold(addr, "host-gateway") {
				addr = "$GW"
			}
			lines = append(lines, fmt.Sprintf(`echo "%s %s" >> /etc/hosts`, addr, host))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	script := `GW=$(awk '$2 == "00000000" { h=$3; for (i=7; i>=1; i-=2) { ` +
		`hi=index("0123456789ABCDEF", substr(h,i,1))-1; ` +
		`lo=index("0123456789ABCDEF", substr(h,i+1,1))-1; ` +
		`printf "%s%d", i==7 ? "" : ".", hi*16+lo } print ""; exit }' ` +
		`/proc/net/route 2>/dev/null); ` + strings.Join(lines, "; ")
	// 失败不致命：它本来就是补救措施。
	_, _ = e.run(ctx, "exec", "--user", "0", container, "sh", "-c", script)
	return nil
}

// ensureNetworks 创建或校验服务用到的网络，返回网络名 → inspect 数据（供静态 IP 校验用）。
func (e *engine) ensureNetworks(ctx context.Context, p *types.Project, services []types.ServiceConfig, envFile string) (map[string]map[string]any, error) {
	needed := map[string]bool{}
	for _, s := range services {
		for _, n := range serviceNetworks(p, s) {
			needed[n.name] = true
		}
	}
	existing, err := e.names(ctx, "network")
	if err != nil {
		return nil, err
	}
	states := map[string]map[string]any{}
	for _, key := range sortedKeys(p.Networks) {
		n := p.Networks[key]
		if !needed[n.Name] {
			continue
		}
		if bool(n.External) {
			if !existing[n.Name] {
				return nil, fmt.Errorf("external network '%s' not found", n.Name)
			}
			if states[n.Name], err = e.verifyNetwork(ctx, n); err != nil {
				return nil, err
			}
			continue
		}
		created := false
		if !existing[n.Name] {
			if _, err := e.run(ctx, networkCreateArgs(p, key, n, envFile)...); err != nil {
				return nil, err
			}
			e.info("Network %s created", n.Name)
			created = true
		}
		// dry-run 里刚「创建」的网络并不真的存在，跳过校验。
		if !(created && e.dryRun) {
			if states[n.Name], err = e.verifyNetwork(ctx, n); err != nil {
				return nil, err
			}
		}
	}
	return states, nil
}

// networkCreateArgs 生成 `wslc network create` 的参数。
func networkCreateArgs(p *types.Project, key string, n types.NetworkConfig, envFile string) []string {
	args := []string{"network", "create"}
	// 用户的标签在前，项目元数据在后；同名时后者覆盖前者，这样归属标签永远准确。
	labels := map[string]string{}
	for k, v := range n.Labels {
		labels[k] = v
	}
	order := sortedKeys(labels)
	for _, k := range order {
		if _, reserved := reservedLabel(resourceLabels(p, "network", key, n.Name, envFile), k); !reserved {
			args = append(args, "-l", k+"="+labels[k])
		}
	}
	for _, kv := range resourceLabels(p, "network", key, n.Name, envFile) {
		args = append(args, "-l", kv[0]+"="+kv[1])
	}
	if n.Internal {
		args = append(args, "--internal")
	}
	if n.Driver != "" {
		args = append(args, "--driver", n.Driver)
	}
	for _, k := range sortedKeys(n.DriverOpts) {
		args = append(args, "--opt", k+"="+n.DriverOpts[k])
	}
	if len(n.Ipam.Config) > 0 && n.Ipam.Config[0] != nil {
		pool := n.Ipam.Config[0]
		for _, f := range [][2]string{{"--subnet", pool.Subnet}, {"--gateway", pool.Gateway}, {"--ip-range", pool.IPRange}} {
			if f[1] != "" {
				args = append(args, f[0], f[1])
			}
		}
	}
	return append(args, n.Name)
}

func reservedLabel(reserved [][2]string, key string) (string, bool) {
	for _, kv := range reserved {
		if kv[0] == key {
			return kv[1], true
		}
	}
	return "", false
}

// ensureVolumes 创建服务用到的命名卷；外部卷必须已存在。
func (e *engine) ensureVolumes(ctx context.Context, p *types.Project, services []types.ServiceConfig, envFile string) error {
	needed := map[string]bool{}
	for _, s := range services {
		for _, m := range s.Volumes {
			if m.Type == types.VolumeTypeVolume {
				if v, ok := p.Volumes[m.Source]; ok {
					needed[v.Name] = true
				}
			}
		}
	}
	existing, err := e.names(ctx, "volume")
	if err != nil {
		return err
	}
	for _, key := range sortedKeys(p.Volumes) {
		v := p.Volumes[key]
		if !needed[v.Name] {
			continue
		}
		if bool(v.External) {
			if !existing[v.Name] {
				return fmt.Errorf("external volume '%s' not found", v.Name)
			}
			continue
		}
		if existing[v.Name] {
			continue
		}
		args := []string{"volume", "create"}
		if v.Driver != "" {
			args = append(args, "--driver", v.Driver)
		}
		for _, k := range sortedKeys(v.DriverOpts) {
			args = append(args, "--opt", k+"="+v.DriverOpts[k])
		}
		reserved := resourceLabels(p, "volume", key, v.Name, envFile)
		for _, k := range sortedKeys(v.Labels) {
			if _, isReserved := reservedLabel(reserved, k); !isReserved {
				args = append(args, "-l", k+"="+v.Labels[k])
			}
		}
		for _, kv := range reserved {
			args = append(args, "-l", kv[0]+"="+kv[1])
		}
		if _, err := e.run(ctx, append(args, v.Name)...); err != nil {
			return err
		}
		e.info("Volume %s created", v.Name)
	}
	return nil
}
