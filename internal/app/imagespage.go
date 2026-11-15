package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/locales"
)

// imagesPage 是镜像管理页。
func (a *App) imagesPage(c *ui.Context) {
	if len(a.filteredImages()) == 0 {
		a.imageActionBar(c, nil)
		a.emptyState(c, locales.ImagesEmptyTitle(), locales.ImagesEmptyBody())
		a.imagePullDialog(c)
		return
	}

	rows := a.filteredImages()

	// 操作条固定在上方。
	a.imageActionBar(c, rows)

	// 表格与输出面板：有内容时上下分栏（可拖动），否则表格占满。
	if a.outputOpen {
		ui.SplitVerticalPercent(c, &a.outputPercent, func() {
			a.imageTable(c, rows)
		}, func() {
			a.outputPanel(c)
		}).Grow(1)
	} else {
		a.imageTable(c, rows)
	}

	a.imagePullDialog(c)
}

// imageActionBar 绘制镜像页顶部的操作条。
func (a *App) imageActionBar(c *ui.Context, rows []wslc.Image) {
	t := c.Theme()

	ui.Row(c).Padding(8, 12).Gap(6).AlignItems(ui.Center).Children(func() {
		var sel *wslc.Image
		if a.imageSel >= 0 && a.imageSel < len(rows) {
			sel = &rows[a.imageSel]
		}

		if ui.Button(c, locales.ImagesPulling()).Tooltip(locales.ImagesPullFromRepo()).Clicked() {
			a.pullImageDialogOpen()
		}

		vSep(c)

		if ui.Button(c, locales.ImagesShowTags()).Disabled(sel == nil).
			Tooltip(locales.ImagesListTagsTooltip()).Clicked() && sel != nil {
			a.loadImageTags(*sel)
		}

		if ui.Button(c, locales.CommonUpdate()).Disabled(sel == nil).
			Tooltip(locales.ImagesUpdateTooltip()).Clicked() && sel != nil {
			img := *sel
			a.confirm(locales.ImagesUpdate(), locales.ImagesUpdateWillPull(img.Ref())+locales.ImagesUpdateOverwrite(), false, func() {
				a.pullAndReport(img.Repository+":"+img.Tag, locales.ImagesUpdatedOne(img.Ref()))
			})
		}

		ui.Spacer(c)

		// 一键更新：把所有可更新的镜像批量重新拉取。
		if ui.Button(c, locales.ImagesUpdateAll()).Tooltip(locales.ImagesUpdateAllTooltip()).Clicked() {
			a.confirmUpdateAll(rows)
		}

		del := ui.Button(c, locales.CommonDelete()).Disabled(sel == nil).Tooltip(locales.ImagesDeleteSelected())
		del.TextColor(t.Danger)
		if del.Clicked() && sel != nil {
			a.confirmDeleteImage(*sel)
		}
	})
}

// confirmDeleteImage 弹出删除镜像的确认框。
func (a *App) confirmDeleteImage(img wslc.Image) {
	ref, id := img.Ref(), img.ID
	a.confirm(locales.ImagesDelete(), locales.CommonDeleteImage(ref), true, func() {
		a.runAsync("rmi-"+id, locales.CommonDeletedImage(ref), func(ctx context.Context) (string, error) {
			return "", a.wslc.RemoveImage(ctx, id, true)
		})
	})
}

