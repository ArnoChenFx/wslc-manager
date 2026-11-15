package app

// 这个文件测「按内容分配表格列宽」（tablefit.go）。
//
// 起点是一个真实截图：服务表里「依赖」「副本数」两列只剩「依赖」和「副…」，
// 「镜像 / 构建」明明内容最长却只分到一小条，其余宽度被几个固定列白占。

import (
	"fmt"
	"math"
	"testing"

	"github.com/arno/wslc-manager/internal/wslc"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/locales"
)

// ---- 纯分配函数 ----

// TestAllocWidths 表驱动测分配规则。
func TestAllocWidths(t *testing.T) {
	near := func(a, b float32) bool { return math.Abs(float64(a-b)) < 1 }

	cases := []struct {
		name         string
		avail        float32
		need, floor  []float32
		wantSum      float32
		wantAtLeast  []float32 // 每列的下限
		wantAtMost   []float32 // 每列的上限（nil 表示不限）
		checkGreater bool      // 为真时要求每列严格大于 floor
	}{
		{
			name: "空间足够：按内容等比放大并填满",
			// 内容 100/200/300（总 600），可用 900 → 各放大 1.5 倍。
			avail: 900, need: []float32{100, 200, 300}, floor: []float32{50, 50, 50},
			wantSum: 900,
			// 换算后整数关系有误差，只校验与本列需求的比例关系。
			checkGreater: true,
		},
		{
			name:  "空间刚好",
			avail: 600, need: []float32{100, 200, 300}, floor: []float32{50, 50, 50},
			wantSum: 600,
		},
		{
			name: "空间不够：保底 + 按缺口分配",
			// 可用 400，保底 150，缺口总量 50+150=200；富余 250 全给缺口。
			avail: 400, need: []float32{100, 200, 300}, floor: []float32{50, 50, 50},
			wantSum:     400,
			wantAtLeast: []float32{50, 50, 50},
		},
		{
			name:  "连保底都放不下：等比压缩保底",
			avail: 100, need: []float32{100, 200, 300}, floor: []float32{50, 50, 100},
			wantSum: 100,
		},
		{
			name:  "保底比需求还大时按需求算",
			avail: 200, need: []float32{40, 40}, floor: []float32{100, 100},
			wantSum: 200,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			need := append([]float32(nil), tc.need...)
			floor := append([]float32(nil), tc.floor...)
			got := allocWidths(tc.avail, need, floor)

			var s float32
			for i := range got {
				s += got[i]
				if i < len(tc.wantAtLeast) && got[i] < tc.wantAtLeast[i]-0.01 {
					t.Errorf("第 %d 列 = %.1f，低于保底 %.1f", i, got[i], tc.wantAtLeast[i])
				}
				if i < len(tc.wantAtMost) && tc.wantAtMost != nil && got[i] > tc.wantAtMost[i]+0.01 {
					t.Errorf("第 %d 列 = %.1f，超过上限 %.1f", i, got[i], tc.wantAtMost[i])
				}
			}
			if !near(s, tc.wantSum) {
				t.Errorf("总和 = %.1f，期望 %.1f（应填满可用宽度）", s, tc.wantSum)
			}
			if tc.checkGreater {
				// 空间够时，每一列都应拿到比需求更多的宽度（等比放大）。
				for i := range got {
					if got[i] <= tc.need[i] {
						t.Errorf("空间有余时第 %d 列 = %.1f 不该不大于需求 %.1f", i, got[i], tc.need[i])
					}
				}
			}
		})
	}
}

// TestAllocWidthsProportional 验证“空间够时按内容等比放大”。
//
// 防的是把富余空间只给某一列：宽窗口下那样会让表格一边空出大片，
// 看着像布局没算完。
func TestAllocWidthsProportional(t *testing.T) {
	got := allocWidths(1200, []float32{200, 100, 100}, []float32{50, 50, 50})
	// 三列需求比 2:1:1，放大后也应保持 2:1:1。
	if !(got[0] > got[1] && math.Abs(float64(got[1]-got[2])) < 1) {
		t.Fatalf("没有按内容等比放大: %v", got)
	}
	if math.Abs(float64(got[0]/got[1]-2)) > 0.1 {
		t.Errorf("比例不成 2:1: 第 1 列 %.1f、第 2 列 %.1f", got[0], got[1])
	}
}

