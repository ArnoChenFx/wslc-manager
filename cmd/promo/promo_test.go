package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/render"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/app"
	"github.com/arno/wslc-manager/internal/wslvm"
)

// han 匹配汉字。英文版里不该出现它。
var han = regexp.MustCompile(`\p{Han}`)

// allFrames 逐帧返回每一帧的时刻（秒）。
func allFrames() []float64 {
	ts := make([]float64, totalFrames)
	for f := range ts {
		ts[f] = float64(f) / fps
	}
	return ts
}

// ---- 时间表 ----

// TestCuesAreOnTheGridAndInOrder 验证剧情时间表的两条约定：
// 每个时刻都是 grid 的整数倍（动作音才能和音乐合拍），并且按故事顺序严格递增、都在片长之内。
func TestCuesAreOnTheGridAndInOrder(t *testing.T) {
	// 按故事顺序列出所有时刻。新增时刻时加在对应位置。
	cues := []struct {
		name string
		at   float64
	}{
		{"cueMarkBottom", cueMarkBottom}, {"cueMarkMiddle", cueMarkMiddle}, {"cueMarkTop", cueMarkTop},
		{"cueWordmark", cueWordmark}, {"cueTagline", cueTagline}, {"cueIntroOut", cueIntroOut},
		{"cueWindowIn", cueWindowIn},
		{"cueContainers", cueContainers}, {"cueSelectMongo", cueSelectMongo}, {"cueStartMongo", cueStartMongo},
		{"cueOpenLogs", cueOpenLogs}, {"cueLogsLoaded", cueLogsLoaded},
		{"cueImages", cueImages}, {"cuePageImage", cuePageImage}, {"cuePullStart", cuePullStart}, {"cuePullDone", cuePullDone},
		{"cueCompose", cueCompose}, {"cuePageCompose", cuePageCompose}, {"cueSelectBlog", cueSelectBlog},
		{"cueComposeUp", cueComposeUp}, {"cueComposeDone", cueComposeDone},
		{"cueVM", cueVM}, {"cuePageSystem", cuePageSystem}, {"cueVMStopping", cueVMStopping},
		{"cueVMStopped", cueVMStopped}, {"cueVMStarting", cueVMStarting}, {"cueVMRunning", cueVMRunning},
		{"cuePersonal", cuePersonal}, {"cuePageBack", cuePageBack}, {"cueAccentFrom", cueAccentFrom},
		{"cueLangFlip", cueLangFlip},
		{"cueWindowOut", cueWindowOut}, {"cueOutro", cueOutro},
	}
	prev := -1.0
	for _, c := range cues {
		if g := c.at / grid; math.Abs(g-math.Round(g)) > 1e-9 {
			t.Errorf("%s = %v 不在 grid（%v 秒）的整数倍上，动作音会和音乐错拍", c.name, c.at, grid)
		}
		if c.at <= prev {
			t.Errorf("%s = %v 没有晚于前一个时刻 %v：时间表乱序了", c.name, c.at, prev)
		}
		if c.at >= totalSec {
			t.Errorf("%s = %v 超出了全片时长 %v", c.name, c.at, float64(totalSec))
		}
		prev = c.at
	}
	// 强调色一种接一种地换：最后一种要在语言切换之前换完，否则两件事叠在一起。
	last := cueAccentFrom + float64(len(accentOrder)-2)*cueAccentStep
	if last >= cueLangFlip {
		t.Errorf("最后一次换强调色在 %v 秒，不早于语言切换 %v 秒", last, cueLangFlip)
	}
}

// TestTotalFramesIsWholeNumber 验证全片恰好是整数帧。
// 带小数的话最后一帧会被悄悄截掉，声音和画面差出半帧。
func TestTotalFramesIsWholeNumber(t *testing.T) {
	if got := float64(totalFrames); math.Abs(got-totalSec*fps) > 1e-9 {
		t.Errorf("totalFrames = %v，而 totalSec*fps = %v", got, totalSec*fps)
	}
}

// ---- 剧情 ----

