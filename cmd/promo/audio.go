package main

// 本文件生成宣传片的声音：一段很轻的背景音乐，加上随画面落下的动作音。
//
// 全部是合成出来的，不带任何素材文件：
//   - 没有版权问题，也不用往仓库里塞二进制；
//   - 动作音的时刻直接读 cue.go 里的剧情时间表，画面一动，声音就在同一个采样点响起；
//   - 生成过程是确定的（噪声用固定种子的线性同余，不用 math/rand），同一份代码每次渲染出同样的声音。
//
// 配乐很克制：85.7 BPM，C 大调的四个和弦（Cmaj7 – Gadd9 – Am7 – Fmaj7）循环，
// 垫底的 pad、低音、八分音符的拨弦琶音。它是背景，不抢画面。
// 我没法在这里「听」它，只能靠数值把关（峰值、音量、有无削波），所以刻意选了最不容易出错的写法。

import (
	"math"

	"github.com/egoist/mygo/plugins/render"
)

// rate 是采样率。render 把所有声音统一重采样成 48 kHz，这里直接按它生成，省掉一次重采样。
const rate = 48000

// mixer 是一块双声道的混音缓冲。
type mixer struct{ l, r []float32 }

func newMixer(seconds float64) *mixer {
	n := int(seconds * rate)
	return &mixer{l: make([]float32, n), r: make([]float32, n)}
}

// put 把一段单声道声音混进 at 秒处。pan 取 -1（全左）到 1（全右），用等功率声像：
// 居中时左右各 0.707，听上去不会比偏向一侧时更响。
func (m *mixer) put(at float64, pcm []float32, gain, pan float64) {
	start := int(at*rate + 0.5)
	angle := (pan + 1) * math.Pi / 4
	gl, gr := gain*math.Cos(angle), gain*math.Sin(angle)
	for i, v := range pcm {
		j := start + i
		if j < 0 || j >= len(m.l) {
			continue
		}
		m.l[j] += float32(float64(v) * gl)
		m.r[j] += float32(float64(v) * gr)
	}
}

// 总电平：先把峰值归一到 1，再乘 loudness 送进软限幅。
//
// 动作音（铃声、咚声）的峰值远高于铺底的音乐，只按峰值归一的话，铺底的音乐就会很轻
// （实测整体 RMS 只有 -19 dB）。软限幅把少数几个尖峰压下来，让整体能抬高几个分贝，
// 又不会削出毛刺：tanh 是平滑的，只有超过大约一半的样本才被轻轻压弯。
const (
	loudness = 1.3
	ceiling  = 0.92
)

// sound 把混音缓冲变成 render.Sound：整体抬高后过软限幅，交错成立体声。
func (m *mixer) sound() *render.Sound {
	top := 0.0
	for i := range m.l {
		top = math.Max(top, math.Max(math.Abs(float64(m.l[i])), math.Abs(float64(m.r[i]))))
	}
	k := 1.0
	if top > 0 {
		k = 1 / top
	}
	out := make([]float32, 2*len(m.l))
	for i := range m.l {
		out[2*i] = float32(softLimit(float64(m.l[i]) * k))
		out[2*i+1] = float32(softLimit(float64(m.r[i]) * k))
	}
	return render.SoundOf(rate, 2, out)
}

// softLimit 是整体电平的软限幅：输入是归一到峰值 1 之内的样本，输出的绝对值永远小于 ceiling。
//
// 抽成独立函数，是为了能单独验证它「不削波、单调、小信号几乎线性」。
func softLimit(v float64) float64 { return ceiling * math.Tanh(v*loudness/ceiling) }

// midi 把 MIDI 音符号换算成频率（A4 = 69 = 440 Hz）。
func midi(n float64) float64 { return 440 * math.Pow(2, (n-69)/12) }

// smooth 是平滑的 0→1 过渡（三次 Hermite），起止斜率为 0，用来做淡入淡出，没有「咔」的一声。
func smooth(x float64) float64 {
	x = math.Min(math.Max(x, 0), 1)
	return x * x * (3 - 2*x)
}

// ---- 音色 ----