func TestAllocWidthsEdgeCases(t *testing.T) {
	if got := allocWidths(0, []float32{10}, []float32{5}); len(got) != 1 || got[0] != 0 {
		t.Errorf("avail=0 应全为 0，得到 %v", got)
	}
	if got := allocWidths(100, nil, nil); len(got) != 0 {
		t.Errorf("没有列时应返回空，得到 %v", got)
	}
}

func TestTrivialCell(t *testing.T) {
	for _, s := range []string{"", "—", "1"} {
		if !trivialCell(s) {
			t.Errorf("%q 应算没信息", s)
		}
	}
	for _, s := range []string{"0", "2", "db", " ", "1.5"} {
		if trivialCell(s) {
			t.Errorf("%q 不该算没信息", s)
		}
	}
}

// ---- 表格几何 ----

// tableMeasure 是探针在界面里量到的文字宽度（单位与列宽一致，均为 DIP）。
type tableMeasure struct {
	header map[string]float32 // 表头文字宽度（600 字重）
	text   map[string]float32 // 普通文字宽度
}

// measureIn 生成一个在构建期量文字宽度的视图包装。
func measureIn(view func(*ui.Context), titles, texts []string) (func(*ui.Context), *tableMeasure) {
	m := &tableMeasure{header: map[string]float32{}, text: map[string]float32{}}
	return func(c *ui.Context) {
		view(c)
		for _, s := range titles {
			if _, ok := m.header[s]; !ok {
				w, _ := c.MeasureText(0, ui.Span{Text: s, Weight: 600})
				m.header[s] = w
			}
		}
		for _, s := range texts {
			if _, ok := m.text[s]; !ok {
				w, _ := c.MeasureText(0, ui.Span{Text: s})
				m.text[s] = w
			}
		}
	}, m
}

// headerWidths 收集各列表头的宽度，以及表格自身的宽度。
func headerWidths(tt *ui.Tester, tableLabel string) (map[string]float32, float32) {
	out := map[string]float32{}
	var tableW float32
	for _, info := range tt.AccessAll() {
		switch info.Role {
		case ui.RoleColumnHeader:
			out[info.Name] = info.Bounds.W
		case ui.RoleTable:
			if tableLabel == "" || info.Name == tableLabel {
				tableW = info.Bounds.W
			}
		}
	}
	return out, tableW
}

// assertHeadersFit 断言每个看得见的表头都没有被截断。
//
// 这是这个功能最直接的验收条件：截图里坏掉的就是「副…」。
func assertHeadersFit(t *testing.T, where string, widths map[string]float32, m *tableMeasure) {
	t.Helper()
	if len(widths) == 0 {
		t.Fatalf("%s：没有找到任何列头", where)
	}
	for title, w := range widths {
		need := m.header[title] + tableCellPadding
		if w+0.5 < need {
			t.Errorf("%s：列头 %q 宽 %.0f，放不下 %.0f 的文字（被截断）", where, title, w, m.header[title])
		}
	}
}

// assertFillsTable 断言列宽之和基本等于表格宽度：既没有溢出，也没有在右边留一条空。
func assertFillsTable(t *testing.T, where string, widths map[string]float32, tableW float32) {
	t.Helper()
	var s float32
	for _, w := range widths {
		s += w
	}
	if tableW > 0 && s > tableW+1 {
		t.Errorf("%s：列宽之和 %.0f 超出表格宽度 %.0f", where, s, tableW)
	}
	if tableW > 0 && s < tableW-16 {
		t.Errorf("%s：列宽之和 %.0f 比表格宽度 %.0f 少太多，右边会空出一条", where, s, tableW)
	}
}

