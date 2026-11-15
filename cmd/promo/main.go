// Command promo 用 MyGo 的 render 插件，把 WSLC Manager 自己的界面渲染成宣传片。
//
// 画面里的窗口不是仿制品：它用的是应用真实的根视图（internal/app 的演示模式），
// 数据是写死的、不连 wslc。界面一改，重新跑一遍，宣传片就跟着变，不会和产品对不上。
//
//	go run ./cmd/promo                      # 中英两版视频 → dist/promo/
//	go run ./cmd/promo -lang en             # 只渲染英文版
//	go run ./cmd/promo -frame 450 -lang zh  # 只导出第 450 帧（PNG），调画面用
//	go run ./cmd/promo -preview             # 在窗口里播放（不带声音）
//	go run ./cmd/promo -renderer cpu        # 没有 GPU 时强制用 CPU
//	go run ./cmd/promo -bitrate 3000        # 想要更小的文件时才用（默认不压，每个约 28 MB）
//	go run ./cmd/promo -webp docs/media     # 同时生成 README 顶部的预览动图（需要 ffmpeg）
//
// 它是个独立的小程序：只在需要宣传素材时手动跑，不参与应用的构建与发布。
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/render"
	"github.com/egoist/mygo/ui"
)

func main() {
	var (
		lang     = flag.String("lang", "all", "语言：zh、en 或 all")
		outDir   = flag.String("o", filepath.Join("dist", "promo"), "输出目录")
		frame    = flag.Int("frame", -1, "只导出这一帧（PNG）；需要指定单一语言")
		preview  = flag.Bool("preview", false, "在窗口里播放（不带声音）；需要指定单一语言")
		renderer = flag.String("renderer", "auto", "绘制：auto、gpu 或 cpu")
		codec    = flag.String("codec", "h264", "编码：h264 或 hevc")
		bitrate  = flag.Int("bitrate", defaultBitrate, "视频码率，单位 kbps；0（默认）表示让系统编码器按自己的画质选，约 6 Mbps、每个 28 MB 左右")
		webpDir  = flag.String("webp", "", "渲染视频后，再用 ffmpeg 把它做成 README 用的预览动图（WebP）放进这个目录，如 docs/media")
		mute     = flag.Bool("mute", false, "不要声音")
	)
	flag.Parse()

	langs, err := parseLangs(*lang)
	if err != nil {
		log.Fatal(err)
	}
	installLocale()

	// Ctrl+C 要能停下渲染，并且清掉没写完的半成品文件（render 在 ctx 结束时会删）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *webpDir != "" && (*preview || *frame >= 0) {
		log.Fatal("-webp 只在渲染视频时有用，不能和 -preview、-frame 一起用")
	}

	switch {
	case *preview:
		if len(langs) != 1 {
			log.Fatal("-preview 需要用 -lang 指定一种语言")
		}
		previewWindow(newComposition(langs[0], nil))
	case *frame >= 0:
		if len(langs) != 1 {
			log.Fatal("-frame 需要用 -lang 指定一种语言")
		}
		if err := writeFrame(newComposition(langs[0], nil), *frame, *outDir, langs[0], *renderer); err != nil {
			log.Fatal(err)
		}
	default:
		for _, l := range langs {
			// 声音按语言各生成一份：英文版不切换界面语言，也就没有那声「切换」提示音。
			var score *render.Sound
			if !*mute {
				score = buildScore(l)
			}
			mp4, err := writeVideo(ctx, newComposition(l, score), *outDir, l, *renderer, *codec, *bitrate)
			if err != nil {
				log.Fatal(err)
			}
			if *webpDir != "" {
				out, err := writePreview(ctx, mp4, *webpDir, l, defaultPreview)
				if err != nil {
					log.Fatal(err)
				}
				fmt.Println(out)
			}
		}
	}
}

// defaultBitrate 是默认的视频码率（kbps）：0，即不限，让系统编码器按自己的画质选（约 6 Mbps）。
//
// 早先这里取过 3000，为了把成片压进 GitHub README 视频 10 MB 的上限。用户后来决定自己把视频
// 托管到别处（Cloudflare R2），不再受那个限制，并要求这边不要再压缩——所以撤掉了。
// 要更小的文件时才用 -bitrate（比如 3000 约 8 MB，静止的界面录像用它也足够清晰）。
const defaultBitrate = 0