// pluck 是拨弦：基频加二、三次泛音，高次泛音衰减得更快，音头亮、尾巴圆。
func pluck(freq, dur, amp float64) []float32 {
	out := make([]float32, int(dur*rate))
	for i := range out {
		t := float64(i) / rate
		s := math.Sin(2*math.Pi*freq*t)*math.Exp(-t*5) +
			0.38*math.Sin(2*math.Pi*2*freq*t)*math.Exp(-t*9) +
			0.12*math.Sin(2*math.Pi*3*freq*t)*math.Exp(-t*14)
		// 三毫秒的起音：没有它，波形从 0 以外的相位突然开始，会有一声细小的「啪」。
		out[i] = float32(amp * math.Min(t/0.003, 1) * s)
	}
	return out
}

// padNote 是垫底的一个音：两根略微失谐的正弦叠在一起（宽厚，不是单薄的一根线），
// 慢慢淡入、慢慢淡出。
func padNote(freq, dur, amp float64) []float32 {
	out := make([]float32, int(dur*rate))
	for i := range out {
		t := float64(i) / rate
		env := smooth(t/0.45) * smooth((dur-t)/0.6)
		s := math.Sin(2*math.Pi*freq*0.9988*t) + math.Sin(2*math.Pi*freq*1.0012*t) +
			0.25*math.Sin(2*math.Pi*2*freq*t)
		out[i] = float32(amp * env * s * 0.5)
	}
	return out
}

// bassNote 是低音：正弦加一点二次泛音（小音箱放不出很低的基频，二次泛音让它仍然听得见）。
func bassNote(freq, dur, amp float64) []float32 {
	out := make([]float32, int(dur*rate))
	for i := range out {
		t := float64(i) / rate
		env := math.Min(t/0.015, 1) * math.Exp(-t*1.6) * smooth((dur-t)/0.25)
		s := math.Sin(2*math.Pi*freq*t) + 0.3*math.Sin(2*math.Pi*2*freq*t)
		out[i] = float32(amp * env * s)
	}
	return out
}

// bell 是铃声：几个不成整数倍的泛音，各自衰减，用在「完成了」的提示上。
func bell(freq, dur, amp float64) []float32 {
	ratios := [...]float64{1, 2.76, 5.4, 8.93}
	levels := [...]float64{1, 0.5, 0.24, 0.1}
	decays := [...]float64{2.2, 3.4, 5.2, 7.5}
	out := make([]float32, int(dur*rate))
	for i := range out {
		t := float64(i) / rate
		var s float64
		for k := range ratios {
			s += levels[k] * math.Sin(2*math.Pi*freq*ratios[k]*t) * math.Exp(-t*decays[k])
		}
		out[i] = float32(amp * math.Min(t/0.002, 1) * s)
	}
	return out
}

// tick 是一下很短的「嗒」：选中一行、切换页面这类小动作的提示。
func tick(freq, amp float64) []float32 {
	out := make([]float32, int(0.06*rate))
	for i := range out {
		t := float64(i) / rate
		out[i] = float32(amp * (math.Sin(2*math.Pi*freq*t) + 0.5*math.Sin(2*math.Pi*1.5*freq*t)) * math.Exp(-t*85))
	}
	return out
}

// blip 是一声频率从 f0 滑到 f1 的短音（指数滑动，音高的变化听上去才是均匀的）。
func blip(f0, f1, dur, amp float64) []float32 {
	out := make([]float32, int(dur*rate))
	var phase float64
	for i := range out {
		t := float64(i) / rate
		p := t / dur
		freq := f0 * math.Pow(f1/f0, p)
		phase += 2 * math.Pi * freq / rate
		out[i] = float32(amp * math.Sin(phase) * math.Min(t/0.004, 1) * math.Exp(-p*3.2))
	}
	return out
}

// thud 是一声低沉的「咚」：频率从 150 Hz 快速落到 50 Hz，关虚拟机时用。
func thud(dur, amp float64) []float32 {
	out := make([]float32, int(dur*rate))
	var phase float64
	for i := range out {
		t := float64(i) / rate
		phase += 2 * math.Pi * (50 + 100*math.Exp(-t*16)) / rate
		out[i] = float32(amp * math.Sin(phase) * math.Min(t/0.003, 1) * math.Exp(-t*7))
	}
	return out
}

