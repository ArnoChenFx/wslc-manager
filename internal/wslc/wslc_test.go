package wslc

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestParseLabels(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "空串",
			in:   "",
			want: map[string]string{},
		},
		{
			name: "简单键值",
			in:   "a=1,b=2",
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "只有一个键",
			in:   "com.example.key=value",
			want: map[string]string{"com.example.key": "value"},
		},
		{
			name: "值里含逗号",
			in:   `com.foo.meta={"a":1,"b":2},com.foo.svc=web`,
			want: map[string]string{"com.foo.meta": `{"a":1,"b":2}`, "com.foo.svc": "web"},
		},
		{
			name: "值里有等号",
			in:   "cmd=a=b,c=d",
			want: map[string]string{"cmd": "a=b", "c": "d"},
		},
		{
			name: "wslc-compose 实际标签",
			in: "com.microsoft.wsl.container.metadata={\"V1\":{\"Flags\":0}}," +
				"com.wslc-compose.config-hash=2614289a868073a1," +
				"com.wslc-compose.container-number=1," +
				"com.wslc-compose.project=open-compute," +
				"com.wslc-compose.service=ocd",
			want: map[string]string{
				"com.microsoft.wsl.container.metadata": `{"V1":{"Flags":0}}`,
				"com.wslc-compose.config-hash":         "2614289a868073a1",
				"com.wslc-compose.container-number":    "1",
				"com.wslc-compose.project":             "open-compute",
				"com.wslc-compose.service":             "ocd",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseLabels(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseLabels(%q)\n got=%v\nwant=%v", tc.in, got, tc.want)
			}
		})
	}
}

func TestContainerLabel(t *testing.T) {
	c := Container{
		Labels: "com.wslc-compose.project=demo,com.wslc-compose.service=web",
	}
	if got := c.Label("com.wslc-compose.project"); got != "demo" {
		t.Errorf("project label = %q, want demo", got)
	}
	if got := c.Label("missing"); got != "" {
		t.Errorf("missing label = %q, want empty", got)
	}
}

// TestContainerState 只验证「是否在运行」的判断。
//
// 面向用户的状态文案（运行中 / 已停止……）不在这一层：它要随界面语言变化，
// 由 internal/app 的 stateLabel 负责，并在那里测试。
// 早先这里直接返回写死的中文，英文界面的状态列也是中文。
func TestContainerState(t *testing.T) {
	tests := []struct {
		state  string
		health string
		docker bool
	}{
		{"running", "", true},
		{"running", "healthy", true},
		{"running", "unhealthy", true},
		{"exited", "", false},
		{"created", "", false},
		{"paused", "", false},
		{"", "", false},
		{"weird", "", false},
	}
	for _, tc := range tests {
		c := Container{State: tc.state, Health: tc.health}
		if c.IsRunning() != tc.docker {
			t.Errorf("State=%q IsRunning=%v, want %v", tc.state, c.IsRunning(), tc.docker)
		}
	}
}

func TestImageRef(t *testing.T) {
	tests := []struct {
		img  Image
		want string
	}{
		{Image{Repository: "nginx", Tag: "alpine"}, "nginx:alpine"},
		{Image{Repository: "ghcr.io/a/b", Tag: "latest"}, "ghcr.io/a/b:latest"},
		{Image{Repository: "<none>", Tag: "<none>", ID: "abc123"}, "abc123"},
		{Image{Repository: "only-repo", Tag: "<none>"}, "only-repo"},
		{Image{Repository: "", Tag: "", ID: "deadbeef"}, "deadbeef"},
	}
	for _, tc := range tests {
		if got := tc.img.Ref(); got != tc.want {
			t.Errorf("Ref() = %q, want %q", got, tc.want)
		}
	}
}

func TestImageDangling(t *testing.T) {
	if !(Image{Repository: "<none>"}).Dangling() {
		t.Error("<none> 应视为悬空镜像")
	}
	if !(Image{Repository: ""}).Dangling() {
		t.Error("空仓库应视为悬空镜像")
	}
	if (Image{Repository: "nginx"}).Dangling() {
		t.Error("有仓库名不应视为悬空镜像")
	}
}

func TestNetworkIsBuiltin(t *testing.T) {
	for _, name := range []string{"bridge", "host", "none"} {
		if !(Network{Name: name}).IsBuiltin() {
			t.Errorf("%q 应为内置网络", name)
		}
	}
	if (Network{Name: "demo_default"}).IsBuiltin() {
		t.Error("自定义网络不应视为内置网络")
	}
}

func TestParseInfo(t *testing.T) {
	raw := `客户端:
WSL 版本: 3.0.5.0
内核版本: 6.18.58.1-1
Direct3D 版本: 1.611.1-81528511
DXCore 版本: 10.0.26100.1-240331-1435.ge-release
Windows 版本: 10.0.26300.9974
设置文件: C:\Users\Alice\AppData\Local\wslc\settings.yaml

服务器:
会话管理器版本: 3.0.5
会话: 1
ID        创建者 PID   显示名称
1         57024     wslc-cli-Alice
`
	info := ParseInfo(raw)
	checks := map[string]string{
		"WSLVersion":    info.WSLVersion,
		"KernelVersion": info.KernelVersion,
		"WindowsVer":    info.WindowsVer,
		"SettingsFile":  info.SettingsFile,
		"SessionMgr":    info.SessionMgr,
		"SessionCount":  info.SessionCount,
	}
	wants := map[string]string{
		"WSLVersion":    "3.0.5.0",
		"KernelVersion": "6.18.58.1-1",
		"WindowsVer":    "10.0.26300.9974",
		"SettingsFile":  `C:\Users\Alice\AppData\Local\wslc\settings.yaml`,
		"SessionMgr":    "3.0.5",
		"SessionCount":  "1",
	}
	for k, got := range checks {
		if got != wants[k] {
			t.Errorf("%s = %q, want %q", k, got, wants[k])
		}
	}
	if info.Raw != raw {
		t.Error("Raw 应保留原始输出")
	}
}