// TestSceneIsAPureFunctionOfTimeAndLanguage 验证同一个（时刻，语言）永远得到同一份场景。
//
// 这是渲染成视频的前提：视频会先把所有帧构建一遍来混音，预览拖动时还会再构建，
// 同一帧每次必须得到同一个结果。所以剧情不能读时钟、不能记前一帧、不能依赖全局语言。
func TestSceneIsAPureFunctionOfTimeAndLanguage(t *testing.T) {
	// 故意先把全局语言设成与要测的语言相反：场景里的文字必须不受它影响。
	installLocale()
	for _, lang := range []string{langZH, langEN} {
		setUILang(otherLang(lang))
		for _, at := range allFrames() {
			a, b := sceneAt(at, lang), sceneAt(at, lang)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("t=%.3f lang=%s：同一时刻两次得到的场景不同", at, lang)
			}
		}
	}
}

// TestEnglishVideoStaysEnglish 验证英文版的界面从头到尾都是英文，不会在结尾切成中文。
//
// 缺陷背景：最初每个版本结尾都会把界面切到「另一种语言」来演示双语，英文版于是在最后几秒
// 出现整屏中文界面。英文版要的是一个完全英文的版本。
func TestEnglishVideoStaysEnglish(t *testing.T) {
	for _, at := range allFrames() {
		if got := uiLangAt(at, langEN); got != langEN {
			t.Fatalf("t=%.3f：英文版界面语言 = %s，应一直是英文", at, got)
		}
	}
	if flipsLanguage(langEN) {
		t.Error("英文版不该切换语言")
	}
}

// TestChineseVideoFlipsToEnglishAtTheEnd 验证中文版在 cueLangFlip 把界面切成英文，之前一直是中文。
func TestChineseVideoFlipsToEnglishAtTheEnd(t *testing.T) {
	for _, at := range allFrames() {
		want := langZH
		if at >= cueLangFlip {
			want = langEN
		}
		if got := uiLangAt(at, langZH); got != want {
			t.Fatalf("t=%.3f：中文版界面语言 = %s，应为 %s", at, got, want)
		}
	}
}

// TestEnglishVideoHasNoChinese 验证英文版里我们自己给的所有文字都没有汉字：
// 标题块文案，以及整个剧情里每一帧场景带的数据与输出面板文字。
//
// （应用自己的界面文字另有 internal/app 的 TestEnglishInterfaceHasNoChinese 把关。）
func TestEnglishVideoHasNoChinese(t *testing.T) {
	copyJSON, _ := json.Marshal(copyEN)
	if han.Match(copyJSON) {
		t.Errorf("英文文案里有汉字: %s", copyJSON)
	}
	for _, at := range allFrames() {
		b, err := json.Marshal(sceneAt(at, langEN))
		if err != nil {
			t.Fatal(err)
		}
		if loc := han.FindIndex(b); loc != nil {
			t.Fatalf("t=%.3f：英文版场景里出现汉字，上下文: %s", at, b[max(loc[0]-30, 0):min(loc[1]+30, len(b))])
		}
	}
}

// TestWorldFollowsTheStory 验证世界状态按剧情单调推进：该变的时刻变，变了就不回头。
func TestWorldFollowsTheStory(t *testing.T) {
	var prev world
	for i, at := range allFrames() {
		w := worldAt(at)
		if i > 0 {
			if prev.mongoRunning && !w.mongoRunning || prev.pulled && !w.pulled || w.blogUp < prev.blogUp {
				t.Fatalf("t=%.3f：世界状态倒退了: %+v → %+v", at, prev, w)
			}
		}
		prev = w
	}
	if worldAt(cueStartMongo-0.01).mongoRunning || !worldAt(cueStartMongo).mongoRunning {
		t.Error("dev-mongo 应恰好在 cueStartMongo 变成运行中")
	}
	if worldAt(cuePullDone-0.01).pulled || !worldAt(cuePullDone).pulled {
		t.Error("python 镜像应恰好在 cuePullDone 出现")
	}
	for want, at := range map[int]float64{
		0: cueComposeUp + composeDBUpAt - 0.01,
		1: cueComposeUp + composeDBUpAt,
		2: cueComposeUp + composeGhostUpAt,
	} {
		if got := worldAt(at).blogUp; got != want {
			t.Errorf("t=%.2f：blogUp = %d，应为 %d", at, got, want)
		}
	}
}