// composeTestProject 造一个有内容可量的项目：长镜像名 + 构建标记。
func composeTestProject() compose.Project {
	return compose.Project{
		Dir: `E:\P`, Name: "open-compute",
		Config: compose.ProjectConfig{
			Name: "open-compute", Directory: `E:\P`,
			Services: map[string]compose.ServiceConfig{"ocd": {
				Name: "ocd", Image: "open-compute/ocd:v0.4.0",
				Build: &compose.BuildConfig{Context: "."},
				Ports: []compose.PortMapping{{Published: "8787", Target: 8787}},
			}},
		},
	}
}

// TestServiceTableHeadersNotClipped 是截图那个场景的回归测试。
//
// 回归测试：列宽写死时，「依赖」72 < 它表头需要 52+内边距，「副本数」40 < 66，
// 于是表头被截成「副…」；而内容最长的「镜像 / 构建」只分到 97（内容要 263）。
func TestServiceTableHeadersNotClipped(t *testing.T) {
	titles := []string{
		locales.ComposeServices(), locales.ContainersImage(), locales.CommonPort(),
		locales.CommonDependsOn(), locales.CommonReplicas(),
	}
	const long = "open-compute/ocd:v0.4.0（本地构建）"

	for _, listW := range []float32{401, 500, 678} {
		t.Run(fmt.Sprintf("宽%.0f", listW), func(t *testing.T) {
			a := newTestApp(t)
			a.page = PageCompose
			a.projects = []compose.Project{composeTestProject()}
			a.projectSel = 0
			a.composeListWidth = listW

			view, m := measureIn(a.View, titles, []string{long})
			tt := ui.NewTester(view, 1180, 760)
			tt.Frame()

			widths, tableW := headerWidths(tt, locales.ComposeServiceList())
			where := fmt.Sprintf("服务表 %v 宽", listW)
			assertHeadersFit(t, where, widths, m)
			assertFillsTable(t, where, widths, tableW)

			// 内容最长的镜像列必须拿到最多宽度（不能像以前那样只剩 97）。
			for title, w := range widths {
				if title != locales.ContainersImage() && w > widths[locales.ContainersImage()] {
					t.Errorf("%s：%q 宽 %.0f 超过了镜像列的 %.0f", where, title, w, widths[locales.ContainersImage()])
				}
			}

			// 401 时空间确实不够，镜像文字只能显示一部分（不能苛求）；678 必须完整显示。
			r, ok := tt.Find(long)
			if !ok {
				t.Fatalf("%s：找不到镜像单元格", where)
			}
			if listW >= 678 && r.W+0.5 < m.text[long] {
				t.Errorf("%s：镜像文字被截断（%.0f < %.0f）", where, r.W, m.text[long])
			}
			if listW >= 401 && r.W < m.text[long]*0.7 {
				t.Errorf("%s：镜像列只显示 %.0f/%.0f，分得太少", where, r.W, m.text[long])
			}
		})
	}
}