// imageTable 绘制镜像表格。
func (a *App) imageTable(c *ui.Context, rows []wslc.Image) {
	t := c.Theme()

	// 列宽按内容分配（见 tablefit.go）。
	grid := make([][]string, len(rows))
	for i, img := range rows {
		grid[i] = imageRowTexts(img)
	}
	titles := []string{
		locales.CommonRepository(), locales.CommonTags(), locales.ImagesId(),
		locales.CommonSize(), locales.ImagesCreated(), locales.NavContainers(),
	}
	cols, keep := buildColumns(c, tableWidth(c, &a.imageTableH), autoTable{
		Titles: titles,
		Floors: []float32{140, 70, 90, 60, 80, 50},
		Aligns: []ui.Align{0, 0, 0, ui.End, 0, ui.End},
		// 悬空镜像的标签整列都是「—」，地方不够时不必留着它。
		DropTrivial: true,
		Grid:        grid,
	})
	if cols == nil {
		// 第一帧还量不到表格宽度：先用写死的值画一帧，下一帧就按内容分配。
		cols = []ui.TableColumn{
			{Title: titles[0], Width: 260, MinWidth: 140},
			{Title: titles[1], Width: 120, MinWidth: 70},
			{Title: titles[2], Width: 120, MinWidth: 90},
			{Title: titles[3], Width: 90, MinWidth: 60, Align: ui.End},
			{Title: titles[4], Width: 130, MinWidth: 80},
			{Title: titles[5], Width: 70, MinWidth: 50, Align: ui.End},
		}
		keep = []int{0, 1, 2, 3, 4, 5}
	}

	a.imageList.Key = func(i int) any {
		if i < len(rows) {
			return rows[i].ID + rows[i].Repository + rows[i].Tag
		}
		return i
	}
	a.imageList.Selected = &a.imageSel
	a.imageList.RoundRows = true
	styleRows(&a.imageList, t)
	syncRowSelection(&a.imageList, a.imageSel)

	table := ui.Table(c, &a.imageList, cols, len(rows), func(row, col int) {
		if row < 0 || row >= len(rows) {
			return
		}
		// 选中行底色带强调色，次要文字需提亮才看得清。
		muted := t.TextMuted
		if a.imageList.CellSelected(row, col) {
			muted = t.Text
		}
		// 列可能被收起（整列无信息），keep 把表格里的列号映回原来的列。
		if col < 0 || col >= len(keep) {
			return
		}
		orig := keep[col]
		if orig >= len(grid[row]) {
			return
		}
		text := grid[row][orig]
		switch orig {
		case 0:
			ui.Text(c, text).SingleLine()
		case 1:
			ui.Text(c, text).SingleLine().TextColor(muted)
		case 2:
			ui.Text(c, text).FontSize(12).TextColor(muted)
		case 3:
			ui.Text(c, text).TextColor(muted)
		case 4:
			ui.Text(c, text).TextColor(muted).SingleLine().Tooltip(text)
		default:
			ui.Text(c, text).TextColor(muted)
		}
	}).Grow(1).Label(locales.ImagesTitle()).Bind(&a.imageTableH)

	table.ContextMenu(func(m *ui.Menu) {
		if a.imageSel < 0 || a.imageSel >= len(rows) {
			return
		}
		img := rows[a.imageSel]
		if m.Item(locales.ImagesCopyID()).Chosen() {
			c.WriteClipboard(img.ID)
			a.toast(locales.CommonCopied())
		}
		if m.Item(locales.ImagesCopyRef()).Chosen() {
			c.WriteClipboard(img.Ref())
			a.toast(locales.CommonCopied())
		}
		m.Separator()
		if m.Item(locales.ImagesShowDetails()).Chosen() {
			a.loadImageInfo(img)
		}
		if m.Item(locales.ImagesTagsFor()).Chosen() {
			a.loadImageTags(img)
		}
		if m.Item(locales.ImagesUpdateThis()).Disabled(img.Dangling()).Chosen() {
			a.pullAndReport(img.Ref(), locales.ImagesUpdatedOne(img.Ref()))
		}
		m.Separator()
		if m.Item(locales.CommonDelete()).Chosen() {
			a.confirmDeleteImage(img)
		}
	})
}

// filteredImages 按搜索框过滤镜像。
func (a *App) filteredImages() []wslc.Image {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.images
	}
	out := make([]wslc.Image, 0, len(a.images))
	for _, img := range a.images {
		hay := strings.ToLower(img.Repository + " " + img.Tag + " " + img.ID)
		if strings.Contains(hay, q) {
			out = append(out, img)
		}
	}
	return out
}

// shortID 截断 sha256 前缀到 12 位，与 docker 的习惯一致。
func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// loadImageInfo 显示镜像的详细信息。
func (a *App) loadImageInfo(img wslc.Image) {
	var b strings.Builder
	line := func(k, v string) {
		if strings.TrimSpace(v) == "" {
			v = "—"
		}
		fmt.Fprintf(&b, "%-14s %s\n", k+":", v)
	}
	line(locales.CommonRepository(), img.Repository)
	line(locales.CommonTags(), img.Tag)
	line(locales.ImagesFullRef(), img.Ref())
	line(locales.ImagesId(), img.ID)
	line(locales.CommonSummary(), img.Digest)
	line(locales.CommonSize(), img.Size)
	line(locales.ImagesSharedSize(), img.SharedSize)
	line(locales.ImagesUniqueSize(), img.UniqueSize)
	line(locales.ImagesCreated(), img.CreatedAt)
	line(locales.ContainersCreated(), img.CreatedSince)
	line(locales.ImagesUsedBy(), img.Containers)
	a.showOutput(locales.ImagesImagePanel(img.Ref()), b.String())
}

