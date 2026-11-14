package app

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/arno/wslc-manager/locales"
	"github.com/egoist/mygo/ui"
)

// dialogs 绘制所有模态对话框（在主视图末尾调用）。
func (a *App) dialogs(c *ui.Context) {
	a.confirmDialog(c)
	a.addProjectDialog(c)
	a.settingsDialog(c)
}

// confirmDialog 是通用确认对话框。
func (a *App) confirmDialog(c *ui.Context) {
	t := c.Theme()

	ui.Modal(c, &a.confirmOpen, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, a.confirmTitle).FontSize(15).Bold()
			ui.Text(c, a.confirmBody).FontSize(12).TextColor(t.TextMuted)

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.CommonCancel()).AutoFocus().Clicked() {
					a.confirmOpen = false
					a.confirmAction = nil
				}
				btn := ui.PrimaryButton(c, locales.CommonConfirm())
				if a.confirmDanger {
					btn.Background(t.Danger).TextColor(ui.RGBA(255, 255, 255, 1))
				}
				if btn.Clicked() {
					act := a.confirmAction
					a.confirmOpen = false
					a.confirmAction = nil
					if act != nil {
						act()
					}
				}
			})
		})
	})
}

// addProjectDialog 是「添加项目目录」对话框。
func (a *App) addProjectDialog(c *ui.Context) {
	t := c.Theme()

	ui.Modal(c, &a.addProjectOpen, func() {
		ui.Column(c).Width(520).Gap(12).Children(func() {
			ui.Text(c, locales.ComposeAddTitle()).FontSize(15).Bold()
			ui.Text(c, locales.ComposeChooseDir()).
				FontSize(12).TextColor(t.TextMuted)

			// 输入框与「浏览…」同一行。submitted 必须声明在行作用域之外：
			// 声明在 Children 闭包里，后面的「添加」按钮就永远收不到 Enter。
			var submitted bool
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				submitted = ui.TextInput(c, &a.addProjectPath).
					Placeholder(locales.ComposeAddPathPlaceholder()).
					Grow(1).
					AutoFocus().
					Submitted()
				// 选目录后直接填入输入框，用户仍可手改。
				if ui.Button(c, locales.CommonBrowse()).Tooltip(locales.SettingsBrowseDir()).Clicked() {
					a.pickDirectory(locales.ComposeAddTitle(), func(dir string) {
						a.addProjectPath = dir
					})
				}
			})

			// 提示是否命中 compose 文件
			if p := strings.TrimSpace(a.addProjectPath); p != "" {
				// 这里只做路径形态的提示，不访问磁盘以免阻塞构建。
				ui.Text(c, locales.ComposeWillValidate()).FontSize(11).TextColor(t.TextMuted)
			}

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.CommonCancel()).Clicked() {
					a.addProjectOpen = false
				}
				path := strings.TrimSpace(a.addProjectPath)
				if (ui.PrimaryButton(c, locales.CommonAdd()).Clicked() || submitted) && path != "" {
					a.addProjectOpen = false
					a.AddProject(path)
				}
			})
		})
	})
}

