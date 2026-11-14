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

		cols := []ui.TableColumn{
			{Title: locales.CommonName(), Width: 280, MinWidth: 140},
			{Title: locales.CommonDriver(), Width: 90, MinWidth: 60},
			{Title: locales.VolumesMountPoint(), MinWidth: 160},
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

		ui.Table(c, &a.volumeList, cols, len(rows), func(row, col int) {
			if row < 0 || row >= len(rows) {
				return
			}
			v := rows[row]
			// 选中行只叠淡蓝，不需要把文字改成反色。
			muted := t.TextMuted
			switch col {
			case 0:
				ui.Text(c, v.Name).SingleLine()
			case 1:
				ui.Text(c, v.Driver).TextColor(muted)
			case 2:
				ui.Text(c, v.Mountpoint).SingleLine().TextColor(muted).FontSize(12)
			}
		}).Grow(1).Label(locales.VolumesTitle()).ContextMenu(func(m *ui.Menu) {
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

		cols := []ui.TableColumn{
			{Title: locales.CommonName(), Width: 300, MinWidth: 140},
			{Title: locales.CommonDriver(), Width: 100, MinWidth: 60},
			{Title: locales.NetworksId(), Width: 120, MinWidth: 90},
			{Title: locales.CommonType(), Width: 90, MinWidth: 60},
			{Title: locales.ImagesCreated(), MinWidth: 140},
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

		ui.Table(c, &a.networkList, cols, len(rows), func(row, col int) {
			if row < 0 || row >= len(rows) {
				return
			}
			n := rows[row]
			// 选中行只叠淡蓝，不需要把文字改成反色。
			muted := t.TextMuted
			switch col {
			case 0:
				ui.Text(c, n.Name).SingleLine()
			case 1:
				ui.Text(c, n.Driver).TextColor(muted)
			case 2:
				ui.Text(c, shortID(n.ID)).FontSize(12).TextColor(muted)
			case 3:
				kind := locales.CommonCustom()
				if n.IsBuiltin() {
					kind = locales.CommonBuiltin()
				}
				ui.Text(c, kind).TextColor(muted)
			case 4:
				ui.Text(c, n.CreatedAt).SingleLine().TextColor(muted).FontSize(12)
			}
		}).Grow(1).Label(locales.NetworksTitle()).ContextMenu(func(m *ui.Menu) {
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