// TestStreamsFitTheirActs 验证两段流式输出：行的时间递增，最后一行出现在动作结束之前，
// 并且容器「起来了」的时刻不早于对应的 Starting 行（状态翻转不能抢在输出之前）。
func TestStreamsFitTheirActs(t *testing.T) {
	for _, st := range []struct {
		name  string
		lines []streamLine
		span  float64
	}{
		{"拉取镜像", pullLines, cuePullDone - cuePullStart},
		{"Compose 启动", composeUpLines, cueComposeDone - cueComposeUp},
	} {
		prev := -1.0
		for _, l := range st.lines {
			if l.at <= prev {
				t.Errorf("%s：行 %q 的时间 %v 没有晚于前一行 %v", st.name, l.text, l.at, prev)
			}
			prev = l.at
		}
		if prev >= st.span {
			t.Errorf("%s：最后一行出现在 %v 秒，动作只有 %v 秒", st.name, prev, st.span)
		}
	}

	startedAt := map[string]float64{}
	for _, l := range composeUpLines {
		startedAt[l.text] = l.at
	}
	if composeDBUpAt < startedAt["Starting blog-db-1 ..."] {
		t.Error("blog-db-1 在输出「Starting」之前就翻成了运行中")
	}
	if composeGhostUpAt < startedAt["Starting blog-ghost-1 ..."] {
		t.Error("blog-ghost-1 在输出「Starting」之前就翻成了运行中")
	}
}

// TestVirtualMachineStoryIsOrdered 验证虚拟机一路走下来：运行 → 正在关闭 → 已停止 → 正在启动 → 运行。
func TestVirtualMachineStoryIsOrdered(t *testing.T) {
	for _, c := range []struct {
		at     float64
		state  wslvm.State
		action app.DemoVMAction
		name   string
	}{
		{cueVMStopping - 0.01, wslvm.Running, app.DemoVMIdle, "运行中"},
		{cueVMStopping, wslvm.Running, app.DemoVMStopping, "正在关闭"},
		{cueVMStopped, wslvm.Stopped, app.DemoVMIdle, "已停止"},
		{cueVMStarting, wslvm.Stopped, app.DemoVMStarting, "正在启动"},
		{cueVMRunning, wslvm.Running, app.DemoVMIdle, "又在运行"},
	} {
		snap, action := vmAt(c.at)
		if snap.State != c.state || action != c.action {
			t.Errorf("t=%.2f：虚拟机应是「%s」（状态 %v、动作 %v），实际状态 %v、动作 %v",
				c.at, c.name, c.state, c.action, snap.State, action)
		}
	}
	// 停止时没有内存占用，运行时有：卡片上「内存占用」那一行靠它显示「—」或数字。
	if snap, _ := vmAt(cueVMStopped); snap.Memory != 0 {
		t.Errorf("已停止的虚拟机不该有内存占用: %d", snap.Memory)
	}
	if snap, _ := vmAt(cueVMStopping - 0.01); snap.Memory == 0 {
		t.Error("运行中的虚拟机应有内存占用")
	}
}

// TestAccentEndsOnBrandColor 验证强调色轮换最后回到品牌蓝：片尾的品牌卡片和图标是蓝的。
func TestAccentEndsOnBrandColor(t *testing.T) {
	if accentAt(0) != brandAccent || accentAt(totalSec-0.01) != brandAccent {
		t.Errorf("片头和片尾的强调色应是品牌色 %s", brandAccent)
	}
	seen := map[string]bool{}
	for _, at := range allFrames() {
		seen[accentAt(at)] = true
	}
	if len(seen) != 6 {
		t.Errorf("强调色应轮换 6 种，实际 %d 种: %v", len(seen), seen)
	}
}

// ---- 声音 ----

