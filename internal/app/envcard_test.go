package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 环境卡片上不应有「重新检测」按钮。
//
// 卡片里只有 wslc 和内置的 Compose 引擎两行：装 / 更新 WSL 走 finishEnv，结束后会自动重检，
// 没有任何事需要用户专门点一下按钮来刷新。再放一个这样的按钮，只会让人以为内置引擎也要「检测」。
// 这个测试防的就是它被加回来。
//
// 需要手动重来的两个入口在别处，文案也不同，这里一并守住：
//   - 系统页维护区的「重新检测环境」（本测试里断言）；
//   - wslc 缺失时的错误页（见 TestErrorPaneStillOffersRecheck）。
//
// 匹配要用整串精确比较：「重新检测环境」包含「重新检测」，按子串找会把维护区的按钮误当成卡片上的。
// 维护区的按钮正好充当对照：它存在，而整串等于「重新检测」的一个都没有，说明比较是分得清的，
// 不是因为什么都没找到才通过。
func TestEnvCardHasNoRecheckButton(t *testing.T) {
	// 窗口取得足够高，让整页都在视口里。
	//
	// Scroll 只构建视口内的元素：系统页上面再多一张卡片，就可能把环境卡片挤出视口，
	// 那时「找不到按钮」就成了空洞的通过。整页都放得下，结果就不依赖卡片的数量和各自的高度。
	const W, H = 1180, 2400

	exact := func(tt *ui.Tester, label string) int {
		n := 0
		for _, s := range tt.Texts() {
			if s == label {
				n++
			}
		}
		return n
	}

	for name, setup := range map[string]func(*App){
		"依赖齐全": func(a *App) {},
		"缺少 wslc": func(a *App) {
			a.env.WSLCPath = ""
			a.env.WSLVersion = ""
		},
		"缺少 wslc 且没有 winget": func(a *App) {
			a.env.WSLCPath = ""
			a.env.WSLVersion = ""
			a.env.Winget = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			sampleData(a)
			a.page = PageSystem
			setup(a)

			tt := ui.NewTester(a.View, W, H)
			for i := 0; i < 3; i++ {
				tt.Frame()
			}

			// 卡片本身确实渲染出来了（否则下面的「没有按钮」毫无意义）。
			if !tt.HasText("依赖环境") {
				t.Fatalf("系统页上应有「依赖环境」卡片, texts=%q", tt.Texts())
			}
			if !tt.HasText("wslc-compose") {
				t.Errorf("卡片里应仍显示 wslc-compose 这一行, texts=%q", tt.Texts())
			}
			// 整串等于「重新检测」的按钮不应出现：它在系统页上唯一的来源就是环境卡片。
			if n := exact(tt, "重新检测"); n != 0 {
				t.Errorf("环境卡片上不应有「重新检测」按钮，找到 %d 个", n)
			}
			// 对照：维护区的「重新检测环境」仍在，且只有一个。
			if n := exact(tt, "重新检测环境"); n != 1 {
				t.Errorf("系统页维护区应有一个「重新检测环境」按钮，找到 %d 个", n)
			}
		})
	}
}

// wslc 缺失时的错误页仍然提供「重新检测」：用户在应用外装好 WSL 回来，需要一个入口让应用重新探测。
func TestErrorPaneStillOffersRecheck(t *testing.T) {
	a := newTestApp(t)
	a.page = PageContainers
	a.wslc = nil
	a.wslcErr = "找不到 wslc"

	tt := frame(t, a)
	found := false
	for _, s := range tt.Texts() {
		if s == "重新检测" {
			found = true
		}
	}
	if !found {
		t.Errorf("wslc 缺失的错误页应有「重新检测」按钮, texts=%q", tt.Texts())
	}
}
