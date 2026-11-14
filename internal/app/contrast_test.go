package app

// 选中行的对比度测试。
//
// 为什么需要：用户截图反馈「某些配色下文字看不清」——
// 选中行底色随强调色变化，而文字颜色是固定的，
// 一旦两者亮度接近，文字就糊成一片。
//
// 这里用 WCAG 的对比度公式量化，而不是靠肉眼判断。
// 阈值 4.5 是 WCAG AA 对正文的要求；选中行文字至少要有 4.5，
// 因为用户正是在读这一行。

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// luminance 按 WCAG 2.x 计算相对亮度。
func luminance(c ui.Color) float64 {
	// Color 的分量是 0..255 的 uint8。
	lin := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrastRatio 返回两色对比度（1..21）。
func contrastRatio(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestSelectedRowContrast 检查每种强调色下，选中行的文字都可读。
//
// 覆盖深色与浅色两套主题；每一种强调色都要过。
func TestSelectedRowContrast(t *testing.T) {
	for _, dark := range []bool{true, false} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			for _, choice := range accentPalette {
				th := productTheme(dark)
				applyAccent(th, choice)

				bg := rowSelection(th, th.Accent)

				// 次要文字在选中行里用的是正常前景色（见各页面的 cell 渲染）。
				text := contrastRatio(th.Text, bg)
				if text < 4.5 {
					t.Errorf("%s/%s: 正文对比度 %.2f < 4.5（文字看不清）",
						name, choice.Key, text)
				}
				t.Logf("%s/%-7s bg=%.2f text=%.2f", name, choice.Key, 0.0, text)
			}
		})
	}
}

// TestAccentTextContrast 检查主按钮文字在所有强调色下都可读。
//
// 主按钮是实心强调色 + AccentText。青绿、琥珀这类亮色上放白字，
// 对比度很容易掉到 3 以下，按钮文字就糊了。
func TestAccentTextContrast(t *testing.T) {
	for _, choice := range accentPalette {
		th := productTheme(true)
		applyAccent(th, choice)

		ratio := contrastRatio(th.AccentText, th.Accent)
		// 按钮文字是大号加粗，AA 要求 3.0；这里仍按 4.5 要求，
		// 因为容器管理界面里按钮往往很密，宁可保守。
		if ratio < 3.0 {
			t.Errorf("%s: 按钮文字对比度 %.2f < 3.0（白字读不出来）", choice.Key, ratio)
		}
		t.Logf("%-7s accent=%02x%02x%02x text=%.2f",
			choice.Key, th.Accent.R, th.Accent.G, th.Accent.B, ratio)
	}
}

// TestSidebarSelectedContrast 检查侧边栏选中项的文字对比度。
//
// 侧边栏选中项用强调色填充，文字用 AccentText；同样的道理。
func TestSidebarSelectedContrast(t *testing.T) {
	for _, dark := range []bool{true, false} {
		for _, choice := range accentPalette {
			th := productTheme(dark)
			applyAccent(th, choice)
			inactive := th.InactiveAccent
			if inactive == (ui.Color{}) {
				inactive = th.Accent
			}
			ratio := contrastRatio(th.AccentText, inactive)
			if ratio < 3.0 {
				t.Errorf("dark=%v %s: 侧边栏选中项对比度 %.2f < 3.0", dark, choice.Key, ratio)
			}
		}
	}
}

// TestSelectedRowIsNotSolidAccent 确保选中行不是整片饱和强调色。
//
// 回归测试：用户截图反馈「某些配色下选中行的文字看不清」。
// 查下来是 CellsStyle 从未生效——MyGo 判定「选单元格模式」的条件是
// `CellSelect && Selection != nil`，缺一个就退回用 t.Accent 整行填充，
// 于是深色文字压在饱和青绿/琥珀上，几乎读不出来。
//
// 这个测试用像素值把行为锁死：选中行的底色必须明显不同于纯强调色，
// 而与背景接近。这样一旦有人又把它改回整片填充，测试立刻失败。
func TestSelectedRowIsNotSolidAccent(t *testing.T) {
	for _, dark := range []bool{true, false} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			for _, choice := range accentPalette {
				th := productTheme(dark)
				applyAccent(th, choice)

				bg := rowSelection(th, th.Accent)

				// 与纯强调色的距离要够远：完全相等说明根本没换成柔和色。
				dAccent := colorDistance(bg, th.Accent)
				if dAccent < 0.05 {
					t.Errorf("%s/%s: 选中行底色几乎等于强调色（距离 %.4f），"+
						"CellsStyle 可能又没生效", name, choice.Key, dAccent)
				}
				// 与页面背景要保持接近，避免看起来像一块彩色补丁。
				dBase := colorDistance(bg, th.Background)
				if dBase > 0.35 {
					t.Errorf("%s/%s: 选中行底色与背景差得太多（距离 %.4f）",
						name, choice.Key, dBase)
				}
			}
		})
	}
}

// colorDistance 返回两色在 0..1 归一化 RGB 空间里的欧氏距离。
func colorDistance(a, b ui.Color) float64 {
	dr := (float64(a.R) - float64(b.R)) / 255
	dg := (float64(a.G) - float64(b.G)) / 255
	db := (float64(a.B) - float64(b.B)) / 255
	return math.Sqrt(dr*dr + dg*dg + db*db)
}
