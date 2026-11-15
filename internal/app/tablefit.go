package app

// 这个文件按内容自动分配表格列宽。
//
// 为什么需要它：MyGo 的 Table 里，Width 固定的列无论内容多宽都占那么多，
// 只有 Width=0 的列才“分剩下的空间”。以前所有列都写死宽度，而且加起来远超表格实际
// 宽度（服务表：4 个固定列占 304，剩给「镜像 / 构建」的只有 97，而内容要 263），
// 结果长列被压成一条缝、窄列的表头还被截断（截图里的「副…」）。
//
// 这里改成：先用 ui.Context.MeasureText 按真实字体量出每列需要多宽（表头 + 最长内容），
// 再按表格实际宽度分配——
//   - 空间够：每列都拿到它需要的宽度，富余的按各列需求等比例摊开（不让某一列吸干全部，
//     否则宽窗口下表格会一边空得离谱）；
//   - 空间不够：先保底（不低于表头文字），剩下的按“离满足还差多少”等比例分，
//     让各列同时接近完整，而不是先把某一列压死；
//   - 空间不够且有些列整列都没有信息（全是「—」「1」或空）：先收起这些列，
//     把宽度让给有内容的列。一个项目里副本数全是默认的 1、也没有 depends_on 时，
//     为了给它们留位置而把镜像名截成「open-comp…」是本末倒置。
//
// 用户手动拖过的列宽始终优先：MyGo 把它存在 ListState.Columns.Widths 里，
// 引擎取值时先看用户的、再看这里算出来的（table.go 的 TableLayout.width）。

import (
	"math"
	"sync"

	"github.com/egoist/mygo/ui"
)

const (
	// tableCellPadding 是表格单元格左右内边距之和（引擎里是 Space(2.5)*2，主字号下约 20）。
	// 用实测值：列宽 = 文字宽 + 20 时刚好不截断（「副本数」42 → 62 才完整）。
	tableCellPadding = 20
	// tableWidthSafety 是每列额外留的一点余量，避免字体渲染的亚像素误差把最后一个字挤掉。
	tableWidthSafety = 4
	// tableNeedRowLimit 是最多量多少行。行数很多时逐行测量没有意义：
	// 持有上千个容器的用户本来就要靠拖动、搜索来用表，宽度按前若干行算就足够了。
	tableNeedRowLimit = 200
)

// autoTable 描述一张要自动分配列宽的表。
type autoTable struct {
	// Titles 是各列的表头（同时作为列 ID）。
	Titles []string
	// Floors 是各列的最小宽度（0 表示只要放得下表头）。
	Floors []float32
	// Aligns 是各列的对齐方式，可为 nil。
	Aligns []ui.Align
	// Extra 是每列除了文字以外还要占的固定宽度，例如名称列前面的状态圆点。
	Extra []float32
	// DropTrivial 为真时，空间不够就收起“整列没信息”的列（见 allTrivial）。
	DropTrivial bool
	// Grid 是各行的文字，Grid[row][col]；缺的列按空处理。
	Grid [][]string
}

// buildColumns 量出各列需要的宽度，并据此生成表格列。
//
// 返回的 keep 是“保留的原始列下标”：空间不够时会收起整列无信息的列，
// 此时返回的列比 Titles 少，渲染时必须用 keep[col] 去取原来的列。
//
// avail <= 0（第一帧还量不到表格宽度）时两个返回值都是 nil，调用方用写死的默认列宽先画一帧。
func buildColumns(c *ui.Context, avail float32, t autoTable) (cols []ui.TableColumn, keep []int) {
	if avail <= 0 || len(t.Titles) == 0 {
		return nil, nil
	}

	need := make([]float32, len(t.Titles))
	floor := make([]float32, len(t.Titles))
	for i, title := range t.Titles {
		// 表头用 600 字重：引擎就是这么画的，用常规字重量会偏窄。
		headerW := measureTextWidth(c, title, 600)
		w := headerW
		for r := 0; r < len(t.Grid) && r < tableNeedRowLimit; r++ {
			if i < len(t.Grid[r]) {
				if cw := measureTextWidth(c, t.Grid[r][i], 0); cw > w {
					w = cw
				}
			}
		}
		var extra float32
		if i < len(t.Extra) {
			extra = t.Extra[i]
		}
		need[i] = float32(math.Ceil(float64(w + extra + tableCellPadding + tableWidthSafety)))

		f := float32(math.Ceil(float64(headerW + extra + tableCellPadding + tableWidthSafety)))
		if i < len(t.Floors) && t.Floors[i] > f {
			f = t.Floors[i]
		}
		floor[i] = f
	}

	// 空间不够时，收起“整列都没信息”的列，把宽度让给有内容的列。
	kept := make([]int, 0, len(t.Titles))
	for i := range t.Titles {
		kept = append(kept, i)
	}
	if t.DropTrivial && sumWidths(need) > avail {
		filtered := kept[:0]
		for _, i := range kept {
			if i == 0 || !t.allTrivial(i) {
				filtered = append(filtered, i)
			}
		}
		kept = filtered
	}

	titles := make([]string, len(kept))
	n2 := make([]float32, len(kept))
	f2 := make([]float32, len(kept))
	aligns := make([]ui.Align, len(kept))
	for k, i := range kept {
		titles[k], n2[k], f2[k] = t.Titles[i], need[i], floor[i]
		if i < len(t.Aligns) {
			aligns[k] = t.Aligns[i]
		}
	}
	return allocColumns(avail, titles, n2, f2, aligns), kept
}

