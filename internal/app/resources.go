package app

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/locales"
)

// volumesPage 是卷管理页。
func (a *App) volumesPage(c *ui.Context) {
	t := c.Theme()
	rows := a.filteredVolumes()

	if len(rows) == 0 {
		a.emptyState(c, locales.VolumesEmptyTitle(), locales.VolumesEmptyBody())
		return
	}

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(8, 12).Gap(6).AlignItems(ui.Center).Children(func() {
			var sel *wslc.Volume
			if a.volumeSel >= 0 && a.volumeSel < len(rows) {
				sel = &rows[a.volumeSel]
			}

			if ui.Button(c, locales.VolumesNewEllipsis()).Clicked() {
				a.newVolumeOpen = true
				a.newVolumeName = ""
			}

			ui.Spacer(c)

			del := ui.Button(c, locales.CommonDelete()).Disabled(sel == nil).Tooltip(locales.VolumesDeleteSelected())
			del.TextColor(t.Danger)
			if del.Clicked() && sel != nil {
				name := sel.Name
				a.confirm(locales.VolumesDelete(), locales.CommonDeleteVolumeData(name), true, func() {
					a.runAsync("rmvol-"+name, locales.CommonDeletedVolume(name), func(ctx context.Context) (string, error) {
						return "", a.wslc.RemoveVolume(ctx, name, true)
					})
				})
			}
		})

		ui.Divider(c)

		// 列宽按内容分配（见 tablefit.go）。
		grid := make([][]string, len(rows))
		for i, v := range rows {
			grid[i] = volumeRowTexts(v)
		}
		volTitles := []string{locales.CommonName(), locales.CommonDriver(), locales.VolumesMountPoint()}
		cols, keep := buildColumns(c, tableWidth(c, &a.volumeTableH), autoTable{
			Titles: volTitles,
			Floors: []float32{140, 60, 160},
			Grid:   grid,
		})
		if cols == nil {
			cols = []ui.TableColumn{
				{Title: volTitles[0], Width: 280, MinWidth: 140},
				{Title: volTitles[1], Width: 90, MinWidth: 60},
				{Title: volTitles[2], MinWidth: 160},
			}
			keep = []int{0, 1, 2}
		}

		a.volumeList.Key = func(i int) any {
			if i < len(rows) {
				return rows[i].Name
			}
			return i
		}
		a.volumeList.Selected = &a.volumeSel
		a.volumeList.RoundRows = true
		styleRows(&a.volumeList, t)
		syncRowSelection(&a.volumeList, a.volumeSel)

		ui.Table(c, &a.volumeList, cols, len(rows), func(row, col int) {
			if row < 0 || row >= len(rows) {
				return
			}
			// 选中行底色带强调色，次要文字需提亮才看得清。
			muted := t.TextMuted
			if a.volumeList.CellSelected(row, col) {
				muted = t.Text
			}
			orig := keep[col]
			switch orig {
			case 0:
				ui.Text(c, grid[row][orig]).SingleLine()
			case 1:
				ui.Text(c, grid[row][orig]).TextColor(muted)
			default:
				// 挂载点很长，被截断时鼠标悬停能看到全文。
				ui.Text(c, grid[row][orig]).SingleLine().TextColor(muted).FontSize(12).Tooltip(grid[row][orig])
			}
		}).Grow(1).Label(locales.VolumesTitle()).Bind(&a.volumeTableH).ContextMenu(func(m *ui.Menu) {
			if a.volumeSel < 0 || a.volumeSel >= len(rows) {
				return
			}
			v := rows[a.volumeSel]
			if m.Item(locales.CommonCopyName()).Chosen() {
				c.WriteClipboard(v.Name)
				a.toast(locales.CommonCopied())
			}
			if m.Item(locales.VolumesCopyMount()).Chosen() {
				c.WriteClipboard(v.Mountpoint)
				a.toast(locales.CommonCopied())
			}
		})

	})

	// 新建卷对话框
	ui.Modal(c, &a.newVolumeOpen, func() {
		ui.Text(c, locales.VolumesNewTitle()).FontSize(15).Bold()
		submitted := ui.TextInput(c, &a.newVolumeName).Placeholder(locales.VolumesName()).AutoFocus().Submitted()
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.CommonCancel()).Clicked() {
				a.newVolumeOpen = false
			}
			name := strings.TrimSpace(a.newVolumeName)
			if (ui.PrimaryButton(c, locales.CommonCreate()).Clicked() || submitted) && name != "" {
				a.newVolumeOpen = false
				a.runAsync("mkvol-"+name, locales.CommonCreatedVolume(name), func(ctx context.Context) (string, error) {
					return "", a.wslc.CreateVolume(ctx, name)
				})
			}
		})
	})
}

// filteredVolumes 按搜索框过滤卷。
func (a *App) filteredVolumes() []wslc.Volume {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.volumes
	}
	out := make([]wslc.Volume, 0, len(a.volumes))
	for _, v := range a.volumes {
		if strings.Contains(strings.ToLower(v.Name+" "+v.Mountpoint), q) {
			out = append(out, v)
		}
	}
	return out
}

