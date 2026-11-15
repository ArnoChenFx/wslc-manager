package main

// 本文件是剧情：给定一个时刻，演示实例该显示什么。
//
// 它是个纯函数——同一个时刻永远得到同一份场景，不读时钟、不记前一帧。
// 渲染视频要求视图对同一帧每次构建出同一个结果（视频会先把所有帧构建一遍来混音，
// 预览拖动时还会再构建），所以剧情只能是时间的函数，不能是「上一帧之后发生了什么」。

import (
	"time"

	"github.com/arno/wslc-manager/internal/app"
	"github.com/arno/wslc-manager/internal/selfupdate"
	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/internal/wslvm"
	"github.com/arno/wslc-manager/locales"
)

// 演示里固定不变的几个事实。
const (
	vmSession = "wslc-cli-demo"
	vmProcess = "vmmemwslc-cli-demo"
	vmPID     = 14872
)

// brandAccent 是品牌强调色：和图标的蓝一致，宣传片里界面也用它。
const brandAccent = "blue"

// accentOrder 是「个性化」一幕里强调色的轮换顺序，最后回到品牌蓝。
var accentOrder = []string{"blue", "magenta", "violet", "teal", "amber", "green", "blue"}

// fixedNow 是状态栏「更新于」和更新卡片「上次检查」显示的时间。写死一个：显示真实时间会让每次渲染的画面都不一样。
//
// 必须用本地时区（time.Local）：更新卡片会把时间转成本地时区再显示，状态栏则按时间自己的时区显示。
// 写成 UTC 的话，两处会差出时区的小时数（实测一个 09:41、一个 17:41），换一台机器渲染还会变。
var fixedNow = time.Date(2026, 11, 15, 9, 41, 7, 0, time.Local)

// otherLang 返回另一种语言，「个性化」一幕的末尾界面会切到它。
func otherLang(lang string) string {
	if lang == langEN {
		return langZH
	}
	return langEN
}

// flipsLanguage 报告这种语言的视频结尾是否把界面切到另一种语言。
//
// 中文版切到英文，展示「也有英文界面」；**英文版不切**——它要的就是一个从头到尾都是英文界面的版本，
// 结尾切成中文反而会让英文观众看到一屏看不懂的字。
// 声音也读它：不切换的版本不该有「切换语言」那一声提示音。
func flipsLanguage(videoLang string) bool { return videoLang == langZH }

// uiLangAt 返回 t 秒时界面该用的语言。
//
// 视频本身是哪种语言就从哪种语言开始；会切换的版本（见 flipsLanguage）到 cueLangFlip
// 整个界面换成另一种，标题块仍保持视频的语言——它们是「窗口外面的字」，不跟界面走。
func uiLangAt(t float64, videoLang string) string {
	if flipsLanguage(videoLang) && t >= cueLangFlip {
		return otherLang(videoLang)
	}
	return videoLang
}

// accentAt 返回 t 秒时的强调色。
func accentAt(t float64) string {
	if t < cueAccentFrom {
		return brandAccent
	}
	i := int((t-cueAccentFrom)/cueAccentStep) + 1
	return accentOrder[min(i, len(accentOrder)-1)]
}

// worldAt 算出 t 秒时的世界状态。它只取决于 t，与当前在哪一页无关：
// 比如 dev-mongo 启动之后，后面每一幕里它都是运行中。
func worldAt(t float64) world {
	w := world{
		mongoRunning: t >= cueStartMongo,
		pulled:       t >= cuePullDone,
	}
	if up := t - cueComposeUp; up >= composeDBUpAt {
		w.blogUp = 1
		if up >= composeGhostUpAt {
			w.blogUp = 2
		}
	}
	return w
}

// vmAt 返回 t 秒时虚拟机的观测快照与正在进行的动作。
//
// 一路走下来：运行（占着大约 1.4 GB）→ 正在关闭 → 已停止 → 正在启动 → 又运行起来
// （刚起来内存小，随后慢慢涨）。
func vmAt(t float64) (wslvm.Snapshot, app.DemoVMAction) {
	snap := wslvm.Snapshot{State: wslvm.Running, Session: vmSession, Process: vmProcess, PID: vmPID}
	const mb = 1 << 20
	switch {
	case t < cueVMStopping:
		snap.Memory = 1413 * mb
		return snap, app.DemoVMIdle
	case t < cueVMStopped:
		snap.Memory = 1413 * mb
		return snap, app.DemoVMStopping
	case t < cueVMStarting:
		return wslvm.Snapshot{State: wslvm.Stopped, Session: vmSession, Process: vmProcess}, app.DemoVMIdle
	case t < cueVMRunning:
		return wslvm.Snapshot{State: wslvm.Stopped, Session: vmSession, Process: vmProcess}, app.DemoVMStarting
	default:
		// 刚起来 380 MB，一秒多一点涨到 468 MB，之后不再变。
		grow := min(max((t-cueVMRunning)/1.2, 0), 1)
		snap.Memory = uint64(380+88*grow) * mb
		return snap, app.DemoVMIdle
	}
}

