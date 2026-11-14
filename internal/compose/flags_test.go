package compose

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// 默认环境：wslc 支持所有可选的 run 选项；本地没有任何镜像。
var testEnv = argsEnv{supports: func(string) bool { return true }}

func hashArgs(t *testing.T, p *types.Project, name string, env argsEnv) []string {
	t.Helper()
	args, err := runArgs(p, p.Services[name], 1, runSpec{forHash: true}, env)
	if err != nil {
		t.Fatalf("runArgs: %v", err)
	}
	return args
}

// 一个覆盖面较广的服务：期望值逐条人工核对过，之后改动参数构造时要有意为之。
func TestRunArgsFull(t *testing.T) {
	p := loadYAML(t, `
name: demo
services:
  web:
    image: nginx:alpine
    command: nginx -g "daemon off;"
    entrypoint: ["/entry.sh", "--x"]
    labels: {team: core}
    environment: {B: "2", A: "1", EMPTY: }
    ports: ["8080:80", "127.0.0.1:8081:81/udp", "[::1]:7000:70", "9000-9001:9000-9001"]
    volumes:
      - C:\data\site:/site:ro
      - data:/data
      - type: tmpfs
        target: /tmp/c
        tmpfs: {size: 1048576, mode: 01777}
      - type: bind
        source: C:\data\logs
        target: /logs
        bind: {recursive: disabled}
    tmpfs: [/run]
    hostname: h
    user: "1000"
    working_dir: /srv
    mem_limit: 256m
    cpus: 0.5
    stop_signal: SIGINT
    stop_grace_period: 1m30s
    restart: always
    cap_add: [net_admin, CAP_SYS_TIME]
    healthcheck:
      test: ["CMD", "wget", "-q", "http://x y"]
      interval: 1m30s
      retries: 5
    stdin_open: true
    tty: true
    networks:
      back: {}
networks:
  back: {}
volumes:
  data: {}
`)
	want := []string{
		"run",
		"-l", "team=core",
		"-e", "A=1", "-e", "B=2", // EMPTY 在任何地方都没有定义，不传
		// 端口按字符串升序输出（声明顺序不影响结果）；端口范围已被 compose-go 展开。
		"-p", "127.0.0.1:8081:81/udp", "-p", "8080:80", "-p", "9000:9000", "-p", "9001:9001", "-p", "[::1]:7000:70",
		"-v", `C:\data\site:/site:ro`,
		"-v", "demo_data:/data", // 命名卷用带项目前缀的真实名字
		"--mount", "type=tmpfs,target=/tmp/c,tmpfs-size=1048576,tmpfs-mode=1777",
		"--mount", `type=bind,source=C:\data\logs,target=/logs,bind-recursive=disabled`,
		"--tmpfs", "/run",
		"--network", "demo_back", "--network-alias", "web", // 别名里总含服务名
		"-h", "h", "-u", "1000", "-w", "/srv",
		"-m", "256m", "--cpus", "0.5",
		"--stop-signal", "SIGINT", "--stop-timeout", "90",
		"--restart", "always",
		"--cap-add", "NET_ADMIN", "--cap-add", "SYS_TIME", // 大写、去掉 CAP_ 前缀
		"--health-cmd", "wget -q 'http://x y'", // CMD 形式保留参数边界
		"--health-interval", "90s", "--health-retries", "5",
		"-i", "-t",
		"--entrypoint", "/entry.sh",
		"nginx:alpine",
		"--x", "nginx", "-g", "daemon off;",
	}
	if got := hashArgs(t, p, "web", testEnv); !reflect.DeepEqual(got, want) {
		t.Errorf("参数不一致:\n got  %q\n want %q", got, want)
	}
}

// 非指纹模式：容器名、-d、项目元数据标签、指纹标签都要在。
func TestRunArgsLabelsAndName(t *testing.T) {
	p := loadYAML(t, "name: demo\nservices:\n  web:\n    image: x\n    labels: {team: core}\n")
	args, err := runArgs(p, p.Services["web"], 2, runSpec{detach: true, operation: "op123", envFile: `E:\x\.env`}, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run -d --name demo-web-2",
		"-l team=core",
		"-l " + LabelProject + "=demo",
		"-l " + LabelVersion + "=" + Version,
		"-l " + LabelService + "=web",
		"-l " + LabelIndex + "=2",
		"-l " + LabelCreateOperation + "=op123",
		"-l " + LabelEnvFile + `=E:\x\.env`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少 %q:\n%s", want, joined)
		}
	}
	h, _ := serviceHash(p, p.Services["web"], testEnv)
	if !strings.Contains(joined, "-l "+LabelConfigHash+"="+h) || !strings.HasPrefix(h, "g1:") {
		t.Errorf("指纹标签不对, hash=%s\n%s", h, joined)
	}

	// create 模式不带 -d，用于多网络容器。
	args, _ = runArgs(p, p.Services["web"], 1, runSpec{create: true, detach: true}, testEnv)
	if args[0] != "create" || contains(args, "-d") {
		t.Errorf("create 模式参数不对: %v", args)
	}
}

