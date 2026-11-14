package envsetup

import (
	"context"
	"testing"
	"unicode/utf16"
)

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

// TestWingetArgShapes 检查 winget 命令的形状：必须静默、必须精确匹配包 ID。
//
// 不带 --accept-* 的话安装会卡在交互式协议确认上，界面上看起来就是「点了没反应」；
// 不带 --exact 的话 winget 会按模糊匹配，可能装错包。
func TestWingetArgShapes(t *testing.T) {
	st := Status{Winget: `C:\winget.exe`}
	for name, build := range map[string]func() (string, []string){
		"安装": st.InstallWSLArgs,
		"更新": st.UpgradeWSLArgs,
	} {
		bin, args := build()
		if bin != st.Winget {
			t.Errorf("%s：bin = %q", name, bin)
		}
		for _, want := range []string{"Microsoft.WSL", "--exact", "--silent", "--accept-package-agreements", "--accept-source-agreements"} {
			if !containsArg(args, want) {
				t.Errorf("%s：缺少参数 %q，实际 %v", name, want, args)
			}
		}
	}
	_, installArgs := st.InstallWSLArgs()
	_, upgradeArgs := st.UpgradeWSLArgs()
	if !containsArg(installArgs, "install") || !containsArg(upgradeArgs, "upgrade") {
		t.Error("子命令不对：应分别是 install 与 upgrade")
	}

	// 没有 winget 时不返回命令，而不是返回一个空的 bin。
	empty := Status{}
	if bin, args := empty.InstallWSLArgs(); bin != "" || args != nil {
		t.Errorf("期望空, got %q %v", bin, args)
	}
	if bin, args := empty.UpgradeWSLArgs(); bin != "" || args != nil {
		t.Errorf("期望空, got %q %v", bin, args)
	}
	if empty.CanInstallWSL() || !st.CanInstallWSL() {
		t.Error("CanInstallWSL 应取决于是否找到 winget")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestDecodeUTF16LE 覆盖 wsl.exe 输出的解码。
//
// 回归测试：`wsl.exe --version` 输出 UTF-16LE。早先按 UTF-8 读，得到夹杂 NUL 的乱码，
// 解析版本号永远匹配不上，系统页的 WSL 版本一直是空的。
func TestDecodeUTF16LE(t *testing.T) {
	encode := func(s string) []byte {
		var b []byte
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	}
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"英文", encode("WSL version: 3.0.5.0\r\n"), "WSL version: 3.0.5.0\r\n"},
		{"中文", encode("WSL 版本: 3.0.5.0"), "WSL 版本: 3.0.5.0"},
		{"UTF-8 原样返回", []byte("plain utf-8 文本"), "plain utf-8 文本"},
		{"空", nil, ""},
		{"一个字节", []byte("x"), "x"},
	}
	for _, tc := range tests {
		if got := decodeUTF16LE(tc.in); got != tc.want {
			t.Errorf("%s：got %q, want %q", tc.name, got, tc.want)
		}
	}
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
	if st.WSLCPath == "" && st.WSLVersion != "" {
		t.Errorf("没找到 wslc 时不应去读 WSL 版本，got %q", st.WSLVersion)
	}
	if st.WSLVersion != "" && !looksLikeVersion(st.WSLVersion) {
		t.Errorf("WSLVersion 形态异常: %q", st.WSLVersion)
	}
	t.Logf("wslc=%q (WSL %s) winget=%v", st.WSLCPath, st.WSLVersion, st.Winget != "")
}
