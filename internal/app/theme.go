package app

// 这个文件定义产品自己的配色与观感。
//
// 为什么不用 MyGo 自带的 DarkTheme：
// 那套是通用默认值（中性灰 + 通用蓝）。本应用需要一套更克制、
// 更适合长时间盯日志的调子，同时保留品牌辨识度。
//
// 设计原则（踩过一次坑之后定下的）：
//   - 强调色只用来「点睛」：主按钮、焦点环、状态点。
//     **不要**让大面积色块（表格选中行、整行高亮）使用满饱和的强调色，
//     那样会显得廉价、刺眼，也无法与内容本身区分主次。
//   - 选中行改用「同色系低透明度」叠加，视觉上像蒙了一层淡淡的蓝，
//     文字仍然是正常前景色，读起来不费劲。
//   - 深色主题背景不要用纯黑，用带一点蓝的深灰，层次更好看。

import (
	"github.com/egoist/mygo/ui"
)

// productTheme 返回本产品使用的主题。
//
// dark 为 true 时用深色，否则用浅色。两者共用同一套强调色，
// 保证切换外观后品牌观感一致。
func productTheme(dark bool) *ui.Theme {
	var t *ui.Theme
	if dark {
		t = ui.DarkTheme()
	} else {
		t = ui.LightTheme()
	}

	t.Radius = 8
	t.Spacing = 4

	if dark {
		// 中性石墨灰：不带蓝调，避免「科技蓝」的廉价感。
		// 背景、卡片、输入框靠明度分层，不靠色相区分。
		t.Background = ui.Hex("#191919")
		t.Surface = ui.Hex("#212121")
		t.SurfaceHover = ui.Hex("#2a2a2a")
		t.SurfacePressed = ui.Hex("#333333")
		t.Border = ui.Hex("#333333")
		t.Text = ui.Hex("#ededed")
		t.TextMuted = ui.Hex("#8a8a8a")

		// 强调色：品红。只用于主按钮、焦点环、选中行，
		// 不大面积铺色，保持「灰底 + 一点亮色」的克制感。
		t.Accent = ui.Hex("#e5357d")
		t.AccentHover = ui.Hex("#f04d90")
		t.AccentPressed = ui.Hex("#c22a68")
		t.AccentText = ui.Hex("#ffffff")

		t.Danger = ui.Hex("#e5534b")
		t.Warning = ui.Hex("#d9a13b")
		t.Success = ui.Hex("#3fb950")

		t.Selection = ui.RGBA(229, 53, 125, 0.30)
		t.Focus = ui.RGBA(240, 77, 144, 0.65)
		t.Scrollbar = ui.RGBA(255, 255, 255, 0.20)
		return t
	}

	// 浅色：中性冷白，配同一个品红强调色。
	t.Background = ui.Hex("#fafafa")
	t.Surface = ui.Hex("#f0f0f0")
	t.SurfaceHover = ui.Hex("#e6e6e6")
	t.SurfacePressed = ui.Hex("#dcdcdc")
	t.Border = ui.Hex("#d6d6d6")
	t.Text = ui.Hex("#1a1a1a")
	t.TextMuted = ui.Hex("#6b6b6b")

	t.Accent = ui.Hex("#d62a70")
	t.AccentHover = ui.Hex("#bc2462")
	t.AccentPressed = ui.Hex("#9e1e52")
	t.AccentText = ui.Hex("#ffffff")

	t.Danger = ui.Hex("#c93c34")
	t.Warning = ui.Hex("#a9741a")
	t.Success = ui.Hex("#227a3a")

	t.Selection = ui.RGBA(214, 42, 112, 0.20)
	t.Focus = ui.RGBA(214, 42, 112, 0.55)
	t.Scrollbar = ui.RGBA(0, 0, 0, 0.20)
	return t
}

// applyTheme 在当前帧应用产品主题。
//
// 外观（深/浅）跟随系统；MyGo 的 Theme 会在系统切换时重建，
// 这里根据已有主题的 Dark 标记选择同一外观下的产品配色。
func (a *App) applyTheme(c *ui.Context) {
	current := c.Theme()
	dark := current != nil && current.Dark
	th := productTheme(dark)
	// 用户选的强调色覆盖默认值；色板只改「点睛」色，不动背景与文字。
	applyAccent(th, accentByKey(a.settings.Accent))
	c.SetTheme(th)
}

// rowSelection 返回列表/表格「选中行」的底色。
//
// 关键取舍：**不用**强调色整行填充。
// 满饱和的蓝色铺满一整行时，文字可读性下降，整屏看起来像被涂色块，
// 而且会与真正需要突出的元素（主按钮、错误提示）抢注意力。
// 这里改用强调色的低透明度叠加：像蒙了一层薄雾，
// 眼睛能立刻找到选中行，但文字仍然是普通前景色。
func rowSelection(t *ui.Theme) ui.Color {
	if t.Dark {
		// 品红的低透明叠加：像蒙了一层淡粉色，
		// 一眼能看出选中，又不至于抢走文字的对比度。
		return ui.RGBA(229, 53, 125, 0.26)
	}
	return ui.RGBA(214, 42, 112, 0.13)
}

// cursorBorder 返回表格里「光标所在格」的描边色。
//
// 比选中底色稍强，用来区分「选中了这一行」和「光标在这一格」。
func cursorBorder(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(240, 120, 170, 0.85)
	}
	return ui.RGBA(198, 36, 100, 0.75)
}

// styleRows 给列表状态套上本产品的行配色。
//
// 每个列表（容器/镜像/卷/网络/项目/服务）都在自己的构建函数里调用它，
// 而不是在 New 里统一设置：主题可能随系统外观变化，配色需要在
// 每一帧按当前主题重算，否则切换深/浅色后选中行颜色会留在旧值。
func styleRows(s *ui.ListState, t *ui.Theme) {
	s.CellsStyle = ui.CellsStyle{
		// Cell 留空：让「光标所在格」沿用选中行底色，只靠描边区分。
		Row: rowSelection(t),
		// 斑马纹：极淡，只用来帮助横向对齐，不喧宾夺主。
		Stripe: stripeFill(t),
	}
}

// stripeFill 返回斑马纹的底色。
func stripeFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.022)
	}
	return ui.RGBA(0, 0, 0, 0.016)
}