// TestMixIsSoundAndDeterministic 验证配乐的几条基本约定：
// 时长等于全片、没有 NaN / 无穷、不是静音，并且两次生成逐样本一致。
//
// 最后一条防的是有人图方便用了 math/rand：每次渲染的声音就不一样，
// 同一份代码今天和明天出来的视频对不上，没法排查。
func TestMixIsSoundAndDeterministic(t *testing.T) {
	a, b := buildMix(langZH), buildMix(langZH)
	if want := int(totalSec * rate); len(a.l) != want || len(a.r) != want {
		t.Fatalf("样本数 = %d/%d，应为 %d", len(a.l), len(a.r), want)
	}
	var energy float64
	for i := range a.l {
		for _, v := range []float32{a.l[i], a.r[i]} {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("第 %d 个样本是 %v", i, v)
			}
			energy += float64(v) * float64(v)
		}
	}
	if energy == 0 {
		t.Error("整段是静音")
	}
	if !reflect.DeepEqual(a.l, b.l) || !reflect.DeepEqual(a.r, b.r) {
		t.Error("两次生成的声音不一样：生成过程里有不确定的东西（随机数？）")
	}
}

// TestLanguageFlipSoundOnlyInVersionsThatFlip 验证「切换语言」那声提示音只在会切换的版本里：
// 中文版和英文版的声音，除了语言切换那一小段，必须逐样本一致。
func TestLanguageFlipSoundOnlyInVersionsThatFlip(t *testing.T) {
	zh, en := buildMix(langZH), buildMix(langEN)
	from, to := int(cueLangFlip*rate), int((cueLangFlip+1.5)*rate)
	var inside, outside float64
	for i := range zh.l {
		d := math.Abs(float64(zh.l[i]-en.l[i])) + math.Abs(float64(zh.r[i]-en.r[i]))
		if i >= from && i < to {
			inside += d
		} else {
			outside += d
		}
	}
	if outside != 0 {
		t.Errorf("两个版本在语言切换之外的声音不一致（差 %v）：英文版不该有任何只属于它的声音", outside)
	}
	if inside == 0 {
		t.Error("中文版在 cueLangFlip 处没有切换提示音")
	}
}

// TestSoftLimitNeverClips 验证软限幅：任何输入的输出都小于 ceiling，单调，小信号几乎线性。
func TestSoftLimitNeverClips(t *testing.T) {
	prev := math.Inf(-1)
	for v := -10.0; v <= 10; v += 0.01 {
		got := softLimit(v)
		if math.Abs(got) >= ceiling {
			t.Fatalf("softLimit(%v) = %v，不该达到上限 %v", v, got, ceiling)
		}
		if got < prev {
			t.Fatalf("softLimit 在 %v 处不单调", v)
		}
		prev = got
	}
	// 小信号几乎不被改变，只乘以整体增益 loudness。
	if got, want := softLimit(0.01), 0.01*loudness; math.Abs(got-want) > 1e-4 {
		t.Errorf("小信号 softLimit(0.01) = %v，应接近 %v", got, want)
	}
}

// ---- 命令行 ----

func TestParseLangs(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"all", []string{langZH, langEN}, false},
		{"", []string{langZH, langEN}, false},
		{"zh", []string{langZH}, false},
		{"ZH-Hans", []string{langZH}, false},
		{"en", []string{langEN}, false},
		{"fr", nil, true},
	}
	for _, c := range cases {
		got, err := parseLangs(c.in)
		if (err != nil) != c.wantErr || !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseLangs(%q) = %v, %v", c.in, got, err)
		}
	}
}

func TestRenderOptions(t *testing.T) {
	o, err := renderOptions("cpu", "hevc", 1800)
	if err != nil || o.Renderer != render.CPU || o.Codec != render.HEVC || o.Bitrate != 1_800_000 || o.AudioBitrate != 96_000 {
		t.Errorf("renderOptions(cpu, hevc, 1800) = %+v, %v", o, err)
	}
	// 码率 0 表示让编码器自己选：不动音频码率。
	if o, _ := renderOptions("auto", "h264", 0); o.Bitrate != 0 || o.AudioBitrate != 0 {
		t.Errorf("码率 0 应保持系统默认: %+v", o)
	}
	for _, bad := range [][3]string{{"vulkan", "h264", ""}, {"auto", "av1", ""}} {
		if _, err := renderOptions(bad[0], bad[1], 0); err == nil {
			t.Errorf("renderOptions(%q, %q) 应报错", bad[0], bad[1])
		}
	}
	if _, err := renderOptions("auto", "h264", -1); err == nil {
		t.Error("负的码率应报错")
	}
}

// ---- 真的渲染一遍 ----