// TestServiceTableDropsEmptyColumnsWhenTight 验证空间紧张时收起“整列没信息”的列。
//
// 「副本数」全是默认的 1、也没有 depends_on 时，那两列占着 110 多的宽度，
// 却让内容最长的镜像列显示不全——该让位。反过来，内容真有值时必须保留这两列。
func TestServiceTableDropsEmptyColumnsWhenTight(t *testing.T) {
	depends := locales.CommonDependsOn()
	replicas := locales.CommonReplicas()

	t.Run("没信息时收起", func(t *testing.T) {
		a := newTestApp(t)
		a.page = PageCompose
		a.projects = []compose.Project{composeTestProject()}
		a.projectSel = 0
		a.composeListWidth = 401 // 紧张：五列放不下

		tt := ui.NewTester(a.View, 1180, 760)
		tt.Frame()
		widths, tableW := headerWidths(tt, locales.ComposeServiceList())
		if _, ok := widths[depends]; ok {
			t.Errorf("空间不够且整列都是「—」时，应收起 %q", depends)
		}
		if _, ok := widths[replicas]; ok {
			t.Errorf("空间不够且整列都是默认的 1 时，应收起 %q", replicas)
		}
		// 收起之后省下来的宽度应留给有内容的列。
		if w := widths[locales.ContainersImage()]; w < 200 {
			t.Errorf("收起两列后镜像列应拿到更多宽度，实际 %.0f", w)
		}
		_ = tableW
	})

	t.Run("有内容时保留", func(t *testing.T) {
		p := composeTestProject()
		svc := p.Config.Services["ocd"]
		svc.DependsOn = []string{"db"}
		svc.Replicas = 3
		p.Config.Services["ocd"] = svc

		a := newTestApp(t)
		a.page = PageCompose
		a.projects = []compose.Project{p}
		a.projectSel = 0
		a.composeListWidth = 401

		view, m := measureIn(a.View, []string{depends, replicas}, nil)
		tt := ui.NewTester(view, 1180, 760)
		tt.Frame()
		// 空间紧张但这两列有真内容：不仅不能收起，表头也不能被截断。
		widths, tableW := headerWidths(tt, locales.ComposeServiceList())
		assertHeadersFit(t, "紧张但有内容", widths, m)
		assertFillsTable(t, "紧张但有内容", widths, tableW)
		if _, ok := widths[depends]; !ok {
			t.Error("depends_on 有值，不该收起这一列")
		}
		if _, ok := widths[replicas]; !ok {
			t.Error("副本数是 3，不该收起这一列")
		}
	})

	t.Run("空间充足时都保留", func(t *testing.T) {
		a := newTestApp(t)
		a.page = PageCompose
		a.projects = []compose.Project{composeTestProject()}
		a.projectSel = 0
		a.composeListWidth = 678 // 放得下

		tt := ui.NewTester(a.View, 1180, 760)
		tt.Frame()
		widths, _ := headerWidths(tt, locales.ComposeServiceList())
		if len(widths) != 5 {
			t.Errorf("空间够时应保留全部 5 列，实际 %d 列: %v", len(widths), widths)
		}
	})
}

// TestAllTableHeadersNotClipped 检查其余四张表在各种窗口宽度下也有同样的保证。
func TestAllTableHeadersNotClipped(t *testing.T) {
	type table struct {
		page   Page
		label  string
		titles []string
		find   []string // 额外要用来量宽度的单元格文字
	}
	tables := []table{
		{PageContainers, locales.ContainersTitle(),
			[]string{locales.ContainersName(), locales.CommonStatus(), locales.NavImages(), locales.CommonPort(), locales.ComposeProjects()},
			[]string{"nginx:alpine", "0.0.0.0:8080->80/tcp"}},
		{PageImages, locales.ImagesTitle(),
			[]string{locales.CommonRepository(), locales.CommonTags(), locales.ImagesId(), locales.CommonSize(), locales.CommonCreate(), locales.NavContainers()},
			[]string{"nginx"}},
		{PageVolumes, locales.VolumesTitle(),
			[]string{locales.CommonName(), locales.CommonDriver(), locales.VolumesMountPoint()},
			[]string{"demo_data"}},
		{PageNetworks, locales.NetworksTitle(),
			[]string{locales.CommonName(), locales.CommonDriver(), locales.NetworksId(), locales.CommonType(), locales.ImagesCreated()},
			[]string{"bridge"}},
	}
	for _, tb := range tables {
		for _, win := range []int{900, 1180, 1600} {
			t.Run(fmt.Sprintf("%d/窗口%d", tb.page, win), func(t *testing.T) {
				a := newTestApp(t)
				sampleData(a)
				a.page = tb.page
				view, m := measureIn(a.View, tb.titles, tb.find)
				tt := ui.NewTester(view, win, 760)
				tt.Frame()

				widths, tableW := headerWidths(tt, tb.label)
				if len(widths) < len(tb.titles)-1 {
					t.Fatalf("只找到 %d 列（期望接近 %d）: %v", len(widths), len(tb.titles), widths)
				}
				where := fmt.Sprintf("页面 %d、窗口 %d", tb.page, win)
				assertHeadersFit(t, where, widths, m)
				assertFillsTable(t, where, widths, tableW)
			})
		}
	}
}

