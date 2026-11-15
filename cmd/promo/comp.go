package main

// 本文件把剧情、画面和声音装配成一个 render.Composition。

import (
	"sync/atomic"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/render"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/app"
)

// uiLang 是此刻界面该用的语言。
//
// 应用的词条（locales.Xxx()）读的是全局的 i18n.Locale()，而它在正常运行时由 mygo.App 驱动。
// 渲染视频没有 mygo.App，所以这里接管取语言的入口：每帧开头由 View 写入，词条随之变化。
// 视频中途界面整体换一种语言（见 cueLangFlip）靠的就是它。
// 用原子指针而不是普通变量：词条函数可能在别的协程里被调用，测试开着 -race。
var uiLang atomic.Pointer[string]

// setUILang 设置此刻界面的语言。
func setUILang(lang string) { uiLang.Store(&lang) }

// installLocale 让应用词条读 uiLang。必须在渲染之前调用一次。
func installLocale() {
	i18n.SetLocaleFunc(func() string {
		if p := uiLang.Load(); p != nil {
			return *p
		}
		return langZH
	})
}

// act 是一幕的标题块出现的时间段。
type act struct {
	from, to float64
	h        func(tx copyText) headlineCopy
}

// acts 是五个有标题块的幕。片头和片尾是整屏的品牌卡片，不在这里。
var acts = []act{
	{cueContainers, cueImages, func(tx copyText) headlineCopy { return tx.Containers }},
	{cueImages, cueCompose, func(tx copyText) headlineCopy { return tx.Images }},
	{cueCompose, cueVM, func(tx copyText) headlineCopy { return tx.Compose }},
	{cueVM, cuePersonal, func(tx copyText) headlineCopy { return tx.VM }},
	{cuePersonal, cueWindowOut, func(tx copyText) headlineCopy { return tx.Personal }},
}

// headlineOverlap 是相邻两幕标题块在时间上重叠的帧数：
// 前一幕的标题要在这段时间里完成退场（见 headline 的末尾八帧）。
const headlineOverlap = 6

// windowPoseAt 返回窗口在这一帧的姿态：片头末尾升起，片尾前落下。
func windowPoseAt(f render.Frame) windowPose {
	in, _ := f.Shift(sec(cueWindowIn), 0)
	s := in.Spring(ui.Snappy.Spring)
	p := windowPose{
		dy:      float32(80 * (1 - s)),
		scale:   float32(0.955 + 0.045*s),
		opacity: float32(in.Interpolate([]float64{0, 10}, []float64{0, 1})),
	}

	// 落下：Interpolate 在范围外取端点值，所以落下之前 e 恒为 0，不用另外判断。
	out, _ := f.Shift(sec(cueWindowOut), 0)
	e := out.Interpolate([]float64{0, 16}, []float64{0, 1}, render.Interpolation{Easing: ui.EaseIn})
	p.dy += float32(50 * e)
	p.scale -= float32(0.04 * e)
	p.opacity *= float32(1 - e)
	return p
}

// masterVolume 是整段声音的总音量：开头淡入，结尾淡出。
//
// 淡出的终点不能是 0：render 把音量 0 当作「没设置」，按 1 播放，
// 淡出到最后一帧反而会炸出满音量。所以取一个听不见的极小值。
func masterVolume(f render.Frame) float64 {
	v := f.Interpolate(
		[]float64{0, float64(sec(0.6)), float64(totalFrames - sec(1.8)), float64(totalFrames - 1)},
		[]float64{0, 1, 1, 0})
	return max(v, 1e-4)
}

// newComposition 为一种语言装配整支宣传片。score 为 nil 时没有声音。
//
// 视图是剧情的纯函数（见 timeline.go）：同一帧每次构建结果相同。
// 演示实例（里面是应用的控件状态：列表、滚动位置）在第 0 帧重新创建，
// 这样无论从哪里开始渲染（整片、某一帧、预览里跳转），第 0 帧之后的状态都一样。
func newComposition(lang string, score *render.Sound) render.Composition {
	tx := copyFor(lang)
	var demo *app.Demo

	return render.Composition{
		Width: canvasW, Height: canvasH, FPS: fps, Frames: totalFrames, Scale: scale,
		Dark: true, Background: bgBottom, Locale: lang,
		View: func(c *ui.Context, f render.Frame) {
			if demo == nil || f.Index == 0 {
				demo = app.NewDemo(winW, bodyH)
			}
			// 先设这一帧界面的语言，再画：应用自己的视图读的是全局语言。
			// 场景本身不依赖它（词条是显式取的），所以顺序不影响场景的内容。
			t := f.Seconds()
			setUILang(uiLangAt(t, lang))
			scene := sceneAt(t, lang)

			if score != nil {
				f.Audio(score, render.AudioOptions{Volume: masterVolume(f)})
			}

			background(c)

			// 标题块：每一幕一个，在窗口下面（先画），互不遮挡。
			for _, a := range acts {
				from := sec(a.from)
				f.Sequence(c, from, sec(a.to)-from+headlineOverlap, func(g render.Frame) {
					headline(c, g, a.h(tx), tx.Join)
				})
			}

			// 窗口：片头末尾升起，到片尾前落下。里面是应用真实的界面。
			// 比可见的时间段提前、延后几帧存在（不透明度为 0），
			// 让表格宽度这类要靠上一帧布局结果的东西在露面之前就已经量好。
			winFrom, winTo := sec(cueWindowIn), sec(cueWindowOut)+20
			f.Sequence(c, winFrom, winTo-winFrom, func(render.Frame) {
				windowFrame(c, windowPoseAt(f), func() { demo.View(c, scene) })
			})

			f.Sequence(c, 0, sec(cueIntroOut)+20, func(g render.Frame) { intro(c, g, tx) })
			f.Sequence(c, sec(cueOutro), 0, func(g render.Frame) { outro(c, g, tx) })
		},
	}
}