// loadImageTags 列出与选中镜像同仓库的所有标签。
//
// wslc 没有「列出远端标签」的命令（tag 列表要走仓库 HTTP API），
// 因此这里列出的是本地已存在、属于同一仓库的所有标签版本。
// 这回答的是常见问题：这仓库我本地有哪些版本、当前用的是哪个。
func (a *App) loadImageTags(img wslc.Image) {
	if img.Dangling() {
		a.toast(locales.ImagesDanglingNoTags())
		return
	}
	a.showOutput(locales.ImagesTagsPanel(img.Repository), imageTagsText(a.images, img))
}

// imageTagsText 生成「同仓库本地标签」的报告文本。
//
// 抽成独立函数便于单元测试直接断言内容。
func imageTagsText(all []wslc.Image, img wslc.Image) string {
	type tagRow struct {
		Tag, ID, Size, Created string
	}
	var found []tagRow
	for _, other := range all {
		if other.Repository != img.Repository || img.Repository == "<none>" {
			continue
		}
		found = append(found, tagRow{
			Tag:     other.Tag,
			ID:      shortID(other.ID),
			Size:    other.Size,
			Created: other.CreatedSince,
		})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Tag < found[j].Tag })

	var b strings.Builder
	b.WriteString(locales.ImagesRepoLine(img.Repository))
	if len(found) == 0 {
		b.WriteString(locales.ImagesNoLocalRepo())
		return b.String()
	}
	b.WriteString(locales.ImagesLocalTagsHeader(len(found)))
	fmt.Fprintf(&b, "%-24s %-14s %-10s %s\n", locales.CommonTags(), locales.ImagesId(), locales.CommonSize(), locales.ContainersCreated())
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", 68))
	for _, tr := range found {
		mark := " "
		if tr.Tag == img.Tag {
			mark = "*"
		}
		tag := tr.Tag
		if tag == "<none>" {
			tag = "—"
		}
		fmt.Fprintf(&b, "%s %-22s %-14s %-10s %s\n", mark, tag, tr.ID, tr.Size, tr.Created)
	}
	b.WriteString(locales.ImagesCurrentTagMark())
	b.WriteString(locales.ImagesUpdateHint())
	return b.String()
}

// pullImageDialogOpen 打开「拉取镜像」对话框。
func (a *App) pullImageDialogOpen() {
	a.update(func() {
		a.pullOpen = true
		a.pullRef = ""
	})
}

// pullAndReport 拉取一个镜像，并实时把进度显示在输出面板里。
//
// 之前的做法是等命令整体结束才显示结果：拉一个几百 MB 的镜像时
// 界面毫无反馈，用户不知道是在下载还是已经卡住。
// wslc pull 本身会逐行输出进度，这里用流式执行把它实时转发到面板。
func (a *App) pullAndReport(ref, doneMsg string) {
	pull := a.pullStream
	if pull == nil {
		return
	}

	// 立即打开面板，让用户马上看到「正在拉取」。
	a.update(func() {
		a.outputTitle = locales.ImagesPullPanel(ref)
		a.outputText = locales.ImagesStartedPull(ref) + " …\n"
		a.outputOpen = true
		a.outputBusy = true
		a.setBusy("pull-"+ref, true)
	})

	go func() {
		ctx, cancel := longContext()
		defer cancel()

		// 收集到的进度行。onLine 在后台协程里被调用，
		// 这里用互斥量保护，再把快照交给主线程刷新。
		var mu sync.Mutex
		var lines []string

		appendLine := func(line string) {
			mu.Lock()
			lines = append(lines, line)
			text := strings.Join(lines, "\n")
			n := len(lines)
			mu.Unlock()

			a.update(func() {
				a.outputText = text
				// 让状态栏能看到行数增长，确认进度在走。
				a.outputBusy = true
				a.outputLines = n
			})
		}

		res, err := pull(ctx, ref, appendLine)

		mu.Lock()
		final := strings.Join(lines, "\n")
		mu.Unlock()

		failed := err != nil || !res.Ok()
		switch {
		case err != nil:
			final += locales.ImagesPullFailedBlock() + err.Error()
		case !res.Ok():
			final += locales.ImagesPullFailedBlock() + res.Error().Error()
		default:
			final += locales.ImagesDoneBlock()
		}

		a.update(func() {
			a.outputText = final
			a.outputBusy = false
			a.outputLines = 0
			a.setBusy("pull-"+ref, false)
			if failed {
				a.lastError = firstLine(final)
				a.toast(locales.ImagesPullFailed() + firstLine(final))
			} else if doneMsg != "" {
				a.toast(doneMsg)
			}
		})
		a.Refresh()
	}()
}