// TestTableWidthsFromPreviousFrame 验证第一帧量不到宽度时先按默认值画，第二帧就按内容分配。
//
// 表格宽度来自上一帧的布局结果（tableWidth）；若第一帧就卡住（不请求重画），
// 表格会一直停在写死的旧列宽上——那正是这个功能要修的东西。
func TestTableWidthsFromPreviousFrame(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame() // settle 会把构建期请求的重绘都画完
	widths, _ := headerWidths(tt, locales.ContainersTitle())
	if len(widths) == 0 {
		t.Fatal("没有找到列头")
	}
	// 写死的旧列宽里，名称列是 190、端口 160；按内容分配后不该还全是那几个数。
	same := widths[locales.ContainersName()] == 190 && widths[locales.CommonPort()] == 160 &&
		widths[locales.NavImages()] == 220
	if same {
		t.Errorf("列宽仍是写死的旧值 %v，说明没有按内容重新分配", widths)
	}
}

// TestUserColumnWidthWins 验证用户手动拖过的列宽优先于按内容算出来的值。
//
// MyGo 把用户拖出来的宽度记在 ListState.Columns.Widths 里，引擎取值时先看它。
// 如果这里算出来的宽度把它盖掉了，用户每次拖动都会被弹回去。
func TestUserColumnWidthWins(t *testing.T) {
	a := newTestApp(t)
	a.page = PageCompose
	a.projects = []compose.Project{composeTestProject()}
	a.projectSel = 0
	a.composeListWidth = 678

	port := locales.CommonPort()
	a.serviceList.Columns.Widths = map[string]float32{port: 210}

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()
	widths, _ := headerWidths(tt, locales.ComposeServiceList())
	if got := widths[port]; math.Abs(float64(got-210)) > 0.5 {
		t.Errorf("用户拖出来的 %q 列宽是 %.1f，被算出来的值盖掉了", port, got)
	}
}

// TestBuildColumnsHeaderFloor 直接测「按内容分配」的保底逻辑。
//
// 用一个西文表头（粗体明显比常规宽）和很短的单元格：这种表头只能靠保底保证完整，
// 一旦保底不看表头文字（或表头按常规字重量），列宽就会小于表头所需，表头被截。
// 中文表头粗体与常规同宽，测不出这个区别，所以这里特意用西文。
func TestBuildColumnsHeaderFloor(t *testing.T) {
	const header = "Ports"
	const cell = "1"
	var got []ui.TableColumn
	var keep []int
	var headerW, cellW float32
	view := func(c *ui.Context) {
		headerW, _ = c.MeasureText(0, ui.Span{Text: header, Weight: 600})
		cellW, _ = c.MeasureText(0, ui.Span{Text: cell})
		// 表格只有 120 宽，两列放不下：必须靠保底撑住表头。
		got, keep = buildColumns(c, 120, autoTable{
			Titles: []string{"Ports", "Tag"},
			Floors: []float32{0, 0},
			Grid:   [][]string{{cell, cell}},
		})
	}
	ui.NewTester(view, 400, 200).Frame()

	if len(got) != 2 || len(keep) != 2 {
		t.Fatalf("应得到 2 列，实际 %d（keep %v）", len(got), keep)
	}
	for i, col := range got {
		if want := headerW + tableCellPadding; col.Width+0.5 < want {
			t.Errorf("第 %d 列宽 %.1f，放不下粗体表头 %.1f + 内边距", i, col.Width, headerW)
		}
	}
	_ = cellW
}

// TestBuildColumnsDroppedColumnKeepsContent 验证收起列之后，剩下的列显示的仍是自己的内容。
//
// 防的是「收起中间一列后，后面的列错位一格」：buildColumns 返回的 keep 把表格列号
// 映回原始列号，渲染必须照它取字。这里让「端口」整列都是「—」（服务没发布端口），
// 空间又紧张，于是它被收起；剩下的「依赖」「副本数」必须还显示自己的值。
func TestBuildColumnsDroppedColumnKeepsContent(t *testing.T) {
	p := compose.Project{
		Dir: `E:\P`, Name: "p",
		Config: compose.ProjectConfig{
			Name: "p", Directory: `E:\P`,
			Services: map[string]compose.ServiceConfig{"ocd": {
				Name: "ocd", Image: "open-compute/ocd:v0.4.0",
				DependsOn: []string{"db"}, Replicas: 3,
				// 故意没有任何端口
			}},
		},
	}
	a := newTestApp(t)
	a.page = PageCompose
	a.projects = []compose.Project{p}
	a.projectSel = 0
	a.composeListWidth = 401

	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()

	widths, _ := headerWidths(tt, locales.ComposeServiceList())
	if _, ok := widths[locales.CommonPort()]; ok {
		t.Errorf("端口整列都是「—」，空间紧张时应被收起：%v", widths)
	}
	// 收起中间的列后，后面两列的内容不能错位。
	if !tt.HasText("db") {
		t.Error("收起端口列后，「依赖」应显示 db")
	}
	if !tt.HasText("3") {
		t.Error("收起端口列后，「副本数」应显示 3")
	}
}