// whoosh 是一阵「呼」：白噪声过一个截止频率扫过去的带通滤波器，窗口升起、落下时用。
//
// 噪声用固定种子的线性同余生成而不是 math/rand：每次渲染都必须得到同样的声音。
// up 为 true 时频率向上扫（升起），否则向下扫（落下）。
func whoosh(dur, amp float64, up bool) []float32 {
	out := make([]float32, int(dur*rate))
	seed := uint32(0x9e3779b9)
	noise := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed>>8)/float64(1<<23) - 1
	}
	var low, band float64
	for i := range out {
		t := float64(i) / rate
		p := t / dur
		if !up {
			p = 1 - p
		}
		cutoff := 250 * math.Pow(14, p) // 250 Hz 扫到 3.5 kHz
		f := 2 * math.Sin(math.Pi*cutoff/rate)
		low += f * band
		high := noise() - low - 0.4*band
		band += f * high
		env := math.Sin(math.Pi * t / dur)
		out[i] = float32(amp * env * env * band * 0.5)
	}
	return out
}

// ---- 配乐 ----

// chord 是一个和弦：pad 的几个音、低音、琶音用的音（都是 MIDI 音符号）。
type chord struct {
	pad  [4]float64
	bass float64
	arp  [4]float64
}

// progression 是四个和弦的循环，每小节一个：Cmaj7 – Gadd9 – Am7 – Fmaj7。
// 声部连接是特意排过的：C4、E4、G4 这几个音在相邻和弦里尽量不动，只动一两个音，听上去才顺。
var progression = [4]chord{
	{pad: [4]float64{60, 64, 67, 71}, bass: 36, arp: [4]float64{72, 76, 79, 83}},
	{pad: [4]float64{59, 62, 67, 69}, bass: 43, arp: [4]float64{71, 74, 79, 81}},
	{pad: [4]float64{57, 60, 64, 67}, bass: 45, arp: [4]float64{72, 76, 79, 81}},
	{pad: [4]float64{57, 60, 64, 65}, bass: 41, arp: [4]float64{72, 76, 77, 81}},
}

// arpPattern 是一小节八个八分音符用的是和弦里的第几个音：先上行，到顶再回落。
var arpPattern = [8]int{0, 1, 2, 1, 3, 2, 1, 2}

// bed 铺背景音乐：pad、低音、琶音。
func bed(m *mixer) {
	bars := int(math.Round(totalSec / bar))
	for b := 0; b < bars; b++ {
		ch := progression[b%len(progression)]
		at := float64(b) * bar

		// pad：略长于一小节，让相邻和弦的尾巴和头叠在一起（淡出淡入），没有换和弦的「缝」。
		for i, n := range ch.pad {
			pan := []float64{-0.35, -0.12, 0.12, 0.35}[i]
			m.put(at, padNote(midi(n), bar+0.7, 0.05), 1, pan)
		}
		// 低音：第一拍和第三拍。
		m.put(at, bassNote(midi(ch.bass), beat*2-0.05, 0.16), 1, 0)
		m.put(at+beat*2, bassNote(midi(ch.bass), beat*2-0.05, 0.12), 1, 0)

		// 琶音：从第二小节开始（第一小节是片头，留给图标的三下拨弦），最后一小节不要（收在主和弦上）。
		if b == 0 || b == bars-1 {
			continue
		}
		for i, idx := range arpPattern {
			amp := 0.065
			if i%4 == 0 {
				amp = 0.10 // 每拍的第一个音略重
			}
			pan := math.Sin(float64(b*8+i)*0.9) * 0.4 // 左右轻轻地飘
			m.put(at+float64(i)*grid, pluck(midi(ch.arp[idx]), 0.9, amp), 1, pan)
		}
	}
}

// 提示音：几个常用的组合。
func chime(m *mixer, at float64) {
	// 三个铃声依次错开几十毫秒，像一次轻轻的上行琶音。
	for i, n := range []float64{84, 88, 91} {
		m.put(at+float64(i)*0.07, bell(midi(n), 1.6, 0.13), 1, []float64{-0.2, 0, 0.2}[i])
	}
}