// networksPage 是网络管理页。
func (a *App) networksPage(c *ui.Context) {
	t := c.Theme()
	rows := a.filteredNetworks()

	if len(rows) == 0 {
		a.emptyState(c, locales.NetworksEmptyTitle(), locales.NetworksEmptyBody())
		return
	}

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(8, 12).Gap(6).AlignItems(ui.Center).Children(func() {
			var sel *wslc.Network
			if a.networkSel >= 0 && a.networkSel < len(rows) {
				sel = &rows[a.networkSel]
			}

			if ui.Button(c, locales.NetworksNewEllipsis()).Clicked() {
				a.newNetworkOpen = true
				a.newNetworkName = ""
			}

			ui.Spacer(c)

			del := ui.Button(c, locales.CommonDelete()).Disabled(sel == nil || sel.IsBuiltin()).Tooltip(locales.NetworksDeleteSelected())
			del.TextColor(t.Danger)
			if del.Clicked() && sel != nil && !sel.IsBuiltin() {
				id, name := sel.ID, sel.Name
				a.confirm(locales.NetworksDelete(), locales.CommonDeleteNetwork(name), true, func() {
					a.runAsync("rmnet-"+id, locales.CommonDeletedNetwork(name), func(ctx context.Context) (string, error) {
						return "", a.wslc.RemoveNetwork(ctx, id, true)
					})
				})
			}
		})

		ui.Divider(c)

		// 列宽按内容分配（见 tablefit.go）。
		grid := make([][]string, len(rows))
		for i, n := range rows {
			grid[i] = networkRowTexts(n)
		}
		netTitles := []string{
			locales.CommonName(), locales.CommonDriver(), locales.NetworksId(),
			locales.CommonType(), locales.ImagesCreated(),
		}
		cols, keep := buildColumns(c, tableWidth(c, &a.networkTableH), autoTable{
			Titles: netTitles,
			Floors: []float32{140, 60, 90, 60, 140},
			Grid:   grid,
		})
		if cols == nil {
			cols = []ui.TableColumn{
				{Title: netTitles[0], Width: 300, MinWidth: 140},
				{Title: netTitles[1], Width: 100, MinWidth: 60},
				{Title: netTitles[2], Width: 120, MinWidth: 90},
				{Title: netTitles[3], Width: 90, MinWidth: 60},
				{Title: netTitles[4], MinWidth: 140},
			}
			keep = []int{0, 1, 2, 3, 4}
		}

		a.networkList.Key = func(i int) any {
			if i < len(rows) {
				return rows[i].ID
			}
			return i
		}
		a.networkList.Selected = &a.networkSel
		a.networkList.RoundRows = true
		styleRows(&a.networkList, t)
		syncRowSelection(&a.networkList, a.networkSel)

		ui.Table(c, &a.networkList, cols, len(rows), func(row, col int) {
			if row < 0 || row >= len(rows) {
				return
			}
			// 选中行底色带强调色，次要文字需提亮才看得清。
			// （这里以前误用了 volumeList.CellSelected，导致网络表选中行的次要文字不会提亮。）
			muted := t.TextMuted
			if a.networkList.CellSelected(row, col) {
				muted = t.Text
			}
			orig := keep[col]
			text := grid[row][orig]
			switch orig {
			case 0:
				ui.Text(c, text).SingleLine()
			case 1:
				ui.Text(c, text).TextColor(muted)
			case 2:
				ui.Text(c, text).FontSize(12).TextColor(muted)
			case 3:
				ui.Text(c, text).TextColor(muted)
			default:
				ui.Text(c, text).SingleLine().TextColor(muted).FontSize(12)
			}
		}).Grow(1).Label(locales.NetworksTitle()).Bind(&a.networkTableH).ContextMenu(func(m *ui.Menu) {
			if a.networkSel < 0 || a.networkSel >= len(rows) {
				return
			}
			n := rows[a.networkSel]
			if m.Item("复制名称").Chosen() {
				c.WriteClipboard(n.Name)
				a.toast(locales.CommonCopied())
			}
			if m.Item(locales.NetworksCopyID()).Chosen() {
				c.WriteClipboard(n.ID)
				a.toast(locales.CommonCopied())
			}
		})

	})

	// 新建网络对话框
	ui.Modal(c, &a.newNetworkOpen, func() {
		ui.Text(c, locales.NetworksNewTitle()).FontSize(15).Bold()
		ui.Text(c, locales.NetworksNewTooltip()).FontSize(12).TextColor(t.TextMuted)
		submitted := ui.TextInput(c, &a.newNetworkName).Placeholder(locales.NetworksName()).AutoFocus().Submitted()
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.CommonCancel()).Clicked() {
				a.newNetworkOpen = false
			}
			name := strings.TrimSpace(a.newNetworkName)
			if (ui.PrimaryButton(c, locales.CommonCreate()).Clicked() || submitted) && name != "" {
				a.newNetworkOpen = false
				a.runAsync("mknet-"+name, locales.CommonCreatedNetwork(name), func(ctx context.Context) (string, error) {
					return "", a.wslc.CreateNetwork(ctx, name)
				})
			}
		})
	})
}

// filteredNetworks 按搜索框过滤网络。
func (a *App) filteredNetworks() []wslc.Network {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.networks
	}
	out := make([]wslc.Network, 0, len(a.networks))
	for _, n := range a.networks {
		if strings.Contains(strings.ToLower(n.Name+" "+n.Driver), q) {
			out = append(out, n)
		}
	}
	return out
}

// volumeRowTexts 返回卷表某一行的各列文字。
//
// 量列宽和画单元格都用它，保证「量的」就是「画的」。
func volumeRowTexts(v wslc.Volume) []string {
	return []string{v.Name, v.Driver, v.Mountpoint}
}

// networkRowTexts 返回网络表某一行的各列文字。
func networkRowTexts(n wslc.Network) []string {
	kind := locales.CommonCustom()
	if n.IsBuiltin() {
		kind = locales.CommonBuiltin()
	}
	return []string{n.Name, n.Driver, shortID(n.ID), kind, n.CreatedAt}
}