func TestParseInfoEnglish(t *testing.T) {
	raw := "WSL Version: 3.0.5.0\nKernel Version: 6.1\n"
	info := ParseInfo(raw)
	if info.WSLVersion != "3.0.5.0" || info.KernelVersion != "6.1" {
		t.Errorf("英文输出解析失败: %+v", info)
	}
}

func TestDecodeNDJSON(t *testing.T) {
	data := `{"ID":"a","Names":"x"}
{"ID":"b","Names":"y"}
`
	var out []Container
	if err := decodeNDJSON(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].ID != "a" || out[1].Names != "y" {
		t.Fatalf("解码结果错误: %+v", out)
	}
}

func TestDecodeNDJSONEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n"} {
		var out []Container
		if err := decodeNDJSON(in, &out); err != nil {
			t.Fatalf("空输入 %q 应无错误: %v", in, err)
		}
		if len(out) != 0 {
			t.Fatalf("空输入 %q 应得到空列表, got %+v", in, out)
		}
	}
}

func TestDecodeNDJSONArray(t *testing.T) {
	data := `[{"ID":"a"},{"ID":"b"}]`
	var out []Container
	if err := decodeNDJSON(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("数组格式解码失败: %+v", out)
	}
}

func TestSortContainers(t *testing.T) {
	cs := []Container{
		{Names: "zeta", State: "exited"},
		{Names: "alpha", State: "running"},
		{Names: "beta", State: "exited"},
		{Names: "Beta", State: "running"},
	}
	sortContainers(cs)
	if !cs[0].IsRunning() || !cs[1].IsRunning() {
		t.Fatalf("运行中的容器应排在前面: %+v", cs)
	}
	// 运行中的按名字排序（忽略大小写）：alpha < Beta
	if cs[0].Names != "alpha" || cs[1].Names != "Beta" {
		t.Errorf("运行中排序错误: %q, %q", cs[0].Names, cs[1].Names)
	}
	if cs[2].Names != "beta" || cs[3].Names != "zeta" {
		t.Errorf("已停止排序错误: %q, %q", cs[2].Names, cs[3].Names)
	}
}

func TestResultError(t *testing.T) {
	ok := Result{ExitCode: 0}
	if ok.Error() != nil {
		t.Error("成功结果不应有 error")
	}
	bad := Result{ExitCode: 1, Args: []string{"list"}, Stderr: "boom"}
	err := bad.Error()
	if err == nil {
		t.Fatal("失败结果应有 error")
	}
	if !contains(err.Error(), "boom") || !contains(err.Error(), "list") {
		t.Errorf("错误信息应包含命令与原因: %v", err)
	}
}

func TestResultCombined(t *testing.T) {
	tests := []struct {
		name string
		in   Result
		want string
	}{
		{"都有，无尾换行", Result{Stdout: "out", Stderr: "err"}, "out\nerr"},
		{"stdout 已有尾换行", Result{Stdout: "out\n", Stderr: "err"}, "out\nerr"},
		{"只有 stdout", Result{Stdout: "out"}, "out"},
		{"只有 stderr", Result{Stderr: "err"}, "err"},
		{"都为空", Result{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Combined(); got != tc.want {
				t.Errorf("Combined() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseSessionsTable(t *testing.T) {
	out := "ID        创建者 PID   显示名称\n1         57024     wslc-cli-Alice\n"
	got := ParseSessionsTable(out)
	if len(got) != 1 {
		t.Fatalf("应解析出 1 个会话, got %+v", got)
	}
	if got[0].ID != "1" || got[0].PID != "57024" || got[0].DisplayName != "wslc-cli-Alice" {
		t.Errorf("会话字段错误: %+v", got[0])
	}
}

func TestParseSessionsTableEmpty(t *testing.T) {
	if got := ParseSessionsTable(""); len(got) != 0 {
		t.Errorf("空输入应返回空列表, got %+v", got)
	}
	if got := ParseSessionsTable("only one line"); len(got) != 0 {
		t.Errorf("只有表头应返回空列表, got %+v", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestRunHookDoesNotDoubleUnlock 验证 Run 在设置回调后不会重复解锁互斥量。
//
// 回归测试：曾经在 Run 里同时保留了 defer c.mu.Unlock() 和显式 Unlock，
// 导致运行期直接崩溃（fatal error: sync: unlock of unlocked mutex）。
// 这个测试用真实 wslc 跑两条命令，并断言回调被调用且不 panic。
func TestRunHookDoesNotDoubleUnlock(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Skipf("跳过：%v", err)
	}

	var mu sync.Mutex
	var got int
	c.SetRunHook(func(r Result) {
		mu.Lock()
		got++
		mu.Unlock()
	})

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.Run(ctx, "--version"); err != nil {
			t.Fatalf("第 %d 次执行失败: %v", i+1, err)
		}
	}

	mu.Lock()
	n := got
	mu.Unlock()
	if n != 3 {
		t.Errorf("回调应被调用 3 次, got %d", n)
	}

	// 继续调用一次，确认互斥量状态仍然正常（重复解锁会直接崩溃）。
	if _, err := c.Run(ctx, "--version"); err != nil {
		t.Fatalf("回调之后再执行失败: %v", err)
	}
}