// TestMeasureTextWidthIsStableAndCached 验证测量结果稳定，且缓存不会把不同文字搞混。
//
// 实测这套字体下 600 字重与常规同宽（英文 "Ports" 两者都是 32.75），
// 所以这里不断言字重差异，只保证：同一段文字反复量得到同一个值（缓存命中不会漂），
// 不同文字量出不同的值（缓存没串）。
func TestMeasureTextWidthIsStableAndCached(t *testing.T) {
	var first, again, other float32
	view := func(c *ui.Context) {
		first = measureTextWidth(c, "Ports", 600)
		again = measureTextWidth(c, "Ports", 600)
		other = measureTextWidth(c, "Authorization", 0)
	}
	ui.NewTester(view, 400, 200).Frame()
	if first != again {
		t.Errorf("同一段文字两次量得不一样：%.2f / %.2f", first, again)
	}
	if other <= first {
		t.Errorf("更长文字应更宽：Ports %.2f、Authorization %.2f", first, other)
	}
}

// TestVolumeTableUsesContentWidths 验证卷表也按内容分配（别的表都是几何断言，这张漏过）。
//
// 回归意义：卷表的「挂载点」最长，而旧代码给「名称」写死 280、
// 挂载点只分到剩下的零头。这里要求挂载点列拿到最多宽度，且列宽和填满表格。
func TestVolumeTableUsesContentWidths(t *testing.T) {
	a := newTestApp(t)
	sampleData(a)
	a.page = PageVolumes
	// 造一个很长的挂载点，让「按内容分配」和「写死 280」明显不同。
	// 只留一条卷：两条同名卷会撞上表格的行键。
	a.volumes = []wslc.Volume{{
		Name: "demo_data", Driver: "guest",
		Mountpoint: "/var/lib/docker/volumes/demo_data/_data/very/deep/nested/path/for/testing",
	}}

	titles := []string{locales.CommonName(), locales.CommonDriver(), locales.VolumesMountPoint()}
	view, m := measureIn(a.View, titles, nil)
	tt := ui.NewTester(view, 1180, 760)
	tt.Frame()

	widths, tableW := headerWidths(tt, locales.VolumesTitle())
	assertHeadersFit(t, "卷表", widths, m)
	assertFillsTable(t, "卷表", widths, tableW)

	mount := widths[locales.VolumesMountPoint()]
	name := widths[locales.CommonName()]
	if mount <= name {
		t.Errorf("挂载点列宽 %.0f 应大于名称列的 %.0f（挂载点内容最长）", mount, name)
	}
}

// TestTableWidthRequestsRedrawWhenUnavailable 直接测 tableWidth：量不到宽度时要请求重画。
//
// 表格宽度取自上一帧；第一帧量不到时若没人请求重画，表格就永远停在写死的旧列宽上，
// 「按内容分配」等于没生效。Tester 会把请求的重绘一直画到静止，所以只能这样测：
// 让视图在量完之后自己标记下来，再看那一帧结束时 Tester 是否仍在等重画。
func TestTableWidthRequestsRedrawWhenUnavailable(t *testing.T) {
	var first float64
	var pendingAfterFrame bool
	frame := 0
	view := func(c *ui.Context) {
		frame++
		// 每次构建都用一个新的句柄：它没有被绑定过，Bounds 为 0，正好模拟「还没有布局结果」。
		var h ui.Handle
		ui.Box(c).Bind(&h)
		w := tableWidth(c, &h)
		if frame == 1 {
			first = float64(w)
		}
	}
	tt := ui.NewTester(view, 300, 200)
	// 只画一帧：Frame() 会一直画到静止，看不出「请求了重画」。
	tt.Frame()
	pendingAfterFrame = tt.FramePending()

	if first != 0 {
		t.Errorf("没有布局过的表格宽度应为 0，实际 %v", first)
	}
	if !pendingAfterFrame {
		t.Error("量不到宽度时没有请求重画，表格会永远停在写死的旧列宽")
	}
}