func TestRunArgsOptionalFlagsDependOnWslc(t *testing.T) {
	p := loadYAML(t, "services:\n  a:\n    image: x\n    restart: always\n    cap_add: [NET_ADMIN]\n")
	old := argsEnv{supports: func(string) bool { return false }} // 旧版 wslc：没有 --restart / --cap-*
	got := strings.Join(hashArgs(t, p, "a", old), " ")
	if strings.Contains(got, "--restart") || strings.Contains(got, "--cap-add") {
		t.Errorf("旧版 wslc 不应收到不支持的选项: %s", got)
	}
	got = strings.Join(hashArgs(t, p, "a", testEnv), " ")
	if !strings.Contains(got, "--restart always") || !strings.Contains(got, "--cap-add NET_ADMIN") {
		t.Errorf("新版 wslc 应收到选项: %s", got)
	}
}

func TestRunArgsEntrypointReset(t *testing.T) {
	lookup := func(ep []string, found bool) argsEnv {
		return argsEnv{supports: testEnv.supports, imageEntrypoint: func(string) ([]string, bool) { return ep, found }}
	}
	run := func(yaml string, env argsEnv) []string {
		p := loadYAML(t, yaml)
		args, err := runArgs(p, p.Services["a"], 1, runSpec{detach: true}, env)
		if err != nil {
			t.Fatal(err)
		}
		// 只看镜像名及其后的部分。
		for i, a := range args {
			if a == "img" {
				return append([]string{}, args[i-2:]...)
			}
		}
		return args
	}

	// command: [] 清空镜像 CMD；显式 --entrypoint 会连带重置 CMD，所以要把镜像自己的 ENTRYPOINT 重新声明。
	got := run("services:\n  a:\n    image: img\n    command: []\n", lookup([]string{"/docker-entrypoint.sh", "arg"}, true))
	if want := []string{"--entrypoint", "/docker-entrypoint.sh", "img", "arg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command 清空 + 镜像有 ENTRYPOINT: got %q, want %q", got, want)
	}
	// 镜像没有 ENTRYPOINT：仍要传 --entrypoint ""，这样才能重置镜像的 CMD。
	got = run("services:\n  a:\n    image: img\n    command: []\n", lookup(nil, true))
	if want := []string{"--entrypoint", "", "img"}; !reflect.DeepEqual(got, want) {
		t.Errorf("镜像没有 ENTRYPOINT: got %q, want %q", got, want)
	}
	// entrypoint: [] 表示不要任何 entrypoint。
	got = run("services:\n  a:\n    image: img\n    entrypoint: []\n", lookup(nil, false))
	if want := []string{"--entrypoint", "", "img"}; !reflect.DeepEqual(got, want) {
		t.Errorf("entrypoint 清空: got %q, want %q", got, want)
	}
}

func TestNetworkFlags(t *testing.T) {
	p := loadYAML(t, `
name: demo
services:
  web:
    image: x
    networks:
      front:
        aliases: [www, site]
        ipv4_address: 10.20.0.10
        priority: 10
      back: {}
  side:
    image: x
    network_mode: "service:web"
  host:
    image: x
    network_mode: host
networks:
  front: {ipam: {config: [{subnet: 10.20.0.0/24}]}}
  back: {}
`)
	// 优先级高的 front 成为主网络，静态 IP 和别名随它走。
	got := strings.Join(hashArgs(t, p, "web", testEnv), " ")
	if !strings.Contains(got, "--network demo_front --ip 10.20.0.10 --network-alias site --network-alias web --network-alias www") {
		t.Errorf("主网络参数不对: %s", got)
	}
	// 次要网络用 network connect。
	nets := serviceNetworks(p, p.Services["web"])
	if len(nets) != 2 || nets[0].key != "front" || nets[1].key != "back" {
		t.Fatalf("网络顺序 = %+v", nets)
	}
	if got, want := networkConnectArgs(p.Services["web"], nets[1], "cid"),
		[]string{"network", "connect", "--network-alias", "web", "demo_back", "cid"}; !reflect.DeepEqual(got, want) {
		t.Errorf("connect 参数 = %q, 期望 %q", got, want)
	}
	// network_mode: service:x → container:<该服务的容器名>
	if got := strings.Join(hashArgs(t, p, "side", testEnv), " "); !strings.Contains(got, "--network container:demo-web-1") {
		t.Errorf("service: 模式 = %s", got)
	}
	if got := strings.Join(hashArgs(t, p, "host", testEnv), " "); !strings.Contains(got, "--network host") {
		t.Errorf("host 模式 = %s", got)
	}
}