// stillOf 用 CPU 渲染器画一帧：测试不依赖 GPU，没有显卡的 CI 上也一样。
func stillOf(t *testing.T, lang string, frame int) *image.RGBA {
	t.Helper()
	installLocale()
	img, err := render.Still(newComposition(lang, nil), frame, render.Options{Renderer: render.CPU})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// litPixels 数矩形（像素）里「够亮」的点：标题、界面文字都是浅色压在深色上。
func litPixels(img *image.RGBA, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if int(c.R)+int(c.G)+int(c.B) > 3*160 {
				n++
			}
		}
	}
	return n
}

// TestCompositionRendersEachAct 真的渲染几个有代表性的帧，验证整条管线（剧情 → 演示实例 → 真实视图 →
// 合成）能出图：尺寸对，标题块和窗口里都有文字，片尾也有内容。
//
// 只数「亮点」，不比对像素：字体渲染随机器略有差异，数量级才是稳的。
func TestCompositionRendersEachAct(t *testing.T) {
	if testing.Short() {
		t.Skip("渲染要把前面的帧全构建一遍，较慢")
	}
	px := func(dip float32) int { return int(dip * scale) }
	headline := image.Rect(px(winX), px(10), px(winX)+px(500), px(winY)-px(8))
	window := image.Rect(px(winX), px(winY), px(winX+winW), px(winY+barH+bodyH))
	center := image.Rect(canvasW/2-300, canvasH/2-150, canvasW/2+300, canvasH/2+150)

	for _, c := range []struct {
		name   string
		lang   string
		sec    float64
		region image.Rectangle
		min    int
	}{
		{"中文版：容器幕", langZH, cueLogsLoaded + 0.5, window, 3000},
		{"中文版：标题块", langZH, cueLogsLoaded + 0.5, headline, 800},
		{"英文版：Compose 幕", langEN, cueComposeUp + 2, window, 3000},
		{"英文版：标题块", langEN, cueComposeUp + 2, headline, 800},
		{"英文版：片尾", langEN, totalSec - 0.2, center, 800},
	} {
		t.Run(c.name, func(t *testing.T) {
			img := stillOf(t, c.lang, sec(c.sec))
			if b := img.Bounds(); b.Dx() != canvasW || b.Dy() != canvasH {
				t.Fatalf("尺寸 = %v，应为 %dx%d", b, canvasW, canvasH)
			}
			if got := litPixels(img, c.region); got < c.min {
				t.Errorf("%v 里只有 %d 个亮点（至少 %d）：这一帧像是空的", c.region, got, c.min)
			}
		})
	}
}

// TestWriteFrameMakesTheDirectoryAndAValidPNG 验证 -frame 导出：目录不存在时自己建，写出的是合法 PNG。
func TestWriteFrameMakesTheDirectoryAndAValidPNG(t *testing.T) {
	if testing.Short() {
		t.Skip("渲染较慢")
	}
	installLocale()
	dir := filepath.Join(t.TempDir(), "a", "b") // 嵌套且不存在
	if err := writeFrame(newComposition(langEN, nil), 60, dir, langEN, "cpu"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "frame-en-0060.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("写出的不是合法 PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != canvasW || b.Dy() != canvasH {
		t.Errorf("尺寸 = %v", b)
	}
}

// TestWriteVideoMakesTheOutputDirectory 是 writeVideo 的回归测试。
//
// 缺陷现象：render.Video 不会替调用方建目录，输出目录不存在时只报一个晦涩的
// Media Foundation 错误 0x80070003，看不出是目录的问题。writeVideo 必须先建目录。
// 没有 H.264 编码器的机器上编码会失败（错误里带 errors.ErrUnsupported，或者是 Media Foundation 的错误），
// 这时只验证目录，然后跳过。
func TestWriteVideoMakesTheOutputDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("要起编码器，较慢")
	}
	tiny := render.Composition{
		Width: 256, Height: 144, FPS: fps, Frames: 3,
		View: func(c *ui.Context, f render.Frame) { ui.Text(c, "x") },
	}
	dir := filepath.Join(t.TempDir(), "nested", "out")
	_, err := writeVideo(context.Background(), tiny, dir, langEN, "cpu", "h264", 0)
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("输出目录没有建出来: %v", statErr)
	}
	switch {
	case err == nil:
		if info, statErr := os.Stat(filepath.Join(dir, "wslc-manager-promo-en.mp4")); statErr != nil || info.Size() == 0 {
			t.Errorf("没有写出视频文件: %v", statErr)
		}
	case errors.Is(err, errors.ErrUnsupported), strings.Contains(err.Error(), "Media Foundation"):
		// 目录是否建出来已经在上面断言过了，所以这里放心跳过：
		// CI 的 Windows Server 镜像不一定装了 Media Foundation，编码会失败，但那不是这条测试要防的缺陷。
		t.Skipf("这台机器没有可用的编码器: %v", err)
	default:
		t.Fatalf("writeVideo: %v", err)
	}
}

