package main

// 本文件是宣传片里「我们自己写」的文案：每一幕的标题、副标题、标签，以及片头片尾。
//
// 窗口里的文字不在这里：那是应用真实的界面，随语言走应用自己的词条（locales）。
// 这里只有窗口外面的字。

// 语言标签：应用词条用的是这两个。
const (
	langZH = "zh-Hans"
	langEN = "en"
)

// headlineCopy 是一幕的标题块。
//
// 标题分成「前半 + 强调词 + 后半」三段，强调词用强调色。
// 不用一整段再去找词：中英文断句位置不同，分开写最直接。
type headlineCopy struct {
	Pre, Key, Post string
	// Sub 是标题下面的一句话说明。
	Sub string
	// Chips 是右侧依次弹出的几个小标签，每个标签一个卖点。
	Chips []string
}

// copyText 是一种语言的全部文案。
type copyText struct {
	// Lang 是语言标签。
	Lang string
	// Join 是标题里「前半」与「强调词」之间的间距（DIP）。
	// 中文不需要，英文要留出一个空格的宽度。
	Join float32

	Containers, Images, Compose, VM, Personal headlineCopy

	// 片尾。
	Tagline  string
	Facts    []string
	URL      string
	Platform string
}

// repoURL 是项目主页。来自 README 与 mygo.json 里的仓库地址，是公开信息。
const repoURL = "github.com/ArnoChenFx/wslc-manager"

// copyFor 返回某种语言的文案。
func copyFor(lang string) copyText {
	if lang == langEN {
		return copyEN
	}
	return copyZH
}

var copyZH = copyText{
	Lang: langZH,
	Join: 0,
	Containers: headlineCopy{
		Pre: "容器，", Key: "一目了然",
		Sub:   "启动、停止、重启；状态、日志、资源占用，一个窗口里全看见",
		Chips: []string{"实时状态", "日志", "资源统计"},
	},
	Images: headlineCopy{
		Pre: "拉取镜像，", Key: "进度实时可见",
		Sub:   "每一层的下载进度都在流动，再也不用对着空白发呆",
		Chips: []string{"流式进度", "一键更新全部", "标签与详情"},
	},
	Compose: headlineCopy{
		Pre: "Compose，", Key: "内置引擎",
		Sub:   "不用另装任何工具：依赖顺序、健康检查、预览，一应俱全",
		Chips: []string{"compose-go 解析", "depends_on", "健康检查", "预览 dry-run"},
	},
	VM: headlineCopy{
		Pre: "虚拟机，", Key: "由你掌控",
		Sub:   "看它占了多少内存，想启停就启停——后台刷新绝不会把它悄悄叫醒",
		Chips: []string{"内存可见", "一键启停", "签名自动更新"},
	},
	Personal: headlineCopy{
		Pre: "你的语言，", Key: "你的配色",
		Sub:   "中文与英文随时切换，六种强调色",
		Chips: []string{"中文 / English", "6 种强调色"},
	},
	Tagline:  "在 Windows 上管理 WSL 容器",
	Facts:    []string{"单个可执行文件", "纯 Go 原生界面", "无需 Electron"},
	URL:      repoURL,
	Platform: "Windows 10 / 11  ·  MIT",
}

var copyEN = copyText{
	Lang: langEN,
	Join: 9,
	Containers: headlineCopy{
		Pre: "Every container,", Key: "at a glance",
		Sub:   "Start, stop, restart. Status, logs and stats — all in one window.",
		Chips: []string{"Live status", "Logs", "Stats"},
	},
	Images: headlineCopy{
		Pre: "Pull images,", Key: "watch the progress",
		Sub:   "Every layer streams live. No more staring at a blank screen.",
		Chips: []string{"Live progress", "Update all", "Tags & details"},
	},
	Compose: headlineCopy{
		Pre: "Compose,", Key: "built in",
		Sub:   "Nothing else to install: dependencies, health checks and dry-run included.",
		Chips: []string{"compose-go", "depends_on", "Health checks", "Dry-run"},
	},
	VM: headlineCopy{
		Pre: "Your VM,", Key: "under your control",
		Sub:   "See its memory, start or stop it. A background refresh never wakes it.",
		Chips: []string{"Memory", "Start / stop", "Signed updates"},
	},
	// 英文版的界面从头到尾都是英文，不会在结尾切成中文（见 flipsLanguage），
	// 所以这一幕只演示强调色，双语只用文字说明。标签里也不写「中文」二字：英文版不该出现汉字。
	Personal: headlineCopy{
		Pre: "Make it", Key: "yours",
		Sub:   "Six accent colors, and an interface in English or Chinese — your choice.",
		Chips: []string{"6 accent colors", "English & Chinese"},
	},
	Tagline:  "Manage WSL containers on Windows",
	Facts:    []string{"One executable", "Native Go UI", "No Electron"},
	URL:      repoURL,
	Platform: "Windows 10 / 11  ·  MIT",
}
