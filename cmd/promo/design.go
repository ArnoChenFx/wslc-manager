package main

// 本文件是宣传片的视觉元素：背景、品牌图标、窗口外壳、标题块、片头与片尾。
//
// 设计原则：
//   - 画面里真正的主角是应用自己的界面（demo.View），窗口外面的东西越克制越好：
//     深色中性底、白色标题、一个强调色。不铺光晕、不加渐变文字——那是「AI 味」的典型标志；
//   - 强调色就是产品图标的蓝，窗口里的界面也用同一个蓝（见 brandAccent），
//     于是窗口内外是同一个色调；
//   - 所有尺寸以 DIP 计，合成按 scale 放大成像素。

import (
	"github.com/egoist/mygo/plugins/render"
	"github.com/egoist/mygo/ui"
)

// 画布与窗口的几何（DIP）。画布是 1920×1080 像素，除以 scale 就是约 1324×745 DIP。
//
// 窗口宽 1180 是应用的默认窗口宽度（见 main.go 的 WindowOptions）：宣传片里的界面就是用户
// 开箱看到的样子，不为了凑版面去拖分隔条、收窄侧边栏——那样英文版的「Compose Projects」会被截断，
// 项目列表的按钮会被挤出边（试过，所以不这么做）。
const (
	canvasW = 1920
	canvasH = 1080
	scale   = 1.45

	// 窗口：标题栏 + 内容区。内容区的尺寸就是演示实例看到的「窗口大小」。
	winX  float32 = 72
	winY  float32 = 100
	winW  float32 = 1180
	barH  float32 = 30
	bodyH float32 = 590
)

// 调色板。深色中性底；文字用偏冷的白，次要文字降一档；强调词用比界面里更亮的蓝，
// 因为大号文字压在近黑底上，需要比按钮底色更高的明度才够醒目。
var (
	bgTop      = ui.Hex("#131316")
	bgBottom   = ui.Hex("#08080a")
	ink        = ui.Hex("#f3f3f6")
	inkMuted   = ui.Hex("#9a9aa4")
	accentText = ui.Hex("#6cb0ff")
	chipText   = ui.Hex("#c9c9d2")
	chipBorder = ui.RGBA(255, 255, 255, 0.15)
	winRing    = ui.RGBA(255, 255, 255, 0.12)
	winShadow  = ui.RGBA(0, 0, 0, 0.62)
	barBg      = ui.Hex("#202020")
	barInk     = ui.Hex("#e8e8e8")
)

// clamp01 把 x 夹在 0 到 1 之间。弹簧会过冲到 1 以上，不透明度之类不能跟着超。
func clamp01(x float64) float32 { return float32(min(max(x, 0), 1)) }

// background 铺满画布的底：极淡的上亮下暗，只为避免一整块死黑。
func background(c *ui.Context) {
	render.Layer(c).LinearGradient(ui.LinearGradient{From: bgTop, To: bgBottom, Angle: 180})
}

// ---- 品牌图标 ----

// markPose 是图标各部分的出场进度（0 未出现，1 完成），用来做「一层层叠起来」。
type markPose struct{ tile, bottom, middle, top float64 }

// markDone 是完整的图标。
var markDone = markPose{1, 1, 1, 1}

// brandMark 画产品图标：深色圆角底 + 三张叠起来的卡片。
//
// 几何和配色逐项照 resources/icon.svg（1024×1024 的画布）折算：
// 用盒子而不是贴图片，是因为每张卡片要能单独动。
func brandMark(c *ui.Context, size float32, p markPose) {
	k := size / 1024
	ring := max(size*8/1024, 0.75)

	ui.Box(c).Width(size).Height(size).Radius(228 * k).Clip().
		LinearGradient(ui.LinearGradient{From: ui.Hex("#171e2c"), To: ui.Hex("#0a0f18"), Angle: 180}).
		Opacity(clamp01(p.tile)).Children(func() {
		// 每张卡片从下方 60 个单位处升到位，同时淡入。
		card := func(x, y, w, h float32, from, to ui.Color, prog float64) ui.Element {
			rise := float32(1-min(prog, 1.15)) * 60 * k
			return ui.Box(c).Absolute().Left(x * k).Top(y*k + rise).Width(w * k).Height(h * k).Radius(72 * k).
				LinearGradient(ui.LinearGradient{From: from, To: to, Angle: 180}).
				Opacity(clamp01(prog * 1.6))
		}
		// 下层略宽，小尺寸下侧面也看得出「叠」。
		card(172, 516, 680, 264, ui.Hex("#163a7a"), ui.Hex("#0c2760"), p.bottom)
		card(192, 386, 640, 264, ui.Hex("#2f6ae0"), ui.Hex("#1a4bb8"), p.middle)
		card(212, 256, 600, 264, ui.Hex("#8ad0ff"), ui.Hex("#3b8eff"), p.top).Clip().Children(func() {
			// 顶卡左上角一层很淡的高光，补一点体积。
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				LinearGradient(ui.LinearGradient{From: ui.RGBA(255, 255, 255, 0.34), To: ui.RGBA(255, 255, 255, 0), Angle: 150})
		})
		// 顶沿一条更淡的亮边，让卡片有一点厚度。
		ui.Box(c).Absolute().Left(284 * k).Top(266 * k).Width(456 * k).Height(max(8*k, 1)).Radius(4 * k).
			Background(ui.RGBA(255, 255, 255, 0.28)).Opacity(clamp01(p.top))
		// 底的描边：很淡的一圈。
		//
		// 必须放在底里面。Absolute 是相对「父元素」定位的：早先把它放在底的外面，
		// 父元素就成了整屏，描边被画到了画面左上角（片头里多出一个圆角方框，
		// 标题栏里也有个小残影）。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(228*k).
			Border(ring, ui.RGBA(255, 255, 255, 0.10))
	})
}

