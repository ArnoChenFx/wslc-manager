package compose

import (
	"reflect"
	"strings"
	"testing"
)

// 告警：wslc 做不到、但可以忽略的字段。文本和顺序都固定，用户在输出面板里会看到。
func TestCheckWarnings(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		extra map[string]string
		want  []string
	}{
		{
			name: "全部会被忽略的字段，按固定顺序告警",
			yaml: `
services:
  a:
    image: x
    privileged: true
    devices: ["/dev/ttyUSB0:/dev/ttyUSB0"]
    sysctls: {net.core.somaxconn: 1024}
    init: true
    pid: host
    ipc: host
    read_only: true
    security_opt: ["no-new-privileges:true"]
    logging: {driver: json-file}
`,
			want: []string{
				"a: privileged mode is not supported by wslc (ignoring 'privileged')",
				"a: device mapping is not supported by wslc (see 'gpus' for GPUs) (ignoring 'devices')",
				"a: sysctls are not supported by wslc (ignoring 'sysctls')",
				"a: init is not supported by wslc (ignoring 'init')",
				"a: pid mode is not supported by wslc (ignoring 'pid')",
				"a: ipc mode is not supported by wslc (ignoring 'ipc')",
				"a: read_only rootfs is not supported by wslc (ignoring 'read_only')",
				"a: security_opt is not supported by wslc (ignoring 'security_opt')",
				"a: logging drivers are not configurable with wslc (ignoring 'logging')",
			},
		},
		{
			name: "restart：只有 always 和 no 被支持",
			yaml: "services:\n  a:\n    image: x\n    restart: unless-stopped\n  b:\n    image: x\n    restart: always\n  c:\n    image: x\n    restart: 'no'\n",
			want: []string{"a: restart policy 'unless-stopped' is not supported by wslc (only 'always' and 'no'); ignoring it"},
		},
		{
			name: "extra_hosts 在启动后才生效",
			yaml: "services:\n  a:\n    image: x\n    extra_hosts: ['h:10.0.0.1']\n",
			want: []string{"a: extra_hosts is applied after start; the entrypoint may not observe it"},
		},
		{
			name: "不支持的挂载选项",
			yaml: `
services:
  a:
    image: x
    volumes:
      - type: bind
        source: C:\data
        target: /b
        bind: {propagation: rshared, selinux: z}
      - type: volume
        source: v
        target: /v
        volume: {nocopy: true, subpath: sub}
volumes:
  v: {}
`,
			want: []string{
				"a: mount /b: bind.propagation is not supported by wslc (ignoring it)",
				"a: mount /b: bind.selinux is not supported by wslc (ignoring it)",
				"a: mount /v: volume.nocopy is not supported by wslc (ignoring it)",
				"a: mount /v: volume.subpath is not supported by wslc (ignoring it)",
			},
		},
		{
			name:  "secret 的属主和权限无法通过文件挂载实现",
			extra: map[string]string{"pw.txt": "x"},
			yaml: `
secrets:
  pw:
    file: ./pw.txt
services:
  a:
    image: x
    secrets:
      - source: pw
        uid: "103"
        mode: 0440
`,
			want: []string{"a: secret 'pw': uid, mode cannot be enforced by wslc file mounts and will be ignored"},
		},
		{
			name: "没有问题就没有告警",
			yaml: "services:\n  a:\n    image: x\n    restart: always\n    ports: ['80:80']\n",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := loadYAML(t, tc.yaml, tc.extra)
			got, err := checkProject(p)
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("告警不一致:\n got  %q\n want %q", got, tc.want)
			}
		})
	}
}

// 错误：忽略了就会悄悄做错事的用法，必须在运行前拦住。
func TestCheckErrors(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		extra map[string]string
		want  string // 错误信息应包含的片段
	}{
		{
			name: "匿名卷",
			yaml: "services:\n  a:\n    image: x\n    volumes: ['/data']\n",
			want: "anonymous volumes are not supported by wslc",
		},
		{
			name:  "build.secrets",
			extra: map[string]string{"s.txt": "x"},
			yaml:  "secrets:\n  s:\n    file: ./s.txt\nservices:\n  a:\n    build:\n      context: .\n      secrets: [s]\n",
			want:  "build.secrets is not supported",
		},
		{
			name: "外部 secret",
			yaml: "secrets:\n  s:\n    external: true\nservices:\n  a:\n    image: x\n    secrets: [s]\n",
			want: "external secrets are not supported",
		},
		{
			name: "来自环境变量的 secret",
			yaml: "secrets:\n  s:\n    environment: SOME_VAR\nservices:\n  a:\n    image: x\n    secrets: [s]\n",
			want: "only file-backed secrets are supported",
		},
		{
			name: "secret 文件不存在",
			yaml: "secrets:\n  s:\n    file: ./nope.txt\nservices:\n  a:\n    image: x\n    secrets: [s]\n",
			want: "file not found",
		},
		{
			name: "IPv6 网络",
			yaml: "networks:\n  n:\n    enable_ipv6: true\nservices:\n  a:\n    image: x\n    networks: [n]\n",
			want: "IPv4 networks without attachable mode",
		},
		{
			name: "attachable 网络",
			yaml: "networks:\n  n:\n    attachable: true\nservices:\n  a:\n    image: x\n    networks: [n]\n",
			want: "IPv4 networks without attachable mode",
		},
		{
			name: "一个网络多个子网",
			yaml: "networks:\n  n:\n    ipam:\n      config:\n        - subnet: 10.0.0.0/24\n        - subnet: 10.1.0.0/24\nservices:\n  a:\n    image: x\n    networks: [n]\n",
			want: "one IPv4 subnet per network",
		},
		{
			name: "自定义 IPAM 驱动",
			yaml: "networks:\n  n:\n    ipam:\n      driver: custom\nservices:\n  a:\n    image: x\n    networks: [n]\n",
			want: "custom IPAM drivers/options are not supported",
		},
		{
			name: "等待 healthy 的依赖关掉了健康检查",
			yaml: "services:\n  a:\n    image: x\n    depends_on:\n      b:\n        condition: service_healthy\n  b:\n    image: x\n    healthcheck:\n      disable: true\n",
			want: "disables its healthcheck",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := loadYAML(t, tc.yaml, tc.extra)
			_, err := checkProject(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误 = %v, 应包含 %q", err, tc.want)
			}
		})
	}
}