// parseLangs 把 -lang 的值换成语言标签列表。
func parseLangs(v string) ([]string, error) {
	switch strings.ToLower(v) {
	case "all", "":
		return []string{langZH, langEN}, nil
	case "zh", "zh-hans", "zh-cn":
		return []string{langZH}, nil
	case "en":
		return []string{langEN}, nil
	}
	return nil, fmt.Errorf("不认识的语言 %q：请用 zh、en 或 all", v)
}

// shortLang 返回文件名里用的语言缩写。
func shortLang(lang string) string {
	if lang == langEN {
		return "en"
	}
	return "zh"
}

// renderOptions 把命令行选项换成 render.Options。
func renderOptions(renderer, codec string, bitrate int) (render.Options, error) {
	var o render.Options
	if bitrate < 0 {
		return o, fmt.Errorf("码率不能是负数: %d", bitrate)
	}
	o.Bitrate = bitrate * 1000
	if bitrate > 0 {
		// 声音在这支片子里只是配乐，96 kbps 的 AAC 足够；默认的 192 kbps 白白占掉 1 MB。
		o.AudioBitrate = 96_000
	}
	switch renderer {
	case "auto":
		o.Renderer = render.Auto
	case "gpu":
		o.Renderer = render.GPU
	case "cpu":
		o.Renderer = render.CPU
	default:
		return o, fmt.Errorf("不认识的绘制方式 %q：请用 auto、gpu 或 cpu", renderer)
	}
	switch codec {
	case "h264", "":
		o.Codec = render.H264
	case "hevc":
		o.Codec = render.HEVC
	default:
		return o, fmt.Errorf("不认识的编码 %q：请用 h264 或 hevc", codec)
	}
	return o, nil
}

// writeVideo 渲染一种语言的视频。
func writeVideo(ctx context.Context, comp render.Composition, dir, lang, renderer, codec string, bitrate int) (string, error) {
	o, err := renderOptions(renderer, codec, bitrate)
	if err != nil {
		return "", err
	}
	// render.Video 不会替我们建目录：目录不存在时只报一个晦涩的 Media Foundation 错误 0x80070003。
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	o.Progress = func(p render.Progress) {
		if p.Done%fps == 0 || p.Done == p.Frames {
			fmt.Fprintf(os.Stderr, "\r[%s] %s: %d/%d 帧 (%.0f fps)  ", shortLang(lang), p.Renderer,
				p.Done, p.Frames, float64(p.Done)/p.Elapsed.Seconds())
		}
	}
	path := filepath.Join(dir, "wslc-manager-promo-"+shortLang(lang)+".mp4")
	err = render.Video(ctx, comp, path, o)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("渲染 %s 失败: %w", shortLang(lang), err)
	}
	abs, _ := filepath.Abs(path)
	fmt.Println(abs)
	return abs, nil
}

// writeFrame 把某一帧导出成 PNG，调画面用：不用等整片渲染完。
func writeFrame(comp render.Composition, frame int, dir, lang, renderer string) error {
	o, err := renderOptions(renderer, "", 0)
	if err != nil {
		return err
	}
	img, err := render.Still(comp, frame, o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("frame-%s-%04d.png", shortLang(lang), frame))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	abs, _ := filepath.Abs(path)
	fmt.Println(abs)
	return nil
}

// previewWindow 在窗口里播放合成，带暂停和跳转。
//
// 预览不带声音（render.NewPlayer 就是这样），要听声音得渲染成视频。
func previewWindow(comp render.Composition) {
	player, err := render.NewPlayer(comp)
	if err != nil {
		log.Fatal(err)
	}
	player.SetLoop(true)
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title: "WSLC Manager promo preview", Width: 1000, Height: 640,
			Content: ui.View(func(c *ui.Context) { previewView(c, player) }),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// previewView 是预览窗口的内容：视频加一排控件。
func previewView(c *ui.Context, p *ui.VideoPlayer) {
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		ui.Video(c, p).Grow(1).FillWidth()
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			label := "暂停"
			if !p.Playing() {
				label = "播放"
			}
			if ui.Button(c, label).Clicked() {
				if p.Playing() {
					p.Pause()
				} else {
					p.Play()
				}
			}
			at := p.Position().Seconds()
			if ui.Slider(c, &at, 0, p.Duration().Seconds()).Grow(1).Changed() {
				p.Seek(time.Duration(at * float64(time.Second)))
			}
			ui.Text(c, fmt.Sprintf("第 %d 帧", int(p.Position().Seconds()*fps))).Width(90)
		})
	})
}