// ---- README 预览动图 ----

// TestPreviewShowsACompleteFrame 验证预览的起止点：第一帧必须是个完整的画面（窗口已经升起、标题块已经出现），
// 终点在窗口落下之前。
//
// 为什么在意第一帧：有些地方只显示动图的第一帧（链接预览、不播放动图的阅读器）。
// 片头里窗口还没升起的那几帧是一片空黑，从那里开始截的话，README 上看到的就是一块黑。
func TestPreviewShowsACompleteFrame(t *testing.T) {
	// 标题块右边的小标签逐个弹出（最晚一个在标题出现后约 0.6 秒才开始，再花约 0.4 秒到位），
	// 起点早于这个时刻，第一帧里就有半透明的小标签。
	if previewFrom < cueContainers+1.0 {
		t.Errorf("预览起点 %v 离标题块出现（%v）不到 1 秒：小标签还没弹出到位", previewFrom, cueContainers)
	}
	if previewFrom < cueWindowIn+0.5 {
		t.Errorf("预览起点 %v 离窗口开始升起（%v）太近：窗口还没升到位", previewFrom, cueWindowIn)
	}
	if previewTo >= cueWindowOut {
		t.Errorf("预览终点 %v 不早于窗口落下的 %v：结尾会拍到一半的窗口", previewTo, cueWindowOut)
	}
	if got := previewTo - previewFrom; got < 20 {
		t.Errorf("预览只有 %.1f 秒，装不下几幕内容", got)
	}
}

// TestPreviewArgs 验证 ffmpeg 参数：截取区间、帧率与宽度、动画 WebP 编码器、循环播放、质量。
func TestPreviewArgs(t *testing.T) {
	args := previewArgs("in.mp4", "out.webp", previewSpec{from: 3.9, length: 29.3})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-ss 3.90", "-t 29.30", "-i in.mp4",
		"fps=15,scale=880:-1", "-c:v libwebp_anim", "-quality 80", "-loop 0",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("参数里缺 %q: %s", want, joined)
		}
	}
	if args[len(args)-1] != "out.webp" {
		t.Errorf("输出文件应是最后一个参数: %v", args)
	}
	// 质量不能降到 70：帧间增量误差累积，暗色背景上会出块状残影（见 previewQuality 的注释）。
	if previewQuality < 80 {
		t.Errorf("previewQuality = %d，低于 80 会出现块状残影", previewQuality)
	}
}

// requireFFmpeg 在没有 ffmpeg 的机器上跳过测试。
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("没有 ffmpeg")
	}
}

