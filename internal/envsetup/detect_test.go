package envsetup

import (
	"context"
	"testing"
)

// TestCompareVersions 覆盖版本比较的边界。
//
// 这段逻辑决定「有新版本」按钮是否出现，比错会误导用户：
// 把旧版说成最新，或一直提示已是最新。
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.6.0", "0.6.0", 0},
		{"0.6.0", "0.7.0", -1},
		{"0.7.0", "0.6.0", 1},
		{"0.6.0", "0.6.1", -1},
		{"1.0", "0.9.9", 1},
		// 段数不同：缺的段按 0 算
		{"1.0", "1.0.0", 0},
		{"1.0.1", "1.0", 1},
		// 预发布后缀按数字前缀比较，方向仍然正确
		{"0.6.0", "0.7.0rc1", -1},
		{"0.7.0rc1", "0.7.0", 0},
		// 非数字段按 0
		{"abc", "0.0.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, 期望 %d", c.a, c.b, got, c.want)
		}
	}
}

// TestFirstVersion 从命令行输出里取版本号。
func TestFirstVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"wslc-compose 0.6.0\n", "0.6.0"},
		{"wslc-compose version 1.2.3", "1.2.3"},
		{"v2.0", "2.0"},
		{"no version here", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := firstVersion(c.in); got != c.want {
			t.Errorf("firstVersion(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestLooksLikeVersion 判断版本号形态。
func TestLooksLikeVersion(t *testing.T) {
	for _, s := range []string{"3.0.5.0", "6.6.87.2-1", "1.0"} {
		if !looksLikeVersion(s) {
			t.Errorf("%q 应被识别为版本号", s)
		}
	}
	for _, s := range []string{"", "abc", "1", "x.y"} {
		if looksLikeVersion(s) {
			t.Errorf("%q 不应被识别为版本号", s)
		}
	}
}

// TestComposeOutdated 检查「有新版本」的判定。
//
// 关键点：任一边拿不到版本时不报「有更新」，否则会诱导用户
// 去升级一个其实已经最新的东西。
func TestComposeOutdated(t *testing.T) {
	cases := []struct {
		name          string
		local, latest string
		want          bool
	}{
		{"落后", "0.6.0", "0.7.0", true},
		{"相同", "0.6.0", "0.6.0", false},
		{"本地更新", "0.8.0", "0.7.0", false},
		{"本地未知", "", "0.7.0", false},
		{"远端未知", "0.6.0", "", false},
		{"都未知", "", "", false},
	}
	for _, c := range cases {
		st := Status{ComposeVersion: c.local, Latest: c.latest}
		if got := st.ComposeOutdated(); got != c.want {
			t.Errorf("%s: ComposeOutdated() = %v, 期望 %v", c.name, got, c.want)
		}
	}
}

// TestInstallArgShapes 检查四种安装/升级命令的形态。
//
// 这些命令会被真的执行，参数写错会在用户机器上装错东西，
// 所以逐项断言，而不是只测「非空」。
func TestInstallArgShapes(t *testing.T) {
	t.Run("uv 安装用固定版本", func(t *testing.T) {
		st := Status{Upgrader: "uv", Latest: "0.7.0"}
		bin, args := st.InstallComposeArgs()
		if bin != "uv" {
			t.Fatalf("bin = %q", bin)
		}
		want := []string{"tool", "install", "wslc-compose==0.7.0"}
		if len(args) != len(want) {
			t.Fatalf("args = %v", args)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Errorf("args[%d] = %q, 期望 %q", i, args[i], want[i])
			}
		}
	})

	t.Run("uv 升级不带版本", func(t *testing.T) {
		st := Status{Upgrader: "uv"}
		_, args := st.UpgradeComposeArgs()
		if len(args) != 3 || args[2] != "wslc-compose" {
			t.Errorf("args = %v", args)
		}
	})

	t.Run("pipx 安装", func(t *testing.T) {
		st := Status{Upgrader: "pipx", Latest: "0.7.0"}
		bin, args := st.InstallComposeArgs()
		if bin != "pipx" || len(args) != 2 || args[0] != "install" {
			t.Errorf("bin = %q, args = %v", bin, args)
		}
	})

	t.Run("没有安装器时返回空", func(t *testing.T) {
		st := Status{}
		if bin, args := st.InstallComposeArgs(); bin != "" || args != nil {
			t.Errorf("期望空, got %q %v", bin, args)
		}
		if bin, args := st.UpgradeComposeArgs(); bin != "" || args != nil {
			t.Errorf("期望空, got %q %v", bin, args)
		}
	})

	t.Run("winget 安装 WSL 带静默参数", func(t *testing.T) {
		st := Status{Winget: `C:\winget.exe`}
		bin, args := st.InstallWSLArgs()
		if bin != st.Winget {
			t.Errorf("bin = %q", bin)
		}
		joined := ""
		for _, a := range args {
			joined += a + " "
		}
		for _, want := range []string{"install", "Microsoft.WSL", "--exact", "--silent"} {
			if !containsArg(args, want) {
				t.Errorf("缺少参数 %q，实际 %q", want, joined)
			}
		}
	})

	t.Run("没有 winget 时不返回命令", func(t *testing.T) {
		st := Status{}
		if bin, args := st.InstallWSLArgs(); bin != "" || args != nil {
			t.Errorf("期望空, got %q %v", bin, args)
		}
	})
}

// containsArg 判断参数列表里是否有某个值。
func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestDetectRuns 在真实的开发机上跑一次检测。
//
// 它只读、不写系统，所以可以安全地作为常规测试；但结果依赖机器环境，
// 因此只断言「不 panic 且基本结构合理」，不断言具体路径。
func TestDetectRuns(t *testing.T) {
	st := Detect(context.Background())

	if st.CheckedAt.IsZero() {
		t.Error("CheckedAt 未设置")
	}
	// 找到路径时，版本号要么是空（取不到），要么形如版本。
	if st.ComposePath != "" && st.ComposeVersion != "" && !looksLikeVersion(st.ComposeVersion) {
		t.Errorf("ComposeVersion 形态异常: %q", st.ComposeVersion)
	}
	// Upgrader 只允许这三种取值。
	switch st.Upgrader {
	case "", "uv", "pipx":
	default:
		t.Errorf("Upgrader 取值异常: %q", st.Upgrader)
	}
	t.Logf("wslc=%q (WSL %s) compose=%q (%s) latest=%q upgrader=%q winget=%v",
		st.WSLCPath, st.WSLVersion, st.ComposePath, st.ComposeVersion,
		st.Latest, st.Upgrader, st.Winget != "")
}