// sfx 按剧情时间表放动作音。flip 表示这个版本的结尾会切换界面语言（见 flipsLanguage）。
func sfx(m *mixer, flip bool) {
	// 片头：图标一层层叠起来，三下上行的拨弦（C5、E5、G5），名字出现时一声很轻的铃。
	for i, c := range []float64{cueMarkBottom, cueMarkMiddle, cueMarkTop} {
		m.put(c, pluck(midi([]float64{72, 76, 79}[i]), 1.1, 0.22), 1, []float64{-0.25, 0, 0.25}[i])
	}
	m.put(cueWordmark, bell(midi(84), 1.8, 0.09), 1, 0)

	// 窗口升起、落下。
	m.put(cueWindowIn-0.1, whoosh(0.9, 0.30, true), 1, 0)
	m.put(cueWindowOut, whoosh(0.7, 0.26, false), 1, 0)

	// 第二幕：选中、点启动、打开日志。
	m.put(cueSelectMongo, tick(1500, 0.22), 1, -0.1)
	m.put(cueStartMongo, blip(520, 880, 0.14, 0.30), 1, 0)
	m.put(cueOpenLogs, tick(1100, 0.20), 1, 0.1)

	// 第三幕：切页、开始拉取（进度行一行一下很轻的嗒声）、完成。
	m.put(cuePageImage, tick(900, 0.18), 1, 0)
	m.put(cuePullStart, blip(440, 740, 0.14, 0.26), 1, 0)
	for i, l := range pullLines {
		m.put(cuePullStart+l.at, tick([]float64{2000, 2400}[i%2], 0.05), 1, 0.15)
	}
	chime(m, cuePullDone)

	// 第四幕：切页、选项目、点启动、输出行、两个服务起来、完成。
	m.put(cuePageCompose, tick(900, 0.18), 1, 0)
	m.put(cueSelectBlog, tick(1500, 0.22), 1, -0.1)
	m.put(cueComposeUp, blip(520, 880, 0.14, 0.30), 1, 0)
	for i, l := range composeUpLines {
		m.put(cueComposeUp+l.at, tick([]float64{2000, 2400}[i%2], 0.05), 1, 0.15)
	}
	m.put(cueComposeUp+composeDBUpAt, blip(660, 990, 0.12, 0.22), 1, -0.15)
	m.put(cueComposeUp+composeGhostUpAt, blip(740, 1110, 0.12, 0.22), 1, 0.15)
	chime(m, cueComposeDone)

	// 第五幕：切页，关虚拟机（咚 + 下滑的音），已停止，启动（上扫的音），又运行了。
	m.put(cuePageSystem, tick(900, 0.18), 1, 0)
	m.put(cueVMStopping, thud(0.5, 0.34), 1, 0)
	m.put(cueVMStopping, blip(700, 300, 0.30, 0.16), 1, 0)
	m.put(cueVMStopped, tick(700, 0.18), 1, 0)
	m.put(cueVMStarting, blip(220, 880, 0.55, 0.20), 1, 0)
	chime(m, cueVMRunning)

	// 第六幕：强调色每换一种，响一下上行音阶里的下一个音（C D E G A C），六种颜色六个音。
	m.put(cuePageBack, tick(900, 0.18), 1, 0)
	for i, n := range []float64{72, 74, 76, 79, 81, 84} {
		m.put(cueAccentFrom+float64(i)*cueAccentStep, pluck(midi(n), 0.8, 0.20), 1, []float64{-0.3, -0.18, -0.06, 0.06, 0.18, 0.3}[i])
	}
	// 语言切换：两个音一上一下，像把界面翻了个面。
	// 不切换的版本画面上什么都没发生，这声提示音就成了没有对应的杂音。
	if flip {
		m.put(cueLangFlip, pluck(midi(79), 0.7, 0.22), 1, -0.2)
		m.put(cueLangFlip+0.12, pluck(midi(84), 0.9, 0.22), 1, 0.2)
	}

	// 片尾：落在主和弦上的一下铃声。
	m.put(cueOutro, bell(midi(72), 3.0, 0.16), 1, -0.1)
	m.put(cueOutro, bell(midi(79), 3.0, 0.12), 1, 0.1)
	m.put(cueOutro+0.12, bell(midi(84), 2.8, 0.10), 1, 0)
}

// buildScore 生成某种语言的视频的声音。时长就是全片的时长。
//
// 按语言各生成一份而不是共用：两个版本的剧情不完全一样（英文版结尾不切换界面语言），
// 动作音要跟着各自的画面走。生成很快（几百毫秒），多生成一次不心疼。
func buildScore(lang string) *render.Sound { return buildMix(lang).sound() }

// buildMix 生成混音缓冲：配乐加动作音，还没有做总电平处理。拆出来是为了能直接检查样本。
func buildMix(lang string) *mixer {
	m := newMixer(totalSec)
	bed(m)
	sfx(m, flipsLanguage(lang))
	return m
}
