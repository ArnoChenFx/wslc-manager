package selfupdate

// 这个文件验证发布流程第一步「检查签名密钥」真的会拦住配错的密钥。
//
// 为什么要真的执行那段脚本，而不是只看文本：这一步要是写坏了，
// 毛病要到打 tag 那天才暴露，而且最糟的情形是「照常发布、却没人能更新」。
//
// 为什么用 mygo keygen 生成密钥：这一步依赖一个事实——MyGo 的私钥文件是
// Go 的 ed25519 私钥（32 字节种子 + 32 字节公钥）的 base64，公钥就是后 32 字节。
// 用 MyGo 自己的 keygen 来生成，哪天它改了格式，这里会立刻变红，
// 而不是等到发布那天才发现校验把正确的密钥也拦了。

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// keyPair 是 mygo keygen 生成的一对密钥，内容已去掉首尾空白。
type keyPair struct{ private, public string }

// generateKeyPair 调 MyGo 自己的 keygen 生成一对新密钥。
func generateKeyPair(t *testing.T, repoRoot string) keyPair {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("go", "tool", "mygo", "keygen", "-o", dir)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mygo keygen 失败: %v\n%s", err, out)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		return strings.TrimSpace(string(data))
	}
	return keyPair{private: read("mygo-update.key"), public: read("mygo-update.pub")}
}

// runKeyCheck 在一个只含 mygo.json 的临时目录里执行脚本，返回退出码和全部输出。
func runKeyCheck(t *testing.T, pwsh, script, publicKey, secret string) (int, string) {
	t.Helper()
	work := t.TempDir()
	cfg := `{"updates":{"publicKey":"` + publicKey + `","github":"owner/repo"}}`
	if err := os.WriteFile(filepath.Join(work, "mygo.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", script)
	cmd.Dir = work
	// 排在后面的同名变量生效，所以开发机上本来就设了私钥也不会串进来。
	cmd.Env = append(os.Environ(), "MYGO_UPDATER_PRIVATE_KEY="+secret)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	default:
		t.Fatalf("启动 pwsh 失败: %v", err)
		return -1, ""
	}
}

// TestKeyCheckStepCatchesWrongKeys 验证检查步骤会拦住配错的签名密钥。
//
// 防的是这个缺陷：mygo build 用任何密钥都能成功签名，所以贴错了密钥
// （贴成公钥、贴成另一对密钥的私钥）时，工作流照常构建、照常发布，
// 但应用里内置的是另一把公钥，所有已安装的程序都会拒收这次更新，
// 而且从头到尾没有任何一步报错。
func TestKeyCheckStepCatchesWrongKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("要调 go tool mygo 和 pwsh，-short 下跳过")
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("没有 pwsh：发布流程跑在 pwsh 上，没有它就没法执行这段脚本")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// 取出工作流里那一步的脚本，原样写成文件来执行。
	steps := loadReleaseSteps(t)
	i := stepIndex(t, steps, "检查签名密钥", func(s workflowStep) bool {
		return strings.Contains(s.Run, "MYGO_UPDATER_PRIVATE_KEY") && !strings.Contains(s.Run, "mygo build")
	})
	script := filepath.Join(t.TempDir(), "check-key.ps1")
	if err := os.WriteFile(script, []byte(steps[i].Run), 0o644); err != nil {
		t.Fatal(err)
	}

	a := generateKeyPair(t, root) // 应用里内置的那一对
	b := generateKeyPair(t, root) // 另一对：比如本机上别的应用的密钥

	cases := []struct {
		name     string
		secret   string
		wantExit int
		wantMsg  string // 应出现在输出里的标题；空表示不该有任何 ::error
	}{
		{"私钥与公钥配对", a.private, 0, ""},
		{"粘贴时多带了结尾换行", a.private + "\r\n", 0, ""},
		{"另一对密钥的私钥", b.private, 1, "Wrong update signing key"},
		{"误贴了公钥", a.public, 1, "Bad update signing key"},
		{"根本不是 base64", "this is not a key", 1, "Bad update signing key"},
		{"没有设置", "", 1, "Missing update signing key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runKeyCheck(t, pwsh, script, a.public, tc.secret)
			if code != tc.wantExit {
				t.Errorf("退出码 = %d，期望 %d\n输出:\n%s", code, tc.wantExit, out)
			}
			if tc.wantMsg != "" && !strings.Contains(out, tc.wantMsg) {
				t.Errorf("输出里应有 %q\n输出:\n%s", tc.wantMsg, out)
			}
			if tc.wantMsg == "" && strings.Contains(out, "::error") {
				t.Errorf("正确的密钥不该报错\n输出:\n%s", out)
			}
		})
	}
}