// baseScene 是不随剧情变化的那部分：数据、版本号、更新状态、会话。
func baseScene(w world) app.DemoScene {
	s := app.NewDemoScene()
	s.Containers, s.Images = containersOf(w), imagesOf(w)
	s.Volumes, s.Networks, s.Projects = volumesOf(), networksOf(), projectsOf()
	s.Sessions = []wslc.Session{{ID: "1", PID: "14872", DisplayName: vmSession}}
	s.Info = wslc.Info{
		WSLVersion: "2.6.1.0", KernelVersion: "6.6.87.2-1", WindowsVer: "10.0.26200.6899",
		Direct3D: "1.611.1-81528511", SessionMgr: "2.6.1.0", SessionCount: "1",
	}
	s.WSLCVersion = "wslc 3.0.5.0"
	s.ComposeVersion = "wslc-compose 1.0.0 (built-in)"
	s.UpdatedAt = fixedNow

	// 输出面板占的比例：应用的默认值。各幕需要时再改。
	s.OutputPercent = 58

	// 有新版本可用：侧边栏的「系统」上挂着「新」，系统页有更新卡片。
	// 版本号是演示用的，不对应任何真实发布。
	s.Update = selfupdate.Status{
		Version: "0.2.0", Supported: true, Available: "0.3.0",
		LastCheck: fixedNow, AutoCheck: true,
	}
	return s
}

// sceneAt 返回 t 秒时的演示场景。
//
// 它是（时刻，视频语言）的纯函数：面板标题等文字用 locales.In(这一刻界面的语言) 显式取词条，
// 不读全局语言。全局语言（应用自己的视图读它）由调用方在画这一帧之前按 uiLangAt 设好。
func sceneAt(t float64, videoLang string) app.DemoScene {
	m := locales.In(uiLangAt(t, videoLang))
	w := worldAt(t)
	s := baseScene(w)
	s.Accent = accentAt(t)
	s.VM, s.VMAction = vmAt(t)

	switch {
	case t < cuePageImage:
		// 第二幕：容器。选中 dev-mongo，启动它，再看日志。
		s.Page = app.PageContainers
		if t >= cueSelectMongo {
			s.ContainerRow = 0
		}
		if t >= cueOpenLogs {
			out := &app.DemoOutput{Title: m.CommonLogs() + " · " + mongoName}
			if t < cueLogsLoaded {
				// 真实界面读日志前有一瞬「加载中」。
				out.Text, out.Busy = m.StatusLoading(), true
			} else {
				out.Text = mongoLogs
			}
			s.Output = out
		}

	case t < cuePageCompose:
		// 第三幕：镜像。拉取开始面板立刻打开，进度逐行出现，完成后新镜像排在最前。
		s.Page = app.PageImages
		// 镜像有 9 行（拉完之后），要让表格整个放得下：放不下时列表会滚动，
		// 新拉下来插在最前面的那一行（也是选中的那一行）就被顶出视口，看不见了。
		s.OutputPercent = 70
		if t >= cuePullDone {
			s.ImageRow = 0
		}
		if t >= cuePullStart {
			title, text, busy := pullPanel(m, t-cuePullStart, t >= cuePullDone)
			s.Output = &app.DemoOutput{Title: title, Text: text, Busy: busy}
		}

	case t < cuePageSystem:
		// 第四幕：Compose。选中 blog，点启动，服务一个个起来。
		s.Page = app.PageCompose
		// 编排输出只有九行，但最后一行「完成（耗时 …）」要完整露出来：
		// 输出面板结束后不再跟随滚动，放不下的话最后一行会被面板底边切掉一半。
		s.OutputPercent = 62
		if t >= cueSelectBlog {
			s.ProjectRow = 0
		}
		if t >= cueComposeUp {
			done := t >= cueComposeDone
			title, text, busy := composeUpPanel(m, t-cueComposeUp, done)
			s.Output = &app.DemoOutput{Title: title, Text: text, Busy: busy}
			if !done {
				s.ComposeBusyDir = blogDir
			}
		}

	case t < cuePageBack:
		// 第五幕：虚拟机。
		s.Page = app.PageSystem

	default:
		// 第六幕：个性化。回到容器页，dev-mongo 仍是选中的。
		s.Page = app.PageContainers
		s.ContainerRow = 0
	}
	return s
}
