package selfupdate

// 这个文件是「更新配置与发布流程」的契约测试。
//
// 为什么放在这里：更新要真正生效，应用这边（本包）、mygo.json 里的更新配置、
// 发布流程（.github/workflows/release.yml）三处必须对得上，而发布流程的毛病
// 只有下次打 tag 才暴露——那时包已经发出去了，装了旧版的用户永远收不到更新。
// 所以把这几处约定写成测试，改坏了在 go test 里就红。
//
// 这些测试读取仓库根目录下的文件；go test 的工作目录是包目录，往上两级即根。

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// readRepoFile 读取仓库根目录下的文件。
func readRepoFile(t *testing.T, rel ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, rel...)...))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", filepath.Join(rel...), err)
	}
	return data
}

// TestMygoJSONUpdatesConfig 验证 mygo.json 里的更新配置能用。
//
// 防的是三个真实会犯的错：
//   - 把私钥（解码后 64 字节）而不是公钥（32 字节）贴进 mygo.json 并提交。
//     私钥一旦入库就等于泄露，任何人都能给用户推送代码；
//   - 照抄 mygo keygen 打印的模板，留下 "owner/name" 占位，应用就会去一个
//     不存在的仓库找更新，而且不报任何错；
//   - 设了和发布流程不一致的 tagPrefix，应用找不到我们打的 v* tag 的发布。
func TestMygoJSONUpdatesConfig(t *testing.T) {
	var cfg struct {
		Version string `json:"version"`
		Updates struct {
			PublicKey string `json:"publicKey"`
			GitHub    string `json:"github"`
			TagPrefix string `json:"tagPrefix"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(readRepoFile(t, "mygo.json"), &cfg); err != nil {
		t.Fatalf("mygo.json 不是合法 JSON: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(cfg.Updates.PublicKey)
	if err != nil {
		t.Fatalf("updates.publicKey 不是 base64: %v", err)
	}
	switch len(raw) {
	case 32: // ed25519 公钥，正确
	case 64:
		t.Fatal("updates.publicKey 解码后是 64 字节，这是私钥！立刻从仓库和历史里清掉，并重新生成密钥对")
	default:
		t.Fatalf("updates.publicKey 解码后应为 32 字节，实际 %d 字节", len(raw))
	}

	if cfg.Updates.GitHub == "owner/name" {
		t.Error(`updates.github 还是 mygo keygen 打印的模板 "owner/name"`)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(cfg.Updates.GitHub) {
		t.Errorf("updates.github 应为 owner/name 的形式，实际 %q", cfg.Updates.GitHub)
	}

	if p := cfg.Updates.TagPrefix; p != "" && p != "v" {
		t.Errorf("updates.tagPrefix 必须和发布流程的 v* tag 一致（空或 v），实际 %q", p)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(cfg.Version) {
		t.Errorf("mygo.json 的 version 应为 x.y.z，实际 %q", cfg.Version)
	}
}

// workflowStep 是工作流里一个步骤中我们关心的字段。
type workflowStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

// secretRef 是密钥在工作流里的引用写法。
const secretRef = "${{ secrets.MYGO_UPDATER_PRIVATE_KEY }}"

// loadReleaseSteps 解析发布工作流，返回唯一那个 job 的步骤。
func loadReleaseSteps(t *testing.T) []workflowStep {
	t.Helper()
	var wf struct {
		Jobs map[string]struct {
			Steps []workflowStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(readRepoFile(t, ".github", "workflows", "release.yml"), &wf); err != nil {
		t.Fatalf("release.yml 不是合法 YAML: %v", err)
	}
	if len(wf.Jobs) != 1 {
		t.Fatalf("release.yml 应只有一个 job，实际 %d 个", len(wf.Jobs))
	}
	for _, j := range wf.Jobs {
		return j.Steps
	}
	return nil
}

// stepIndex 返回第一个满足条件的步骤的下标，没有则失败。
func stepIndex(t *testing.T, steps []workflowStep, what string, match func(workflowStep) bool) int {
	t.Helper()
	for i, s := range steps {
		if match(s) {
			return i
		}
	}
	t.Fatalf("发布流程里找不到「%s」这一步", what)
	return -1
}

// TestReleaseWorkflowPublishesUpdates 验证发布流程会发出可用的更新。
//
// 防的是这几种「发布成功、但没人能更新」：
//   - 构建时没把私钥交给 mygo build：它照常成功，只是悄悄不生成更新文件；
//   - 私钥被交给了所有步骤（包括跑测试的那一步），白白扩大暴露面；
//   - 更新文件没有上传，或者在文件传完之前 manifest 就已经对外可见
//     （应用从「最新发布」读 manifest，所以先建草稿、传完再公开）；
//   - 测试步骤排在密钥检查之前，缺密钥要等十几分钟才报错。
func TestReleaseWorkflowPublishesUpdates(t *testing.T) {
	steps := loadReleaseSteps(t)

	keyCheck := stepIndex(t, steps, "检查签名密钥", func(s workflowStep) bool {
		return strings.Contains(s.Run, "MYGO_UPDATER_PRIVATE_KEY") && !strings.Contains(s.Run, "mygo build")
	})
	tests := stepIndex(t, steps, "跑测试", func(s workflowStep) bool { return strings.Contains(s.Run, "go test") })
	build := stepIndex(t, steps, "mygo build", func(s workflowStep) bool { return strings.Contains(s.Run, "mygo build") })
	verify := stepIndex(t, steps, "校验更新文件", func(s workflowStep) bool {
		return s.ID == "update" && strings.Contains(s.Run, "update-windows-amd64.json")
	})
	release := stepIndex(t, steps, "创建发布", func(s workflowStep) bool {
		return strings.HasPrefix(s.Uses, "softprops/action-gh-release")
	})
	publish := stepIndex(t, steps, "公开发布", func(s workflowStep) bool {
		return strings.Contains(s.Run, "gh release edit")
	})

	// 步骤顺序：先查密钥，再测试、构建、校验、建草稿、公开。
	order := []struct {
		name string
		idx  int
	}{{"检查密钥", keyCheck}, {"测试", tests}, {"构建", build}, {"校验更新文件", verify}, {"创建发布", release}, {"公开发布", publish}}
	for i := 1; i < len(order); i++ {
		if order[i-1].idx >= order[i].idx {
			t.Errorf("步骤顺序不对：「%s」应排在「%s」之前", order[i-1].name, order[i].name)
		}
	}

	// 私钥只给检查和构建这两步。
	if got := steps[build].Env["MYGO_UPDATER_PRIVATE_KEY"]; got != secretRef {
		t.Errorf("构建步骤必须把私钥交给 mygo build，env 里是 %q", got)
	}
	if got := steps[keyCheck].Env["MYGO_UPDATER_PRIVATE_KEY"]; got != secretRef {
		t.Errorf("检查步骤必须读到私钥才能判断有没有，env 里是 %q", got)
	}
	for i, s := range steps {
		if _, has := s.Env["MYGO_UPDATER_PRIVATE_KEY"]; has && i != keyCheck && i != build {
			t.Errorf("步骤 %d「%s」不该拿到私钥", i+1, s.Name)
		}
	}
	if strings.Contains(steps[tests].Run, "MYGO_UPDATER_PRIVATE_KEY") {
		t.Error("跑测试的步骤不该碰私钥")
	}

	// 创建发布：先是草稿，更新文件都在上传列表里，预发布 tag 不当作最新。
	rel := steps[release].With
	if d, ok := rel["draft"]; !ok || (d != true && d != "true") {
		t.Errorf("发布必须先建成草稿再公开，draft = %v", rel["draft"])
	}
	files, _ := rel["files"].(string)
	for _, want := range []string{"steps.update.outputs.archive", "steps.update.outputs.manifest", ".delta"} {
		if !strings.Contains(files, want) {
			t.Errorf("上传列表里缺少 %q：\n%s", want, files)
		}
	}
	if pre, _ := rel["prerelease"].(string); !strings.Contains(pre, "contains(github.ref_name, '-')") {
		t.Errorf("带连字符的 tag（如 v1.0.0-rc.1）应标成预发布，prerelease = %q", pre)
	}

	// 公开发布：只能在上传之后；用 gh 取消草稿状态，需要令牌。
	if !strings.Contains(steps[publish].Run, "--draft=false") {
		t.Errorf("公开发布这一步应取消草稿状态: %s", steps[publish].Run)
	}
	if steps[publish].Env["GH_TOKEN"] == "" {
		t.Error("gh 需要 GH_TOKEN 才能公开发布")
	}
}

// TestReleaseVerifyStepChecksWhatAppsNeed 验证「校验更新文件」这一步检查了应用依赖的几项。
//
// 防的是校验被悄悄删减：应用按 manifest 里的地址下载压缩包，所以版本、
// 下载地址（含 tag 和文件名）、签名、大小任何一项对不上，用户都会更新失败。
func TestReleaseVerifyStepChecksWhatAppsNeed(t *testing.T) {
	steps := loadReleaseSteps(t)
	i := stepIndex(t, steps, "校验更新文件", func(s workflowStep) bool { return s.ID == "update" })
	run := steps[i].Run
	for _, want := range []string{
		"$m.version",   // 版本要和 tag 一致
		"$m.url",       // 下载地址要指向本仓库、本 tag、正确文件名
		"$m.signature", // 必须有签名
		"$m.size",      // 大小要和压缩包一致
		"GITHUB_OUTPUT",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("校验步骤里缺少对 %s 的检查", want)
		}
	}
}