// ---- 窗口外壳 ----

// windowPose 是窗口的姿态：升起、落下时用。
type windowPose struct {
	dy      float32 // 垂直偏移（DIP），向下为正
	scale   float32
	opacity float32
}

// windowFrame 画窗口外壳（Windows 11 深色标题栏 + 圆角 + 阴影），内容区交给 body。
//
// 标题栏是仿的：真实应用用系统自己的标题栏，截图里自然带着；这里在渲染里没有系统窗口，
// 只能画一条，样子照 Windows 11 的深色标题栏，让它一眼是个「真窗口」。
func windowFrame(c *ui.Context, p windowPose, body func()) {
	ui.Column(c).Absolute().Left(winX).Top(winY).Width(winW).Height(barH+bodyH).
		Radius(10).Clip().Background(barBg).Shadow(0, 30, 80, 0, winShadow).
		Translate(0, p.dy).Scale(p.scale, p.scale).Opacity(p.opacity).Children(func() {
		titleBar(c)
		// 内容区必须有确定的高度：应用的根视图是 Fill 的，
		// 没有确定高度就会按整个窗口去算，把标题栏也算进去而溢出。
		ui.Column(c).Width(winW).Height(bodyH).Children(body)
		// 窗口外沿一圈极淡的描边，深色底上用它把窗口勾出来。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(10).Border(1, winRing)
	})
}

// titleBar 是窗口顶部的标题栏。
func titleBar(c *ui.Context) {
	ui.Row(c).Width(winW).Height(barH).Background(barBg).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(12).Height(1)
		brandMark(c, 15, markDone)
		ui.Text(c, "WSLC Manager").FontSize(12).TextColor(barInk).Margin(0, 0, 0, 9)
		ui.Spacer(c)
		// 三个窗口按钮：最小化、最大化、关闭，各 46 宽，和 Windows 11 一致。
		captionButton(c, func() { ui.Box(c).Width(10).Height(1).Background(barInk) })
		captionButton(c, func() { ui.Box(c).Width(10).Height(10).Radius(1.5).Border(1, barInk) })
		captionButton(c, func() {
			// 叉号：两条细线交叉。
			ui.Box(c).Width(12).Height(12).Children(func() {
				ui.Box(c).Absolute().Left(-1.5).Top(5.5).Width(15).Height(1).Background(barInk).Rotate(45)
				ui.Box(c).Absolute().Left(-1.5).Top(5.5).Width(15).Height(1).Background(barInk).Rotate(-45)
			})
		})
	})
}

// captionButton 是标题栏右侧的一个按钮：46×30，图形居中。
func captionButton(c *ui.Context, glyph func()) {
	ui.Column(c).Width(46).Height(barH).Center().Children(glyph)
}

// ---- 标题块 ----

// chip 是一个小标签：圆角描边的胶囊，一个卖点一个。
func chip(c *ui.Context, text string, prog float64) {
	ui.Text(c, text).FontSize(12).TextColor(chipText).Padding(5, 11).Radius(999).Border(1, chipBorder).
		Opacity(clamp01(prog*1.6)).Translate(0, float32(10*(1-min(prog, 1))))
}