func TestHealthFlags(t *testing.T) {
	load := func(hc string) *types.HealthCheckConfig {
		return loadYAML(t, "services:\n  a:\n    image: x\n    healthcheck:\n"+hc).Services["a"].HealthCheck
	}
	tests := []struct {
		name string
		hc   string
		want []string
	}{
		{"shell 形式", "      test: pg_isready -U postgres\n", []string{"--health-cmd", "pg_isready -U postgres"}},
		{"CMD 形式", "      test: [CMD, redis-cli, ping]\n", []string{"--health-cmd", "redis-cli ping"}},
		{"全部选项", "      test: [CMD-SHELL, 'true']\n      interval: 5s\n      timeout: 1m\n      start_period: 500ms\n      retries: 3\n",
			[]string{"--health-cmd", "true", "--health-interval", "5s", "--health-timeout", "60s", "--health-start-period", "500ms", "--health-retries", "3"}},
		{"只调整镜像自带的检查", "      interval: 2s\n", []string{"--health-interval", "2s"}},
		{"禁用", "      disable: true\n", []string{"--no-healthcheck"}},
		{"NONE", "      test: [NONE]\n", []string{"--no-healthcheck"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := healthFlags(load(tc.hc)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	if healthFlags(nil) != nil {
		t.Error("没有健康检查时不应有参数")
	}
}

func TestResourceAndGPUFlags(t *testing.T) {
	p := loadYAML(t, `
services:
  a:
    image: x
    mem_limit: 1073741824
    cpus: 1.5
    shm_size: 2gb
  b:
    image: x
    deploy:
      resources:
        limits: {memory: "300000", cpus: "0.25"}
        reservations:
          devices: [{capabilities: [gpu], count: 2}]
  c:
    image: x
    gpus: all
  d:
    image: x
    mem_limit: 1500k
`)
	tests := []struct {
		svc  string
		want []string
		gpu  string
	}{
		{"a", []string{"-m", "1g", "--cpus", "1.5", "--shm-size", "2g"}, ""},
		{"b", []string{"-m", "300000", "--cpus", "0.25"}, "2"},
		{"c", nil, "all"},
		{"d", []string{"-m", "1500k"}, ""},
	}
	for _, tc := range tests {
		s := p.Services[tc.svc]
		if got := resourceFlags(s); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: 资源参数 = %q, 期望 %q", tc.svc, got, tc.want)
		}
		if got := gpuSpec(s); got != tc.gpu {
			t.Errorf("%s: gpus = %q, 期望 %q", tc.svc, got, tc.gpu)
		}
	}
}

func TestSmallFormatters(t *testing.T) {
	for in, want := range map[int64]string{1 << 30: "1g", 3 << 30: "3g", 256 << 20: "256m", 1500 << 10: "1500k", 1023: "1023", 0: "0"} {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, 期望 %q", in, got, want)
		}
	}
	for in, want := range map[time.Duration]string{90 * time.Second: "90s", 5 * time.Second: "5s", 500 * time.Millisecond: "500ms", 2 * time.Minute: "120s"} {
		if got := durationArg(in); got != want {
			t.Errorf("durationArg(%v) = %q, 期望 %q", in, got, want)
		}
	}
	// 偶数优先取整：1.5s → 2，2.5s → 2，0.4s → 至少 1。
	for in, want := range map[time.Duration]int{1500 * time.Millisecond: 2, 2500 * time.Millisecond: 2, 400 * time.Millisecond: 1, 90 * time.Second: 90} {
		if got := durationSeconds(in); got != want {
			t.Errorf("durationSeconds(%v) = %d, 期望 %d", in, got, want)
		}
	}
	if got := normalizeCaps([]string{"net_admin", "CAP_NET_ADMIN", " sys_time ", ""}); !reflect.DeepEqual(got, []string{"NET_ADMIN", "SYS_TIME"}) {
		t.Errorf("normalizeCaps = %v", got)
	}
}

func TestPortFlag(t *testing.T) {
	tests := []struct {
		in   types.ServicePortConfig
		want string
	}{
		{types.ServicePortConfig{Target: 80}, "80"},
		{types.ServicePortConfig{Target: 80, Published: "8080"}, "8080:80"},
		{types.ServicePortConfig{Target: 53, Published: "53", Protocol: "udp"}, "53:53/udp"},
		{types.ServicePortConfig{Target: 80, Published: "8080", Protocol: "tcp"}, "8080:80"},
		{types.ServicePortConfig{Target: 80, Published: "8080", HostIP: "127.0.0.1"}, "127.0.0.1:8080:80"},
		{types.ServicePortConfig{Target: 80, HostIP: "127.0.0.1"}, "127.0.0.1::80"},
		{types.ServicePortConfig{Target: 70, Published: "7000", HostIP: "::1"}, "[::1]:7000:70"},
	}
	for _, tc := range tests {
		if got := portFlag(tc.in); got != tc.want {
			t.Errorf("portFlag(%+v) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestSecretsAndConfigsMounts(t *testing.T) {
	p := loadYAML(t, `
name: demo
secrets:
  pw: {file: ./pw.txt}
  pw2: {file: ./pw.txt}
configs:
  cfg: {file: ./cfg.ini}
  cfg2: {file: ./cfg.ini}
services:
  a:
    image: x
    secrets: [pw, {source: pw2, target: renamed}]
    configs: [cfg, {source: cfg2, target: /etc/app.ini}]
`, map[string]string{"pw.txt": "x", "cfg.ini": "y"})
	got := strings.Join(hashArgs(t, p, "a", testEnv), " ")
	for _, want := range []string{
		"pw.txt:/run/secrets/pw:ro", "pw.txt:/run/secrets/renamed:ro",
		"cfg.ini:/cfg:ro", "cfg.ini:/etc/app.ini:ro",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q:\n%s", want, got)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	// 只写了名字的构建参数（FROM_PROJECT_ENV）从插值环境里取值，取不到的（MISSING）被丢弃。
	p := loadYAMLEnv(t, `
name: demo
services:
  api:
    image: org/api:dev
    build:
      context: ./api
      dockerfile: Dockerfile.dev
      args: [NODE_ENV=production, FROM_PROJECT_ENV, MISSING]
      target: runtime
      pull: true
      no_cache: true
  tools:
    build: .
`, []string{"FROM_PROJECT_ENV=v1"})
	args := buildArgs(p, p.Services["api"], false)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"build -t org/api:dev",
		"-f ", "Dockerfile.dev", // wslc 相对自己的当前目录解析 -f，所以给绝对路径
		"--build-arg FROM_PROJECT_ENV=v1", "--build-arg NODE_ENV=production",
		"--target runtime", "--pull", "--no-cache",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少 %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "MISSING") {
		t.Errorf("取不到值的构建参数不应传递: %s", joined)
	}
	if !strings.HasSuffix(args[len(args)-1], "api") {
		t.Errorf("最后一个参数应是构建上下文: %v", args)
	}
	// 只写了 build 的服务用 <项目>-<服务> 作镜像名。
	if got := buildArgs(p, p.Services["tools"], true); got[2] != "demo-tools" || !contains(got, "--no-cache") {
		t.Errorf("tools 的构建参数 = %v", got)
	}
}

func TestResourceLabels(t *testing.T) {
	p := loadYAML(t, "name: demo\nservices:\n  a:\n    image: x\n")
	got := resourceLabels(p, "network", "default", "demo_default", "")
	keys := make([]string, len(got))
	for i, kv := range got {
		keys[i] = kv[0]
	}
	want := []string{LabelProject, LabelVersion, LabelWorkingDir, LabelConfigFiles, LabelNetwork, LabelConfigHash}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("标签键 = %v, 期望 %v", keys, want)
	}
	// 资源指纹只取决于 (种类, 键, 真实名)，同样的输入永远得到同样的值。
	again := resourceLabels(p, "network", "default", "demo_default", "")
	if got[len(got)-1] != again[len(again)-1] || len(got[len(got)-1][1]) != 16 {
		t.Errorf("资源指纹不稳定或长度不对: %v", got[len(got)-1])
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"": "''", "abc": "abc", "a b": "'a b'", "it's": `'it'"'"'s'`,
		"a/b-c_d.e:f,g=h+i@j%k": "a/b-c_d.e:f,g=h+i@j%k", "中文": "'中文'", "a$b": "'a$b'", "~": "'~'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, 期望 %q", in, got, want)
		}
	}
	if got := shellJoin([]string{"wget", "-q", "http://x y"}); got != "wget -q 'http://x y'" {
		t.Errorf("shellJoin = %q", got)
	}
}
