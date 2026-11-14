package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// hashPrefix 标明指纹的格式版本。
//
// 指纹算法或参数格式变了就换一个前缀：带旧前缀的容器会被判定为「配置已变」，
// 在下次 up 时重建一次——这正是想要的效果，不需要任何迁移逻辑。
const hashPrefix = "g1:"

// serviceHash 返回服务配置的指纹，写进容器标签 com.wslc-compose.config-hash。
// `up` 靠它判断「配置变了没有、要不要重建容器」：指纹相同就保留，不同就重建。
//
// 做法：对**生成出来的 wslc 参数**求哈希。参数向量完整决定了容器的样子，所以：
//   - 自动覆盖所有我们真正会应用的字段；
//   - 自动忽略 wslc 不支持、被我们丢弃的字段（改它们不该重建容器）；
//   - 以后新增参数也不需要同步修改指纹逻辑。
//
// 参数里没有体现、但会影响容器的内容单独补进来：次要网络、secrets/configs 的文件内容、
// extra_hosts（启动后才应用）、构建设置、`command: []` 这种显式清空。
// 不参与的：depends_on（只影响启动顺序）、profiles、副本数、构建上下文路径。
//
// 副作用说明：参数的顺序或格式变了，指纹就变，现有容器会重建一次。改 runArgs 时要有意为之。
func serviceHash(p *types.Project, s types.ServiceConfig, env argsEnv) (string, error) {
	args, err := runArgs(p, s, 1, runSpec{forHash: true}, env)
	if err != nil {
		return "", err
	}
	parts := append([]string(nil), args...)

	if s.Command != nil && len(s.Command) == 0 {
		parts = append(parts, "|command-reset")
	}
	for _, n := range serviceNetworks(p, s) {
		// 第一个是主网络，已在 run 参数里；其余的是 create 之后 connect 的。
		parts = append(parts, "|net-decl", n.name, fmt.Sprintf("%t", p.Networks[n.key].Internal), p.Networks[n.key].Driver)
	}
	if nets := serviceNetworks(p, s); s.NetworkMode == "" && len(nets) > 1 {
		for _, n := range nets[1:] {
			parts = append(parts, "|connect")
			parts = append(parts, networkConnectArgs(s, n, "")...)
		}
	}
	for _, v := range s.Volumes {
		if v.Type == types.VolumeTypeVolume {
			if vol, ok := p.Volumes[v.Source]; ok {
				parts = append(parts, "|vol-decl", vol.Name, vol.Driver, fmt.Sprint(vol.DriverOpts), fmt.Sprintf("%t", bool(vol.External)))
			}
		}
	}
	// 文件内容变了容器要重建，所以带上内容的哈希而不只是路径。
	for _, ref := range s.Secrets {
		parts = append(parts, "|secret", ref.Source, fileDigest(p.Secrets[ref.Source].File))
	}
	for _, ref := range s.Configs {
		parts = append(parts, "|config", ref.Source, fileDigest(p.Configs[ref.Source].File))
	}
	var hosts []string
	for host, ips := range s.ExtraHosts {
		hosts = append(hosts, host+"="+strings.Join(ips, ","))
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		parts = append(parts, "|extra-host", h)
	}
	if b := s.Build; b != nil {
		parts = append(parts, "|build", b.Dockerfile, b.Target, strconv.FormatBool(b.Pull), strconv.FormatBool(b.NoCache))
		for _, k := range sortedKeys(b.Args) {
			v := ""
			if b.Args[k] != nil {
				v = *b.Args[k]
			}
			parts = append(parts, k+"="+v)
		}
	}

	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0}) // 用 NUL 分隔，避免 ["a b"] 与 ["a", "b"] 哈希相同
	}
	return hashPrefix + hex.EncodeToString(h.Sum(nil))[:32], nil
}

// fileDigest 返回文件内容的 sha256（十六进制）。
//
// 文件读不出来时退而哈希路径本身：这样 up 仍然能工作，并且路径变化依旧会被检测到。
func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		sum := sha256.Sum256([]byte("path:" + path))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