// settingsDialog 是设置对话框。
func (a *App) settingsDialog(c *ui.Context) {
	t := c.Theme()

	ui.Modal(c, &a.settingsOpen, func() {
		// 弹窗尺寸固定，不能让内容把它撑破：
		// 标题 + 固定高度的滚动区 + 固定高度的按钮行。
		ui.Column(c).Width(620).Gap(12).Children(func() {
			ui.Text(c, locales.SettingsTitle()).FontSize(15).Bold()

			// 滚动区用固定高度（不写 Grow）：Grow 会与 MaxHeight 相互竞争，
			// 导致整个弹窗被内容撑得超出屏幕。
			//
			// 内边距的取舍：
			//   - 顶部留 4，否则第一项贴着滚动区上边缘，看起来像被裁掉；
			//   - 右侧留 14，给滚动条让位（滚动条画在内容之上，不占布局）。
			ui.Scroll(c).Height(430).Children(func() {
				ui.Column(c).FillWidth().Padding(4, 14, 4, 0).Gap(18).Children(func() {
					// ── 常规 ──────────────────────────────
					// Fieldset 自带分组标题，并让组内所有 Field 的标签
					// 共享同一列宽；之前手写「加粗小标题 + Form」时
					// 两套对齐并存，视觉上就乱了。
					ui.Fieldset(c, locales.SettingsGeneral(), func() {
						ui.Field(c, locales.SettingsLanguage(), func() {
							a.languageSelect(c)
						}).Description(locales.SettingsLanguageHint())

						ui.Field(c, locales.SettingsAccent(), func() {
							a.accentPicker(c)
						}).Description(locales.SettingsAccentHint())

						ui.Field(c, locales.SettingsAutoRefresh(), func() {
							ui.TextInput(c, &a.pollSecondsText).
								Placeholder(locales.SettingsZeroDisables()).Width(160)
						}).Description(locales.SettingsAutoRefreshHint())

						// 自动更新的两个偏好由 MyGo 的更新插件保存（updater.json），
						// 改动立即生效，不用点「保存」，和语言、强调色一样。
						// 开关直接绑定快照里的字段，点击后下一帧就能看到新值；
						// 插件确认后再经 OnChange 把快照校正一遍。
						// 不支持自更新的构建（开发版本等）不会在后台检查，开关置灰并说明原因。
						ui.Field(c, locales.SettingsAutoCheckUpdates(), func() {
							sw := ui.Switch(c, &a.upd.AutoCheck).
								Label(locales.SettingsAutoCheckUpdates()).Disabled(!a.upd.Supported)
							if sw.Changed() {
								a.setAutoCheckUpdates(a.upd.AutoCheck)
							}
						}).Description(a.updateFieldHint(locales.SettingsAutoCheckUpdatesHint()))

						ui.Field(c, locales.SettingsAutoInstallUpdates(), func() {
							sw := ui.Switch(c, &a.upd.AutoInstall).
								Label(locales.SettingsAutoInstallUpdates()).Disabled(!a.upd.Supported)
							if sw.Changed() {
								a.setAutoInstallUpdates(a.upd.AutoInstall)
							}
						}).Description(a.updateFieldHint(locales.SettingsAutoInstallUpdatesHint()))
					})

					// ── 项目扫描 ──────────────────────────
					ui.Fieldset(c, locales.SettingsProjectScan(), func() {
						ui.Field(c, locales.SettingsScanDepth(), func() {
							ui.TextInput(c, &a.scanDepthText).Placeholder("3").Width(120)
						}).Description(locales.SettingsScanDepthHint())
					})

					// ── 扫描根目录 ────────────────────────
					a.settingsGroup(c, locales.SettingsScanDirs(), func() {
						if len(a.settings.ScanRoots) == 0 {
							ui.Text(c, locales.SettingsNoScanDirs()).
								FontSize(11).TextColor(t.TextMuted)
						}
						for i := range a.settings.ScanRoots {
							idx := i
							a.settingsListItem(c, a.settings.ScanRoots[idx], func() {
								a.settings.ScanRoots = append(
									a.settings.ScanRoots[:idx], a.settings.ScanRoots[idx+1:]...)
								a.saveSettings()
							}, locales.SettingsRemoveDir())
						}
						ui.Row(c).Gap(8).Children(func() {
							ui.TextInput(c, &a.newScanRoot).
								Placeholder(locales.SettingsScanDirPlaceholder()).Grow(1)
							// 让用户直接选目录，而不是手打路径。
							if ui.Button(c, locales.CommonBrowse()).
								Tooltip(locales.SettingsBrowseDir()).Clicked() {
								a.pickDirectory(locales.SettingsScanDirs(), func(dir string) {
									a.settings.ScanRoots = append(a.settings.ScanRoots, dir)
									a.saveSettings()
								})
							}
							if ui.Button(c, locales.CommonJoin()).Clicked() &&
								strings.TrimSpace(a.newScanRoot) != "" {
								a.settings.ScanRoots = append(a.settings.ScanRoots,
									strings.TrimSpace(a.newScanRoot))
								a.newScanRoot = ""
								a.saveSettings()
							}
						})
					})

					// ── 已添加的项目 ──────────────────────
					a.settingsGroup(c, locales.SettingsAddedProjects(), func() {
						if len(a.settings.Projects) == 0 {
							ui.Text(c, locales.CommonNone()).FontSize(11).TextColor(t.TextMuted)
						}
						for i := range a.settings.Projects {
							idx := i
							a.settingsListItem(c, a.settings.Projects[idx], func() {
								a.RemoveProject(a.settings.Projects[idx])
							}, locales.ComposeRemoveFromList())
						}
					})

					ui.Divider(c)
					if ui.Button(c, locales.SettingsOpenFolder()).Clicked() {
						if a.store != nil {
							a.openInExplorer(strings.TrimSuffix(a.store.Path(), "settings.json"))
						}
					}
				})
			})

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.CommonClose()).Clicked() {
					a.settingsOpen = false
				}
				if ui.PrimaryButton(c, locales.CommonSave()).Clicked() {
					a.applySettingsFromText()
					a.settingsOpen = false
					a.toast(locales.SettingsSaved())
				}
			})
		})
	})
}

// settingsGroup 画一个「标题 + 内容」的分组。
//
// 用于不适合用 Fieldset 的内容（路径列表、输入框 + 按钮的组合行）：
// Fieldset 要求内容是 Field，而这些是自由布局。
// 标题样式与 Fieldset 的 legend 保持一致（小号、次要色、加粗），
// 这样两种分组混排时看起来是同一套设计。
func (a *App) settingsGroup(c *ui.Context, title string, body func()) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, title).FontSize(12).Bold().TextColor(t.TextMuted)
		body()
	})
}

// settingsListItem 画一行「路径 + 删除按钮」。
//
// 路径用等宽的感觉（小号、次要色）并允许省略，
// 整行悬停有提示，避免长路径把删除按钮挤出去。
func (a *App) settingsListItem(c *ui.Context, text string, onRemove func(), removeTooltip string) {
	t := c.Theme()
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(11).TextColor(t.TextMuted).
			Grow(1).SingleLine().Tooltip(text)
		if ui.Button(c, "").Label(locales.CommonRemove()).
			Tooltip(removeTooltip).Children(func() {
			ui.Icon(c, icon("x")).FontSize(12)
		}).Clicked() {
			onRemove()
		}
	})
}

// applySettingsFromText 把设置对话框里的文本字段写回结构化设置。
func (a *App) applySettingsFromText() {
	if n := atoiSafe(a.pollSecondsText); n >= 0 {
		a.settings.PollSeconds = n
	}
	if n := atoiSafe(a.scanDepthText); n > 0 {
		a.settings.ScanDepth = n
	}
	a.saveSettings()
}

// atoiSafe 把字符串转 int，失败返回 -1。
func atoiSafe(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return -1
		}
	}
	return n
}

// openInExplorer 在系统文件管理器中打开一个目录。
//
// Windows 用 explorer，macOS 用 open，其它用 xdg-open。
func (a *App) openInExplorer(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		a.toast(locales.SettingsOpenDirFailed(err.Error()))
		return
	}
	// explorer 会返回非零退出码但确实打开了目录，因此这里不等结果。
	go func() { _ = cmd.Wait() }()
}
