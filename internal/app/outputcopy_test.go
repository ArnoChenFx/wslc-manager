package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestOutputTextCopiesAcrossLines 验证日志面板里可以跨行选择并复制。
//
// 回归测试：日志面板曾经被拆成「每行一个 Text」，并且每个 Text 各自
// Selectable。MyGo 里单个 Text 上的选区只停留在该段落内，于是选区被切成
// 一行一块：从第一行拖到最后一行、Ctrl+A，复制出来的都只有第一行，
// 用户反馈「批量复制都复制不了了」。
//
// 整段输出放回同一个 Text 之后，拖选和全选都应该拿到完整的多行文本。
// 前两项只看复制结果、不依赖日志是几个 Text；
// 第三项要按整段文本的外框换算行位置，所以同时钉住「日志放在同一个 Text 里」
// 这个结构——这是用户明确要求的做法，不要再拆回逐行渲染。
func TestOutputTextCopiesAcrossLines(t *testing.T) {
	lines := []string{
		"2026-11-14 10:00:01 starting",
		"2026-11-14 10:00:02 listening on :8080",
		"",                            // 空行是有意义的（分隔不同阶段），复制时必须保留
		"  indented line with spaces", // 行首缩进也不能丢
		"2026-11-14 10:00:04 ready",
		"ZZZ-LAST",
	}
	full := strings.Join(lines, "\n")

	a := newTestApp(t)
	sampleData(a)
	a.page = PageContainers
	a.outputOpen = true
	a.outputTitle = "日志 · ocd"
	a.outputPercent = 50
	a.outputText = full

	const W, H = 1180, 760
	tt := ui.NewTester(a.View, W, H)
	tt.Frame()
	vp := outputScrollViewport(t, tt)

	// 1. 从第一行开头一路拖到最后一行下面的空白处：应整段选中。
	tt.SetClipboard("")
	tt.Press(vp.X+13, vp.Y+10)
	tt.Move(vp.X+vp.W/2, vp.Y+vp.H-6)
	tt.Release(vp.X+vp.W/2, vp.Y+vp.H-6)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != full {
		t.Errorf("从头拖到尾后复制的内容不对：\n got: %q\nwant: %q", got, full)
	}

	// 2. 点进文本再 Ctrl+A、Ctrl+C：同样应是整段。
	tt.SetClipboard("")
	tt.ClickAt(vp.X+20, vp.Y+10)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != full {
		t.Errorf("全选后复制的内容不对：\n got: %q\nwant: %q", got, full)
	}

	// 3. 只拖选中间几行（第 2 行中段 → 第 5 行中段）：
	//    起止两行只取一部分，中间两行必须完整，且换行符一个不少。
	r, ok := tt.Find(full)
	if !ok {
		t.Fatal("找不到整段日志文本（日志应该放在同一个 Text 里）")
	}
	lh := r.H / float32(len(lines)) // 6 行都很短、不折行，按等分取每行中心
	tt.SetClipboard("")
	tt.Press(r.X+60, r.Y+lh*1.5)
	tt.Move(r.X+60, r.Y+lh*4.5)
	tt.Release(r.X+60, r.Y+lh*4.5)
	tt.Key(ui.Cmd, ui.KeyC)
	got := tt.Clipboard()
	if want := lines[2] + "\n" + lines[3] + "\n"; !strings.Contains(got, want) {
		t.Errorf("跨行拖选没有完整带上中间的行：\n got: %q\nwant 包含: %q", got, want)
	}
	if n := strings.Count(got, "\n"); n != 3 {
		t.Errorf("跨 4 行应有 3 个换行，实际 %d 个：%q", n, got)
	}
}