// headline 画一幕的标题块：左边标题加一句话，右边几个标签依次弹出。
//
// f 是以这一幕开头为 0 的帧（Frame.Sequence 给的），Count 是这一幕标题的总帧数：
// 最后八帧留给退场，这样下一幕的标题出现时，这一幕的已经淡出了，不会两行字叠在一起。
func headline(c *ui.Context, f render.Frame, h headlineCopy, join float32) {
	in := f.SpringIn(ui.Snappy.Spring, 16)
	exit := float64(f.Count - 14)
	out := f.Interpolate([]float64{exit, exit + 8}, []float64{1, 0})
	sub, _ := f.Shift(5, 0)
	subIn := sub.SpringIn(ui.Smooth.Spring, 16)

	ui.Column(c).Absolute().Left(winX).Top(16).Gap(7).Opacity(float32(out)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(join).
			Opacity(clamp01(in*1.5)).Translate(0, float32(20*(1-min(in, 1)))).Children(func() {
			ui.Text(c, h.Pre).FontSize(36).Bold().TextColor(ink).LetterSpacing(-0.3)
			ui.Text(c, h.Key).FontSize(36).Bold().TextColor(accentText).LetterSpacing(-0.3)
			if h.Post != "" {
				ui.Text(c, h.Post).FontSize(36).Bold().TextColor(ink).LetterSpacing(-0.3)
			}
		})
		ui.Text(c, h.Sub).FontSize(16).TextColor(inkMuted).
			Opacity(clamp01(subIn*1.5)).Translate(0, float32(12*(1-min(subIn, 1))))
	})

	ui.Row(c).Absolute().Right(winX).Top(30).Gap(8).Opacity(float32(out)).Children(func() {
		for i, text := range h.Chips {
			g, _ := f.Shift(10+4*i, 0)
			chip(c, text, g.Spring(ui.Snappy.Spring))
		}
	})
}

// ---- 片头与片尾 ----

// intro 是片头：图标一层层叠起来，名字和一句话先后出现，然后整体上移淡出，让位给窗口。
func intro(c *ui.Context, f render.Frame, tx copyText) {
	pose := markPose{tile: float64(f.Interpolate([]float64{0, 8}, []float64{0, 1}))}
	for i, cue := range []float64{cueMarkBottom, cueMarkMiddle, cueMarkTop} {
		g, _ := f.Shift(sec(cue), 0)
		prog := g.Spring(ui.Bouncy.Spring)
		switch i {
		case 0:
			pose.bottom = prog
		case 1:
			pose.middle = prog
		default:
			pose.top = prog
		}
	}
	word, _ := f.Shift(sec(cueWordmark), 0)
	tag, _ := f.Shift(sec(cueTagline), 0)
	wIn, tIn := word.SpringIn(ui.Snappy.Spring, 18), tag.SpringIn(ui.Smooth.Spring, 18)

	out0 := float64(sec(cueIntroOut))
	out := f.Interpolate([]float64{out0, out0 + 14}, []float64{1, 0})
	lift := float32(f.Interpolate([]float64{out0, out0 + 14}, []float64{0, -24}, render.Interpolation{Easing: ui.EaseIn}))

	ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(20).
		Opacity(float32(out)).Translate(0, lift).Children(func() {
		brandMark(c, 150, pose)
		ui.Text(c, "WSLC Manager").FontSize(46).Bold().TextColor(ink).LetterSpacing(-0.8).
			Opacity(clamp01(wIn*1.5)).Translate(0, float32(18*(1-min(wIn, 1))))
		ui.Text(c, tx.Tagline).FontSize(18).TextColor(inkMuted).
			Opacity(clamp01(tIn*1.5)).Translate(0, float32(12*(1-min(tIn, 1))))
	})
}

// outro 是片尾：品牌名、一句话、三个事实、仓库地址。一直停到结尾。
func outro(c *ui.Context, f render.Frame, tx copyText) {
	// 各元素按顺序依次进来，错开几帧。
	step := func(delay int) float64 {
		g, _ := f.Shift(delay, 0)
		return g.SpringIn(ui.Snappy.Spring, 18)
	}
	mark, word, tag, facts, url := step(0), step(5), step(10), step(16), step(24)
	rise := func(p float64, dist float32) (float32, float32) { return clamp01(p * 1.5), dist * float32(1-min(p, 1)) }

	ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(16).Children(func() {
		o, dy := rise(mark, 18)
		ui.Box(c).Opacity(o).Translate(0, dy).Children(func() { brandMark(c, 96, markDone) })

		o, dy = rise(word, 18)
		ui.Text(c, "WSLC Manager").FontSize(54).Bold().TextColor(ink).LetterSpacing(-1).Opacity(o).Translate(0, dy)

		o, dy = rise(tag, 14)
		ui.Text(c, tx.Tagline).FontSize(20).TextColor(inkMuted).Opacity(o).Translate(0, dy)

		o, dy = rise(facts, 14)
		ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Opacity(o).Translate(0, dy).Children(func() {
			for _, fact := range tx.Facts {
				ui.Text(c, fact).FontSize(14).TextColor(chipText).Padding(7, 14).Radius(999).Border(1, chipBorder)
			}
		})

		o, dy = rise(url, 12)
		ui.Column(c).AlignItems(ui.Center).Gap(6).Margin(14, 0, 0, 0).Opacity(o).Translate(0, dy).Children(func() {
			ui.Text(c, tx.URL).FontSize(17).Bold().TextColor(accentText)
			ui.Text(c, tx.Platform).FontSize(12).TextColor(inkMuted)
		})
	})
}