// imagePullRefs 收集一批镜像里可重新拉取的引用：去重、保持顺序、跳过悬空镜像。
func imagePullRefs(rows []wslc.Image) []string {
	seen := map[string]bool{}
	var refs []string
	for _, img := range rows {
		if img.Dangling() {
			continue
		}
		ref := img.Ref()
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

// confirmUpdateAll 发起「一键更新全部」的确认，并在确认后逐个流式拉取。
func (a *App) confirmUpdateAll(rows []wslc.Image) {
	pull := a.pullStream
	if pull == nil {
		return
	}
	refs := imagePullRefs(rows)
	if len(refs) == 0 {
		a.toast(locales.ImagesNothingToUpdate())
		return
	}

	preview := refs
	if len(preview) > 8 {
		preview = preview[:8]
	}
	body := locales.ImagesUpdateQueueBlock(len(refs), strings.Join(preview, "\n"))
	if len(refs) > len(preview) {
		body += locales.ImagesAndMore(len(refs))
	}
	body += locales.ImagesPullSequential()

	a.confirm(locales.ImagesUpdateAllTitle(), body, false, func() {
		a.update(func() {
			a.outputTitle = locales.ImagesUpdateAllTitle()
			a.outputText = locales.ImagesUpdatingBlock(len(refs))
			a.outputOpen = true
			a.outputBusy = true
			a.setBusy("pull-all", true)
		})

		go func() {
			ctx, cancel := longContext()
			defer cancel()

			var mu sync.Mutex
			var buf []string
			var okCount, failCount int

			emit := func(line string) {
				mu.Lock()
				buf = append(buf, line)
				text := strings.Join(buf, "\n")
				mu.Unlock()
				a.update(func() { a.outputText = text })
			}

			for i, ref := range refs {
				emit(fmt.Sprintf("\n===== [%d/%d] %s =====", i+1, len(refs), ref))

				res, err := pull(ctx, ref, emit)
				if err != nil || !res.Ok() {
					failCount++
					reason := ""
					if err != nil {
						reason = err.Error()
					} else if res.Error() != nil {
						reason = res.Error().Error()
					}
					emit(locales.ImagesFailed() + firstLine(reason))
				} else {
					okCount++
					emit(locales.ImagesOk())
				}
			}

			mu.Lock()
			summary := locales.ImagesUpdateSummaryBlock(okCount, failCount)
			buf = append(buf, summary)
			final := strings.Join(buf, "\n")
			mu.Unlock()

			a.update(func() {
				a.outputText = final
				a.outputBusy = false
				a.setBusy("pull-all", false)
				if failCount == 0 {
					a.toast(locales.ImagesUpdatedBlock(okCount))
				} else {
					a.toast(locales.ImagesUpdateSummary(okCount, failCount))
				}
			})
			a.Refresh()
		}()
	})
}

// imagePullDialog 绘制拉取镜像对话框。
func (a *App) imagePullDialog(c *ui.Context) {
	ui.Modal(c, &a.pullOpen, func() {
		ui.Text(c, locales.ImagesPullTitle()).FontSize(15).Bold()
		ui.Text(c, locales.ImagesPullPlaceholder()).FontSize(12).TextColor(c.Theme().TextMuted)

		submitted := ui.TextInput(c, &a.pullRef).Placeholder("repo/image:tag").AutoFocus().Submitted()

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.CommonCancel()).Clicked() {
				a.pullOpen = false
			}
			if (ui.PrimaryButton(c, locales.CommonPull()).Clicked() || submitted) && strings.TrimSpace(a.pullRef) != "" {
				ref := strings.TrimSpace(a.pullRef)
				a.pullOpen = false
				a.pullAndReport(ref, locales.ImagesPulled(ref))
			}
		})
	})
}

// imageRowTexts 返回镜像表某一行的各列文字。
//
// 量列宽和画单元格都用它，保证「量的」就是「画的」。
func imageRowTexts(img wslc.Image) []string {
	repo := img.Repository
	if img.Dangling() {
		repo = "<none>"
	}
	tag := img.Tag
	if tag == "<none>" {
		tag = "—"
	}
	return []string{repo, tag, shortID(img.ID), img.Size, img.CreatedSince, img.Containers}
}
