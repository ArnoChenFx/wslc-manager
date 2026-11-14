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
// 关键取舍：**不用**强调色整行填充，也不返回半透明色。
//
// 半透明色的问题：MyGo 把 CellsStyle.Row 直接当背景填充，
// 是否与下层混合取决于绘制路径；实测在青绿等亮色强调下
// 会得到饱和色块，灰色文字压上去几乎看不清（用户截图确认过）。
//
// 所以这里返回一个**不透明的、与背景预混合过的**深色：
// 由背景色与强调色按固定比例调出来，结果可控、可预测，
// 且保证次要文字（TextMuted）仍有足够对比度。
func rowSelection(t *ui.Theme, accent ui.Color) ui.Color {
	// 强调色只占一小部分，主要是让选中行「带一点色彩的暗示」。
	const mix = 0.16
	base := t.Background
	if !t.Dark {
		base = t.Surface
	}
	return base.Mix(accent, mix)
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
	// 让 CellsStyle 真正生效需要两个条件，缺一不可：
	//   1. CellSelect = true
	//   2. Selection != nil
	// MyGo 判定「选单元格模式」的条件是 `CellSelect && Selection != nil`；
	// 少了任何一个，全部退回到 t.Accent 整行填充——
	// 那正是「选中行一大坨饱和色、文字糊在里面」的根源（截图确认过）。
	//
	// All: true 表示整行一起选，而不是逐单元格，
	// 保持和之前一样的行选体验。
	s.CellSelect = true
	// 还需要一个非 nil 的 Selection：MyGo 判定「选单元格模式」的条件是
	// `CellSelect && Selection != nil`，少了它 CellsStyle 不生效，
	// 会退回 t.Accent 整行填充（文字糊成一片的根源）。
	//
	// 这里不引入多选：syncRowSelection 每帧把 Selection 收紧成
	// 当前 Selected 的那一项，对外仍是单选行为。
	// 键类型必须与 ListState.Key 的返回值一致（各页面返回的是字符串 ID），
	// 否则 MyGo 会在运行时 panic：key 类型与 Selection 不匹配。
	if s.Selection == nil {
		s.Selection = &ui.Selection[string]{}
	}

	// 三个颜色分工：
	//   Cell  —— 选中行里的单元格（会盖在 Row 之上）
	//   Row   —— 选中行的其余部分（行间隙、内边距）
	//   CellText —— 单元格文字
	//
	// Cell 必须显式给值。留空时 MyGo 会回退到 t.Accent 整片填充，
	// 那正是「选中行变成一大块饱和色、文字糊在里头」的原因
	// （用户截图确认过）。这里令 CellText 与主题前景色一致，
	// 因此 Cell 只需与 Row 稍有区别即可，不必再反色。
	bg := rowSelection(t, t.Accent)
	s.CellsStyle = ui.CellsStyle{
		Cell:     bg,
		Row:      bg,
		CellText: t.Text,
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

// syncRowSelection 让 UI 的多选容器始终只包含当前选中项。
//
// 为什么需要：为了启用 CellsStyle（自定义选中行配色），
// 必须给 ListState.Selection 一个非 nil 值，而它天生是多选的。
// 本应用所有列表都是单选，所以每帧把多余的选择清掉，
// 保证行为与视觉都还是「只选中一行」。
//
// 键取自 ListState.Key，与 Selection 的键类型保持一致，
// 否则 MyGo 会在运行时 panic（"a row's key is a string, not the int"）。
func syncRowSelection(s *ui.ListState, selected int) {
	sel, ok := s.Selection.(*ui.Selection[string])
	if !ok || sel == nil || s.Key == nil {
		return
	}
	sel.Clear()
	if selected < 0 {
		return
	}
	if k, ok := s.Key(selected).(string); ok {
		sel.Add(k)
	}
}

// listTheme 返回一份「专供列表/表格使用」的主题。
//
// 为什么需要：MyGo 的 ui.List 选中行一律用 accent 整片填充
// （见 list.go 的 w.Selected(on)），而 accent 同时也是按钮颜色。
// 按钮需要饱和、选中行需要柔和，两者是冲突的诉求，主题里只有一个字段。
//
// CellsStyle 可以绕过这个限制，但它只对 Table 生效
// （内部要求 f.flat）。Compose 项目列表是 ui.List，用不上。
//
// 所以这里派生一份主题：把 Accent 换成与背景预混合的柔和色，
// 只作用于列表所在子树（ui.Themed）。按钮在别处渲染，不受影响。
//
// 同时把 AccentText 设成正常前景色，使选中行的文字仍然清晰——
// 这也正是用户报告「看不清」的那一半问题。
func listTheme(base *ui.Theme) *ui.Theme {
	t := *base
	t.Accent = rowSelection(base, base.Accent)
	t.AccentHover = t.Accent
	t.AccentPressed = t.Accent
	t.AccentText = base.Text
	// 失焦时不要退回灰色：列表选中项应保持可辨识。
	t.InactiveAccent = t.Accent
	return &t
}