// TestWritePreviewMakesAnAnimatedWebP 真的跑一遍：先渲染一个很小的视频，再用 ffmpeg 做成动画 WebP，
// 验证写出的是合法的动画 WebP（RIFF/WEBP 文件头，带 ANIM 块）。
// 编码器或 ffmpeg 的 WebP 编码器不可用时跳过。
func TestWritePreviewMakesAnAnimatedWebP(t *testing.T) {
	if testing.Short() {
		t.Skip("要起编码器和 ffmpeg，较慢")
	}
	requireFFmpeg(t)
	tiny := render.Composition{
		Width: 256, Height: 144, FPS: fps, Frames: 12,
		View: func(c *ui.Context, f render.Frame) {
			ui.Text(c, "x").Opacity(float32(f.Progress()))
		},
	}
	mp4, err := writeVideo(context.Background(), tiny, t.TempDir(), langEN, "cpu", "h264", 0)
	if err != nil {
		t.Skipf("这台机器渲染不出视频: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "nested", "media") // 目录不存在，writePreview 要自己建
	out, err := writePreview(context.Background(), mp4, dir, langEN, previewSpec{from: 0, length: 0.4})
	if err != nil {
		if strings.Contains(err.Error(), "libwebp_anim") || strings.Contains(err.Error(), "Unknown encoder") {
			t.Skipf("这个 ffmpeg 没有动画 WebP 编码器: %v", err)
		}
		t.Fatal(err)
	}
	if filepath.Base(out) != "promo-en.webp" {
		t.Errorf("文件名 = %s，应为 promo-en.webp", filepath.Base(out))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatalf("不是 WebP 文件，文件头: %q", data[:min(12, len(data))])
	}
	if !bytes.Contains(data, []byte("ANIM")) {
		t.Error("没有 ANIM 块：写出的不是动画 WebP")
	}
}

// TestWritePreviewFailureLeavesNoFile 验证失败时不留半成品。
//
// ffmpeg 是直接写目标文件的；失败后留下一个写了一半的 promo-en.webp，README 引用它就是一张坏图。
// 这里换上「先写出半个文件、再失败」的假 ffmpeg：真的 ffmpeg 在输入打不开时根本不会创建输出文件，
// 用垃圾输入测不到清理逻辑（最初就是那样写的，变异验证发现删掉清理代码测试照样通过）。
func TestWritePreviewFailureLeavesNoFile(t *testing.T) {
	oldFind, oldRun := findFFmpeg, runFFmpeg
	t.Cleanup(func() { findFFmpeg, runFFmpeg = oldFind, oldRun })
	findFFmpeg = func() (string, error) { return "fake-ffmpeg", nil }
	runFFmpeg = func(ctx context.Context, ffmpeg string, args []string) ([]byte, error) {
		out := args[len(args)-1] // 输出文件是最后一个参数
		if err := os.WriteFile(out, []byte("RIFF half-written"), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("conversion failed!"), errors.New("exit status 1")
	}

	dir := filepath.Join(t.TempDir(), "media")
	_, err := writePreview(context.Background(), "in.mp4", dir, langEN, defaultPreview)
	if err == nil {
		t.Fatal("ffmpeg 失败了，writePreview 应当报错")
	}
	if !strings.Contains(err.Error(), "conversion failed!") {
		t.Errorf("错误里应带上 ffmpeg 的输出，方便排查: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "promo-en.webp")); !os.IsNotExist(statErr) {
		t.Errorf("失败后不该留下写了一半的输出文件: %v", statErr)
	}
}

// TestWritePreviewWithoutFFmpeg 验证找不到 ffmpeg 时给出明白的错误，并且不去建输出目录。
func TestWritePreviewWithoutFFmpeg(t *testing.T) {
	oldFind := findFFmpeg
	t.Cleanup(func() { findFFmpeg = oldFind })
	findFFmpeg = func() (string, error) { return "", exec.ErrNotFound }

	dir := filepath.Join(t.TempDir(), "media")
	_, err := writePreview(context.Background(), "in.mp4", dir, langEN, defaultPreview)
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("应报告缺少 ffmpeg，实际: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("找不到 ffmpeg 时不该留下一个空的输出目录")
	}
}

// TestDefaultIsNotCompressed 记录一个产品决定：默认不压缩视频。
//
// 早先默认码率取 3000 kbps，为的是把成片压进 GitHub README 视频 10 MB 的上限。用户后来决定把视频
// 自己托管到 Cloudflare R2，不再受那个限制，并要求这边不要再压缩。所以默认码率是 0（系统编码器按自己的
// 画质选，音频也用默认的 192 kbps）；要更小的文件时才显式传 -bitrate。
func TestDefaultIsNotCompressed(t *testing.T) {
	o, err := renderOptions("auto", "h264", defaultBitrate)
	if err != nil {
		t.Fatal(err)
	}
	if defaultBitrate != 0 || o.Bitrate != 0 || o.AudioBitrate != 0 {
		t.Errorf("默认不该限制码率：defaultBitrate=%d，选项 %+v", defaultBitrate, o)
	}
}
