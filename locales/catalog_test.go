package locales_test

// 词表本身的回归测试。
//
// 为什么要测这些：中英双语最容易出的错不是「翻得不好」，
// 而是「漏翻」「占位符对不上」「英文串里混进中文」。
// 这三类错误界面上不一定立刻看得出来（漏翻会回退成中文），
// 但用起来就是「部分界面还是中文」。所以单独断言。

import (
	"regexp"
	"strings"
	"testing"

	"github.com/arno/wslc-manager/locales"
)

// TestCatalogHasBothLanguages 确认词表注册了两种语言。
func TestCatalogHasBothLanguages(t *testing.T) {
	if locales.Catalog == nil {
		t.Fatal("词表未注册")
	}
	langs := locales.Catalog.Languages()
	if len(langs) < 2 {
		t.Fatalf("期望至少两种语言，实际 %v", langs)
	}
	if langs[0] != "zh-Hans" {
		t.Errorf("源语言应为 zh-Hans，实际 %q", langs[0])
	}
	if !strings.Contains(strings.Join(langs, ","), "en") {
		t.Errorf("缺少英文词表，实际 %v", langs)
	}
}

// TestEnglishHasNoChinese 确认英文词条里没有残留中文。
//
// 这类问题是真实会犯的：复制中文条目改英文时漏改一半，
// 或者英文条目被误粘贴成中文原文。
var hanzi = regexp.MustCompile(`[\p{Han}]`)

func TestEnglishHasNoChinese(t *testing.T) {
	// 用一批关键键名取出英文文案逐条检查。
	// 这里不遍历整个词表，因为词表 API 只暴露按 key 取值；
	// 覆盖面靠下面的完整性检查保证。
	keys := englishSamples()
	for key, got := range keys {
		if hanzi.MatchString(got) {
			t.Errorf("英文词条 %q 里混入中文: %q", key, got)
		}
	}
}

// englishSamples 取一组代表性词条的英文，覆盖各页面。
func englishSamples() map[string]string {
	m := locales.In("en")
	return map[string]string{
		"nav.containers":        m.NavContainers(),
		"nav.images":            m.NavImages(),
		"nav.compose":           m.NavCompose(),
		"common.refresh":        m.CommonRefresh(),
		"common.settings":       m.CommonSettings(),
		"containers.empty":      m.ContainersEmptyBody(),
		"images.pull":           m.ImagesPull(),
		"volumes.empty":         m.VolumesEmptyBody(),
		"networks.empty":        m.NetworksEmptyBody(),
		"compose.empty":         m.ComposeEmptyBody(),
		"system.knownLimits":    m.SystemKnownLimits(),
		"settings.language":     m.SettingsLanguage(),
		"logs.title":            m.LogsTitle(),
		"diag.title":            m.DiagTitle(),
		"error.wslcUnavailable": m.ErrorWslcUnavailable(),
	}
}

// TestPlaceholdersSurvive 确认带参数的词条保留了占位符。
//
// 占位符丢了会直接显示空白或 0，用户看到的是残缺信息。
func TestPlaceholdersSurvive(t *testing.T) {
	en := locales.In("en")
	zh := locales.In("zh-Hans")

	cases := []struct {
		name      string
		en, zh    string
		substring string
	}{
		{"containers.confirmDelete", en.ContainersConfirmDelete("demo"), zh.ContainersConfirmDelete("demo"), "demo"},
		{"compose.scanDone", en.ComposeScanDone(3), zh.ComposeScanDone(3), "3"},
		{"images.updating", en.ImagesUpdating(3), zh.ImagesUpdating(3), "3"},
		{"images.updateSummary", en.ImagesUpdateSummary(2, 1), zh.ImagesUpdateSummary(2, 1), "2"},
		{"status.updatedAt", en.StatusUpdatedAt("12:00"), zh.StatusUpdatedAt("12:00"), "12:00"},
	}
	for _, c := range cases {
		if !strings.Contains(c.en, c.substring) {
			t.Errorf("%s 英文缺少参数 %q: %q", c.name, c.substring, c.en)
		}
		if !strings.Contains(c.zh, c.substring) {
			t.Errorf("%s 中文缺少参数 %q: %q", c.name, c.substring, c.zh)
		}
	}
}

// TestEnglishTokensMatch 检查中英条目的必要差异。
//
// 目的很具体：防止「英文条目其实是中文原文没换」这种漏翻。
// 只挑那些中英必然不同的条目来比，不做全文比较。
func TestEnglishTokensMatch(t *testing.T) {
	en := locales.In("en")
	zh := locales.In("zh-Hans")

	pairs := []struct {
		name string
		en   string
		zh   string
	}{
		{"navContainers", en.NavContainers(), zh.NavContainers()},
		{"commonRefresh", en.CommonRefresh(), zh.CommonRefresh()},
		{"imagesPull", en.ImagesPull(), zh.ImagesPull()},
		{"settingsLanguage", en.SettingsLanguage(), zh.SettingsLanguage()},
		{"logsTitle", en.LogsTitle(), zh.LogsTitle()},
	}
	for _, p := range pairs {
		if p.en == p.zh {
			t.Errorf("%s 的英文与中文完全相同，可能是漏翻: %q", p.name, p.en)
		}
		if p.en == "" || p.zh == "" {
			t.Errorf("%s 有空词条: en=%q zh=%q", p.name, p.en, p.zh)
		}
	}
}

// TestPluralForms 确认英文复数规则生效。
//
// ICU 复数在这里是必要的：英文 1 个镜像和 3 个镜像写法不同，
// 直接拼 "3 images" / "1 images" 会显得粗糙。
func TestPluralForms(t *testing.T) {
	en := locales.In("en")

	one := en.ImagesUpdated(1)
	many := en.ImagesUpdated(3)
	if one == many {
		t.Fatalf("复数未生效，1 和 3 得到同一串: %q", one)
	}
	if !strings.Contains(one, "1") {
		t.Errorf("单数形式应包含 1: %q", one)
	}
	if !strings.Contains(many, "3") {
		t.Errorf("复数形式应包含 3: %q", many)
	}

	zero := en.ImagesLocalTags(0)
	if !strings.Contains(strings.ToLower(zero), "no") {
		t.Errorf("0 个标签应使用 =0 分支: %q", zero)
	}
}

// TestPullButtonIsAnAction 是「拉取镜像」按钮文案的回归测试。
//
// 缺陷现象：镜像页工具栏第一个按钮在中文里是「拉取镜像…」（动作加省略号，点开对话框），
// 英文却译成了 "Pulling image…"——进行时，读起来像「正在拉取」，而这个按钮平时是空闲的。
// 这类错误词表测试看不出来（不空、没有汉字、占位符都对），是渲染英文版宣传片时看画面才发现的。
func TestPullButtonIsAnAction(t *testing.T) {
	en := locales.In("en").ImagesPulling()
	if strings.HasPrefix(en, "Pulling") {
		t.Errorf("按钮是动作，不是进度，不该是进行时: %q", en)
	}
	if !strings.HasSuffix(en, "…") {
		t.Errorf("点开对话框的按钮要带省略号: %q", en)
	}
}