// TestColumnWidthsFollowAvailableSpace 验证列宽随可用宽度变化，而不是写死的默认值。
//
// 回归意义：只看「长列最宽」抓不住「某张表忘了改成按内容分配」——
// 卷表写死名称 280、挂载点吃掉剩下的宽度，挂载点照样最宽，断言照样通过（变异验证抓到过）。
// 而写死的列宽有个特征：窗口变宽变窄它都不变。按内容分配的列宽一定跟着可用宽度走。
// 所以比较同一张表在两个窗口宽度下的同一列宽度。
func TestColumnWidthsFollowAvailableSpace(t *testing.T) {
	measure := func(t *testing.T, page Page, setup func(*App), label, col string) (float32, float32) {
		t.Helper()
		widthAt := func(win int) float32 {
			a := newTestApp(t)
			setup(a)
			a.page = page
			tt := ui.NewTester(a.View, win, 760)
			tt.Frame()
			widths, _ := headerWidths(tt, label)
			w, ok := widths[col]
			if !ok {
				t.Fatalf("窗口 %d 下找不到列 %q: %v", win, col, widths)
			}
			return w
		}
		return widthAt(900), widthAt(1600)
	}

	volumes := func(a *App) {
		a.volumes = []wslc.Volume{{
			Name: "d", Driver: "guest",
			Mountpoint: "/var/lib/docker/volumes/x/_data/very/deep/nested/path",
		}}
	}
	containers := func(a *App) {
		a.containers = []wslc.Container{
			{ID: "c1", Names: "w", Image: "nginx:alpine", State: "running", Status: "Up 1 minute"},
		}
	}

	for _, tc := range []struct {
		name    string
		page    Page
		setup   func(*App)
		label   string
		col     string
		fixedAt float32 // 以前写死的宽度（供错误信息用）
	}{
		{"卷表名称列", PageVolumes, volumes, locales.VolumesTitle(), locales.CommonName(), 280},
		{"容器表端口列", PageContainers, containers, locales.ContainersTitle(), locales.CommonPort(), 160},
		{"容器表名称列", PageContainers, containers, locales.ContainersTitle(), locales.ContainersName(), 190},
	} {
		t.Run(tc.name, func(t *testing.T) {
			narrow, wide := measure(t, tc.page, tc.setup, tc.label, tc.col)
			if narrow == wide {
				t.Errorf("窗口 900 与 1600 下同一列都是 %.0f，说明用的还是写死的 %.0f，没有按内容分配", narrow, tc.fixedAt)
			}
			if wide <= narrow {
				t.Errorf("窗口变宽后该列没有变宽：900 → %.0f，1600 → %.0f", narrow, wide)
			}
		})
	}
}

// TestBuildColumnsNeedsTableWidth 验证拿不到表格宽度时不做分配。
//
// 表格宽度为 0 意味着「还没有布局结果」。此时若照常分配，算出来全是 16 宽，
// 表格会先画成一条挤在一起的样子再跳变；正确的做法是返回 nil、让调用方用写死的列宽先画一帧。
func TestBuildColumnsNeedsTableWidth(t *testing.T) {
	var cols []ui.TableColumn
	var keep []int
	view := func(c *ui.Context) {
		cols, keep = buildColumns(c, 0, autoTable{
			Titles: []string{"Ports", "Tag"},
			Grid:   [][]string{{"1", "2"}},
		})
	}
	ui.NewTester(view, 300, 200).Frame()
	if cols != nil || keep != nil {
		t.Errorf("宽度为 0 时应返回 nil、nil，实际 cols=%v keep=%v", cols, keep)
	}
}