// allocWidths 把各列的需求（need）和保底（floor）分配成具体宽度，总和尽量等于 avail。
//
// 纯函数，不碰 UI，便于表驱动测试。结果与 avail、need、floor 一一对应，
// 且每项不小于 floor（除非连保底都放不下）。
func allocWidths(avail float32, need, floor []float32) []float32 {
	n := len(need)
	out := make([]float32, n)
	if n == 0 || avail <= 0 {
		return out
	}

	var sumNeed, sumFloor float32
	for i := 0; i < n; i++ {
		if floor[i] > need[i] {
			floor[i] = need[i] // 保底不该超过需求
		}
		sumNeed += need[i]
		sumFloor += floor[i]
	}
	if sumNeed <= 0 {
		return out
	}

	switch {
	case sumNeed <= avail:
		// 空间足够：每列按自己的内容宽度等比放大。
		//
		// 这跟浏览器表格的自动布局一个道理：富余空间按各列的“分量”分，内容多的列
		// 拿得多（长名字/长镜像名因此能完整显示），内容少的列也跟着略宽，
		// 整体比例不变。若把富余全给某一列，宽窗口下表格会一边空得离谱。
		extra := avail - sumNeed
		for i := 0; i < n; i++ {
			out[i] = need[i] + extra*need[i]/sumNeed
		}
	case sumFloor <= avail:
		// 空间不够：先保底，剩下的按“离满足还差多少”等比例分。
		extra := avail - sumFloor
		var deficit float32
		for i := 0; i < n; i++ {
			deficit += need[i] - floor[i]
		}
		for i := 0; i < n; i++ {
			out[i] = floor[i]
			if deficit > 0 {
				out[i] += extra * (need[i] - floor[i]) / deficit
			}
		}
	default:
		// 连保底都放不下（窗口窄得离谱）：等比压缩保底值，让每列都还留一点。
		k := avail / sumFloor
		for i := 0; i < n; i++ {
			out[i] = floor[i] * k
		}
	}
	return out
}

// allocColumns 是 allocWidths 的表格版本：按列生成 ui.TableColumn，宽度取整到 DIP。
func allocColumns(avail float32, titles []string, need, floor []float32, aligns []ui.Align) []ui.TableColumn {
	widths := allocWidths(avail, need, floor)
	cols := make([]ui.TableColumn, len(titles))
	for i, title := range titles {
		var align ui.Align
		if i < len(aligns) {
			align = aligns[i]
		}
		w := float32(math.Floor(float64(widths[i])))
		minW := float32(math.Floor(float64(floor[i])))
		if minW < 16 {
			minW = 16
		}
		// 宽度取整后不能低于保底，否则表头又会被截断。
		if w < minW {
			w = minW
		}
		cols[i] = ui.TableColumn{Title: title, Width: w, MinWidth: minW, Align: align}
	}
	return cols
}

// sumWidths 求一片 float32 的和。
func sumWidths(v []float32) float32 {
	var s float32
	for _, x := range v {
		s += x
	}
	return s
}

// allTrivial 报告某一列是否整列都没有信息：空、「—」，或者全是默认的 1。
//
// 只看前 200 行（和量宽度一致）；空表时当作有信息，不收起——
// 空表只剩一半表头比整张表头更奇怪。
func (t autoTable) allTrivial(col int) bool {
	if len(t.Grid) == 0 {
		return false
	}
	for r := 0; r < len(t.Grid) && r < tableNeedRowLimit; r++ {
		if col >= len(t.Grid[r]) {
			continue
		}
		if !trivialCell(t.Grid[r][col]) {
			return false
		}
	}
	return true
}

// trivialCell 报告一个单元格是不是“没信息”：空、「—」，或默认值 1。
func trivialCell(s string) bool {
	switch s {
	case "", "—", "1":
		return true
	}
	return false
}

// ---- 文字测量缓存 ----

var textWidthCache = struct {
	sync.Mutex
	m map[string]float32
}{m: map[string]float32{}}

// measureTextWidth 量一段文字在当前主题字体下的宽度。
//
// 带缓存：同一个字符串每帧都会再量一次（表格每帧重画），实测单次约 1 微秒，
// 两千个单元格一帧就要 2 毫秒；缓存命中后几乎免费。
//
// 键里带上字重与语言：字体与字号由主题决定，表格都用默认值，只有表头用 600 字重。
func measureTextWidth(c *ui.Context, s string, weight int) float32 {
	if s == "" || c == nil {
		return 0
	}
	key := string(rune('0'+weight%10)) + "|" + c.Locale() + "|" + s
	textWidthCache.Lock()
	if w, ok := textWidthCache.m[key]; ok {
		textWidthCache.Unlock()
		return w
	}
	textWidthCache.Unlock()

	w, _ := c.MeasureText(0, ui.Span{Text: s, Weight: weight})

	textWidthCache.Lock()
	if len(textWidthCache.m) > 8192 { // 防止删容器、换镜像后无限增长
		textWidthCache.m = map[string]float32{}
	}
	textWidthCache.m[key] = w
	textWidthCache.Unlock()
	return w
}

// tableWidth 返回一张表上一帧的实际宽度；还量不到时返回 0 并请求再画一帧。
//
// 不能自己拿窗口宽度去减：表格在分栏里、还可能被父容器留了内边距，
// 减错了就会算多，算多又会把最后一列挤出可见范围。直接问引擎最准。
func tableWidth(c *ui.Context, h *ui.Handle) float32 {
	if h == nil {
		return 0
	}
	w := h.Bounds(c).W
	if w <= 0 {
		// 第一帧还没有布局结果：请求再画一帧，下次就能量到。
		c.Invalidate()
	}
	return w
}
