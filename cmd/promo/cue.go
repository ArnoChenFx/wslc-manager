package main

// 本文件是宣传片的剧情时间表（单位：秒）。
//
// 为什么集中放在一处：画面（timeline.go）和声音（audio.go）都要知道
// 「几秒的时候发生什么」。各写各的，改一处就会对不上——点击音落在状态翻转之前，
// 听上去像卡顿。两边都读这里的常量，就不可能错位。
//
// 为什么都是 grid 的整数倍：背景音乐是 85.7 BPM（一拍 0.7 秒），grid 是半拍，也就是八分音符。
// 所有动作都落在八分音符的格子上，动作音和音乐就永远是合拍的，不会有「差一点点」的别扭感。
// 代价是时刻不能随手取整，改的时候数格子，不要直接改秒数。
//
// 剧情共六幕，窗口从片头末尾升起，到片尾前落下，期间一直是同一个窗口、同一个应用实例：
// 页面在里面切换，上方的标题块随幕更换。

const (
	// fps 是帧率。30 帧在 1080p 的界面录像上足够顺滑，编码也最稳。
	fps = 30

	// grid 是半拍的秒数。
	grid = 0.35
	// beat 是一拍；bar 是一小节（四拍），音乐每小节换一个和弦。
	beat = 2 * grid
	bar  = 4 * beat

	// totalSec 是全片时长，恰好 13 小节：和弦走完三个整循环后，在最后一小节回到主和弦收尾。
	totalSec = 13 * bar
	// totalFrames 是全片帧数。
	//
	// 常量转 int 要求结果恰好是整数：13 小节 × 2.8 秒 × 30 帧 = 1092。
	// 以后改了 grid 或 fps 让它不再是整数，这里会编译失败，而不是悄悄少渲染半帧。
	totalFrames = int(totalSec * fps)
)

// 第一幕：片头。图标一层层叠起来（三下上行的拨弦），名字和一句话先后出现，然后窗口升起。
const (
	cueMarkBottom = 1 * grid // 图标最下面一张卡片出现
	cueMarkMiddle = 2 * grid
	cueMarkTop    = 3 * grid
	cueWordmark   = 4 * grid // 「WSLC Manager」
	cueTagline    = 6 * grid // 一句话介绍
	cueIntroOut   = 7 * grid // 片头元素开始退场
	cueWindowIn   = 8 * grid // 窗口开始升起，正好是第二小节的第一拍
)

// 第二幕：容器。选中一个停着的容器，启动它，看它的日志。
const (
	cueContainers  = 10 * grid // 标题出现
	cueSelectMongo = 13 * grid // 选中 dev-mongo
	cueStartMongo  = 17 * grid // 点「启动」，状态翻成运行中
	cueOpenLogs    = 21 * grid // 打开日志面板
	cueLogsLoaded  = 22 * grid // 日志读出来（先有一瞬「加载中」，和真实界面一样）
)

// 第三幕：镜像。拉取镜像，每一层的进度实时冒出来。
const (
	cueImages    = 29 * grid
	cuePageImage = 30 * grid // 切到镜像页
	cuePullStart = 33 * grid // 开始拉取，面板立刻打开
	cuePullDone  = 43 * grid // 拉取完成，新镜像出现在列表最前面
)

// 第四幕：Compose。启动 blog 项目，服务按依赖顺序一个个起来。
const (
	cueCompose     = 46 * grid
	cuePageCompose = 47 * grid
	cueSelectBlog  = 50 * grid // 选中 blog 项目
	cueComposeUp   = 54 * grid // 点「启动」
	cueComposeDone = 64 * grid // 全部完成
)

// 第五幕：虚拟机。看内存，关掉，再启动。
const (
	cueVM         = 67 * grid
	cuePageSystem = 68 * grid
	cueVMStopping = 73 * grid // 点「关闭」，转圈
	cueVMStopped  = 76 * grid // 已停止，内存变成「—」
	cueVMStarting = 80 * grid // 点「启动」，转圈
	cueVMRunning  = 83 * grid // 又在运行了
)

// 第六幕：个性化。强调色一种接一种地换，然后整个界面换一种语言。
const (
	cuePersonal   = 85 * grid
	cuePageBack   = 86 * grid // 回到容器页
	cueAccentFrom = 87 * grid // 开始换强调色
	cueAccentStep = grid      // 每种颜色停留的时间：一个八分音符，音上是一级级上行的拨弦
	cueLangFlip   = 94 * grid // 界面语言切换
)

// 片尾：窗口落下，品牌卡片出现。
const (
	cueWindowOut = 96 * grid
	cueOutro     = 97 * grid
)

// README 里循环播放的预览动图截取成片的哪一段。
//
// 起点取在窗口升起、标题块和它右边的小标签都弹出到位之后：第一帧必须是个完整的画面，
// 因为有些地方只显示动图的第一帧（链接预览、不播放动图的阅读器）。
// 片头里窗口还没升起的那几帧是一片空黑；标题块刚出现的头一秒，小标签还在逐个淡入，是半透明的影子。
// 终点取在窗口落下之前，于是动图从「窗口里是容器页」回到「窗口里是容器页」，循环起来不突兀。
const (
	previewFrom = cueContainers + 3*grid
	previewTo   = cueWindowOut - 0.4
)

// sec 把秒换算成帧序号（四舍五入）。
func sec(t float64) int { return int(t*fps + 0.5) }
