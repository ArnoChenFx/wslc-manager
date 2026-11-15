package main

// 本文件是宣传片里「流出来」的文字：日志、拉取进度、Compose 的执行输出。
//
// 内容是虚构的，但形状照着真实的来：
//   - 拉取进度是 docker 风格的「层号: 状态」行（应用的测试夹具里就是这个样子）；
//   - Compose 的行是内置引擎 e.info 真正会打的那些（Creating … / Starting … /
//     Network … created）；
//   - 面板标题、开头一行、结尾一行直接用应用的词条，和真实界面逐字一致。
//
// 每一行带一个「出现的时间」（相对动作开始的秒数），画面按当前时间显示已经出现的那些行，
// 于是同一帧永远得到同一段文字——渲染成视频的前提。

import (
	"strings"

	"github.com/arno/wslc-manager/locales"
)

// streamLine 是流里的一行：at 秒之后出现。
type streamLine struct {
	at   float64
	text string
}

// visibleLines 返回 elapsed 秒时已经出现的行，按顺序拼成一段文字。
func visibleLines(lines []streamLine, elapsed float64) (text string, n int) {
	var b []string
	for _, l := range lines {
		if l.at > elapsed {
			break
		}
		b = append(b, l.text)
	}
	return strings.Join(b, "\n"), len(b)
}

// pullLines 是拉取 python:3.13-slim 的进度（相对 cuePullStart）。
//
// 四层并行下载、先到先解压，最后是摘要和结果——和真实 docker 风格的输出节奏一致。
var pullLines = []streamLine{
	{0.15, "3.13-slim: Pulling from library/python"},
	{0.45, "a3f1c0d8e742: Pulling fs layer"},
	{0.55, "6be2d09f1a35: Pulling fs layer"},
	{0.65, "d8c5e7a2b014: Pulling fs layer"},
	{0.75, "f19a4b60c3e8: Pulling fs layer"},
	{1.00, "6be2d09f1a35: Downloading"},
	{1.05, "a3f1c0d8e742: Downloading"},
	{1.25, "d8c5e7a2b014: Downloading"},
	{1.45, "6be2d09f1a35: Download complete"},
	{1.70, "f19a4b60c3e8: Downloading"},
	{1.95, "a3f1c0d8e742: Download complete"},
	{2.15, "d8c5e7a2b014: Download complete"},
	{2.35, "f19a4b60c3e8: Download complete"},
	{2.55, "a3f1c0d8e742: Extracting"},
	{2.75, "6be2d09f1a35: Extracting"},
	{2.95, "d8c5e7a2b014: Pull complete"},
	{3.10, "f19a4b60c3e8: Pull complete"},
	{3.22, "Digest: sha256:7d1b30e4a8c59f26e1d04a93b7c25f8806ad3e71b94c0f2a5d68e13c7b09a4f1"},
	{3.32, "Status: Downloaded newer image for python:3.13-slim"},
}

// pullPanel 返回拉取镜像的输出面板内容。elapsed 是开始拉取之后的秒数；done 为真表示已结束。
//
// 词条显式从 m 取（调用方给的语言），而不是读全局语言：这样剧情是（时刻，语言）的纯函数，
// 不依赖「这一帧的语言设了没有」这种全局状态。
func pullPanel(m locales.Messages, elapsed float64, done bool) (title, text string, busy bool) {
	title = m.ImagesPullPanel(pulledRef)
	body, n := visibleLines(pullLines, elapsed)
	if n == 0 {
		// 第一行进度还没到：真实界面这时显示「开始拉取 … 」。
		return title, m.ImagesStartedPull(pulledRef) + " …\n", true
	}
	if done {
		return title, body + m.ImagesDoneBlock(), false
	}
	return title, body, true
}

// composeUpLines 是启动 blog 项目时内置引擎打出的行（相对 cueComposeUp）。
var composeUpLines = []streamLine{
	{0.20, "Network blog_default created"},
	{0.45, "Volume blog_dbdata created"},
	{0.60, "Volume blog_content created"},
	{0.95, "Creating blog-db-1 ..."},
	{1.20, "Starting blog-db-1 ..."},
	{1.75, "Creating blog-ghost-1 ..."},
	{2.25, "Starting blog-ghost-1 ..."},
}

// 两个服务「起来了」的时刻（相对 cueComposeUp）：紧跟在对应的 Starting 行后面，
// 容器表里的状态就在这一刻翻转，不会比输出行早。它们是 grid 的整数倍（4 格、7 格），
// 好让「起来了」的提示音落在拍上。
const (
	composeDBUpAt    = 4 * grid
	composeGhostUpAt = 7 * grid
)

// composeDuration 是结尾一行里显示的耗时。取一个和动画时长吻合的值。
const composeDuration = "3.412s"

// composeUpPanel 返回启动 blog 项目的输出面板内容。词条从 m 取，见 pullPanel。
func composeUpPanel(m locales.Messages, elapsed float64, done bool) (title, text string, busy bool) {
	title = m.ComposeStart() + " · blog"
	body, n := visibleLines(composeUpLines, elapsed)
	if n == 0 {
		return title, m.ComposeRunningAction("up") + " …\n", true
	}
	if done {
		return title, body + m.ComposeFinishWithDuration(composeDuration), false
	}
	return title, body, true
}

// mongoLogs 是 dev-mongo 的日志。真实界面是一次读出来的（loadLogs），所以这里也整段给，
// 不做逐行冒出的效果——只有拉取和编排才是流式的。
const mongoLogs = `2026-11-15T09:41:03.118Z  I  CONTROL   Starting mongod  version=8.0.4
2026-11-15T09:41:03.121Z  I  STORAGE   Opening WiredTiger  path=/data/db
2026-11-15T09:41:03.402Z  I  STORAGE   Recovering from clean shutdown
2026-11-15T09:41:03.688Z  I  REPL      Replication disabled  standalone=true
2026-11-15T09:41:03.901Z  I  INDEX     Index build complete  count=0
2026-11-15T09:41:04.012Z  I  NETWORK   Listening on 0.0.0.0:27017
2026-11-15T09:41:04.013Z  I  NETWORK   Waiting for connections  port=27017`
