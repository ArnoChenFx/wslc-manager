package main

// 本文件把成片做成 README 里循环播放的预览动图（WebP）。
//
// 为什么需要它：GitHub 的 README 不能内联播放仓库里的 mp4（`<video>` 标签会被过滤，Release 资产又是
// 当作下载送出的），能播放的只有网页编辑器拖拽上传生成的 user-attachments 地址——那一步要经过用户的 GitHub
// 账号，不是本地命令能做的。动图是图片，写进 README 立刻就能动，不依赖任何上传。
//
// 用 WebP 而不是 GIF：同样 30 秒，GIF 要 4–6 MB（还限 128/256 色），WebP 只要 0.5–1.8 MB（看源视频的噪点）且是真彩色，
// 往仓库里提交的二进制小得多。代价是个别旧浏览器不支持动画 WebP（GIF 兼容性最宽）。
//
// 依赖 ffmpeg（要带 libwebp_anim 编码器）。render 插件自己只出 MP4 / MOV / WebM 与 PNG / JPEG，没有动图。

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// 预览动图的参数。
const (
	previewFPS   = 15
	previewWidth = 880 // README 正文约 880 像素宽，再大也会被缩小
	// previewQuality 取 80，不要降到 70：动画 WebP 是帧间增量编码，有损误差会逐帧累积，
	// q70 在大片暗色背景上实测会出现块状残影（同一时刻与原视频逐帧对比过）；q80 肉眼与原视频无差别。
	previewQuality = 80
)

// previewSpec 是从成片里截取的区间（秒）。
type previewSpec struct{ from, length float64 }

// defaultPreview 是正式用的区间，见 cue.go 里的 previewFrom / previewTo。
var defaultPreview = previewSpec{from: previewFrom, length: previewTo - previewFrom}

// previewArgs 返回 ffmpeg 的参数：把 mp4 的 sp 区间做成循环播放的 WebP 动图 out。
func previewArgs(mp4, out string, sp previewSpec) []string {
	secs := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	return []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", secs(sp.from), "-t", secs(sp.length),
		"-i", mp4,
		"-vf", fmt.Sprintf("fps=%d,scale=%d:-1:flags=lanczos", previewFPS, previewWidth),
		"-c:v", "libwebp_anim", "-quality", strconv.Itoa(previewQuality),
		"-preset", "text", "-compression_level", "6",
		"-loop", "0",
		out,
	}
}

// findFFmpeg 和 runFFmpeg 是两个可以替换的依赖，测试用。
//
// 做成变量而不是直接调用，是因为真的 ffmpeg 在输入打不开时根本不会创建输出文件，
// 没法验证「失败后清理写了一半的输出」；换成「先写出半个文件再失败」的假实现才测得到。
// 也让这部分测试不依赖本机装了 ffmpeg。
var (
	findFFmpeg = func() (string, error) { return exec.LookPath("ffmpeg") }
	runFFmpeg  = func(ctx context.Context, ffmpeg string, args []string) ([]byte, error) {
		return exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	}
)

// writePreview 把成片 mp4 做成 dir 下的 promo-<语言>.webp，返回写出的文件路径。
func writePreview(ctx context.Context, mp4, dir, lang string, sp previewSpec) (string, error) {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return "", fmt.Errorf("-webp 需要 ffmpeg，但在 PATH 里找不到: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "promo-"+shortLang(lang)+".webp")

	if msg, err := runFFmpeg(ctx, ffmpeg, previewArgs(mp4, out, sp)); err != nil {
		// ffmpeg 是直接写目标文件的，失败时不能留下写了一半的东西：README 会引用它，
		// 那就是一张坏图，而且下次运行看到文件「已经在」，不容易发现上一次是失败的。
		_ = os.Remove(out)
		return "", fmt.Errorf("ffmpeg 生成预览动图失败: %w\n%s", err, strings.TrimSpace(string(msg)))
	}
	abs, _ := filepath.Abs(out)
	return abs, nil
}
