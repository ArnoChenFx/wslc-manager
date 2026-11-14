package app

// 强调色（accent）的预设色板与设置页里的选择器。
//
// 设计取舍：
//   - 给一组**预设**而不是自由取色器。自由取色看着强大，但用户很容易
//     选到对比度不足的颜色，把自己界面搞成「看不清」；预设都经过
//     明度与饱和度的挑选，保证按钮文字、选中行、焦点环都还读得清。
//   - 强调色只影响「点睛」元素：主按钮、焦点环、选中行、进度条。
//     背景与文字保持中性灰，避免整屏被颜色淹没。

import (
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/locales"
)

// accentChoice 是一个可选强调色。
type accentChoice struct {
	// Key 是写进设置文件的标识，稳定不变。
	Key string
	// Dark 与 Light 分别是深色、浅色主题下的实际颜色。
	// 两套分开给，是因为同一个色相在深底和白底上的最佳明度不同。
	Dark  string
	Light string
}

// accentPalette 是预设色板。
//
// 第一个是默认值（品红）。命名用颜色语言而不是「蓝/红」，
// 免得翻译时出现「Blue」这种只对一半语言准确的词。
var accentPalette = []accentChoice{
	{Key: "magenta", Dark: "#ee458c", Light: "#d62a70"},
	{Key: "violet", Dark: "#9d74f8", Light: "#7c3aed"},
	{Key: "blue", Dark: "#3b82f6", Light: "#2563eb"},
	{Key: "teal", Dark: "#14b8a6", Light: "#0d9488"},
	{Key: "amber", Dark: "#f59e0b", Light: "#d97706"},
	{Key: "green", Dark: "#22c55e", Light: "#16a34a"},
}

// defaultAccent 是出厂强调色。
const defaultAccent = "magenta"

// parseHex 解析 #rrggbb 或 #rgb。
func parseHex(s string) (ui.Color, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return ui.Transparent, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return ui.Transparent, false
	}
	return ui.RGBA(uint8(v>>16), uint8(v>>8), uint8(v), 1), true
}

// accentLabel 返回强调色的本地化名称。
func accentLabel(key string) string {
	switch key {
	case "magenta":
		return locales.AccentMagenta()
	case "violet":
		return locales.AccentViolet()
	case "blue":
		return locales.AccentBlue()
	case "teal":
		return locales.AccentTeal()
	case "amber":
		return locales.AccentAmber()
	case "green":
		return locales.AccentGreen()
	}
	return key
}

// accentByKey 按标识找配色，找不到时返回默认。
func accentByKey(key string) accentChoice {
	for _, a := range accentPalette {
		if a.Key == key {
			return a
		}
	}
	for _, a := range accentPalette {
		if a.Key == defaultAccent {
			return a
		}
	}
	return accentPalette[0]
}

// accentColor 返回某个预设在当前外观下的颜色。
func (a accentChoice) color(dark bool) ui.Color {
	hex := a.Light
	if dark {
		hex = a.Dark
	}
	if c, ok := parseHex(hex); ok {
		return c
	}
	return ui.Hex(hex)
}

// textOnAccent 返回压在强调色上最清晰的前景色（白或近黑）。
//
// 判据用 WCAG 的相对亮度而非简单的 R+G+B：后者对绿色严重低估，
// 例如 #22c55e 的算术均值不低，但它其实非常亮。
func textOnAccent(c ui.Color) ui.Color {
	light := ui.Hex("#ffffff")
	dark := ui.Hex("#101614")

	// 哪个对比度高就用哪个，避免硬编码阈值带来的边界误判。
	if ratioOf(dark, c) >= ratioOf(light, c) {
		return dark
	}
	return light
}

// ratioOf 返回两色的 WCAG 对比度，用于挑选前景色。
func ratioOf(a, b ui.Color) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// relLuminance 计算 WCAG 2.x 的相对亮度。
func relLuminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// applyAccent 把一个预设应用到主题上，派生悬停/按下/焦点等颜色。
func applyAccent(t *ui.Theme, choice accentChoice) {
	c := choice.color(t.Dark)
	t.Accent = c
	t.AccentHover = c.Mix(ui.Hex("#ffffff"), 0.16)
	t.AccentPressed = c.Mix(ui.Hex("#000000"), 0.16)

	// 按钮/选中项上的文字颜色按强调色亮度决定。
	//
	// 一律用白字是错的：青绿 (#14b8a6)、琥珀 (#f59e0b)、绿 (#22c55e)
	// 这些亮色的相对亮度很高，白字压上去对比度只有 2.1–2.5，
	// 远低于 WCAG 的 3.0，实测就是「按钮上的字看不清」。
	// 亮色改用近黑深字，深色仍用白字。
	t.AccentText = textOnAccent(c)

	// 焦点环与文本选中跟着强调色走，保证整体一致。
	t.Focus = c.Alpha(0.62)
	t.Selection = c.Alpha(0.32)
}

// accentPicker 在设置页渲染一行色块，点击即切换强调色。
//
// 每个色块是圆角盒（Radius 半边长 = 圆），选中的那个加一圈描边，
// 让人一眼看出当前选的是哪个。
func (a *App) accentPicker(c *ui.Context) {
	t := c.Theme()
	cur := a.settings.Accent
	if cur == "" {
		cur = defaultAccent
	}

	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		for _, choice := range accentPalette {
			choice := choice
			// 色块本身用当前外观下的实际颜色，所见即所得。
			swatch := choice.color(t.Dark)
			selected := choice.Key == cur

			// 外层做个「点击热区」，比 22x22 的色块大，好点。
			hit := ui.Box(c).Size(28, 28).Radius(14).Justify(ui.Center).
				AlignItems(ui.Center).Cursor(ui.CursorPointer)
			if selected {
				// 选中态：描边用前景色，套在色块外面。
				hit.Border(2, t.Text)
			}
			hit.Label(accentLabel(choice.Key)).Tooltip(accentLabel(choice.Key))
			hit.Children(func() {
				ui.Box(c).Size(20, 20).Radius(10).Background(swatch)
			})
			if hit.Clicked() {
				a.settings.Accent = choice.Key
				a.saveSettings()
			}
		}
	})
}
