package app

// 这个文件是「环境」卡片：检测依赖是否就绪，并提供一键安装/升级。
//
// 设计取舍：
//   - 检测与安装都放后台，界面只读状态、只记录「用户想做什么」；
//   - 安装命令由 internal/envsetup 生成（纯函数，可测），
//     这里只负责执行并把输出流式显示到输出面板；
//   - wslc 属于 WSL，不由我们下载，只引导 winget 安装/更新 WSL。

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/envsetup"
	"github.com/arno/wslc-manager/locales"
)

// envCard 渲染「依赖环境」卡片。
func (a *App) envCard(c *ui.Context) {
	t := c.Theme()

	// 这里**不**发起检测。
	//
	// 视图是「状态 → 界面」的纯函数，可能在同帧被调用多次；
	// 在构建期启动后台任务会带来两个问题：
	//   1. 违反分层约定，视图变成有副作用的；
	//   2. 检测结果异步回填会改变布局高度，导致界面在渲染中途跳动
	//      （测量类测试会因此随机失败——CI 上就真的踩到了）。
	// 首次检测由 Init 触发，之后由「重新检测」按钮刷新。

	a.infoCard(c, locales.EnvTitle(), func() {
		if !a.envChecked {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, locales.EnvDetecting()).FontSize(12).TextColor(t.TextMuted)
			})
			return
		}

		st := a.env

		// ── wslc ──────────────────────────────────────
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			a.envStatusIcon(c, st.WSLCOK())
			ui.Text(c, "wslc").FontSize(12).Bold().Grow(1)
			if st.WSLCOK() {
				ui.Text(c, locales.EnvInstalled(st.WSLVersion)).FontSize(11).TextColor(t.TextMuted)
			} else {
				ui.Text(c, locales.EnvMissing()).FontSize(11).TextColor(t.Danger)
			}
		})
		if !st.WSLCOK() {
			ui.Text(c, locales.EnvWslHint()).FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.Start).Children(func() {
				if st.CanInstallWSL() {
					if ui.PrimaryButton(c, locales.EnvInstallWSL()).Disabled(a.isBusy("env-wsl")).AlignSelf(ui.Start).Clicked() {
						a.runEnvArgs("env-wsl", st.InstallWSLArgs)
					}
				} else {
					ui.Text(c, locales.EnvNoWinget()).FontSize(11).TextColor(t.Warning)
				}
			})
		} else if st.CanInstallWSL() {
			// 已安装也允许更新 WSL——wslc 的行为跟 WSL 版本紧密相关。
			if ui.Button(c, locales.EnvUpdateWSL()).Disabled(a.isBusy("env-wsl")).AlignSelf(ui.Start).Clicked() {
				a.confirm(locales.EnvUpdateWSL(), locales.EnvUpdateWSLConfirm(), false, func() {
					a.runEnvArgs("env-wsl", st.UpgradeWSLArgs)
				})
			}
		}

		ui.Divider(c)

		// ── wslc-compose ──────────────────────────────
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			a.envStatusIcon(c, st.ComposeOK())
			ui.Text(c, "wslc-compose").FontSize(12).Bold().Grow(1)
			if st.ComposeOK() {
				ui.Text(c, locales.EnvInstalled(st.ComposeVersion)).FontSize(11).TextColor(t.TextMuted)
			} else {
				ui.Text(c, locales.EnvMissing()).FontSize(11).TextColor(t.Danger)
			}
		})

		if st.ComposeOK() && st.ComposeOutdated() {
			ui.Text(c, locales.EnvOutdated(st.ComposeVersion, st.Latest)).FontSize(11).TextColor(t.Warning)
		}

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			busy := a.isBusy("env-compose")
			switch {
			case !st.ComposeOK():
				if st.CanInstallCompose() {
					label := locales.EnvInstallCompose()
					if ups := st.Latest; ups != "" {
						label = locales.EnvInstallComposeVersion(ups)
					}
					if ui.PrimaryButton(c, label).Disabled(busy || st.Latest == "").Clicked() {
						a.runEnvArgs("env-compose", st.InstallComposeArgs)
					}
				} else {
					ui.Text(c, locales.EnvNoUpgrader()).FontSize(11).TextColor(t.Warning)
				}
			case st.ComposeOutdated():
				if ui.PrimaryButton(c, locales.EnvUpgradeCompose()).Disabled(busy).Clicked() {
					a.runEnvArgs("env-compose", st.UpgradeComposeArgs)
				}
			}

			if ui.Button(c, locales.ErrorRecheck()).Disabled(busy).Clicked() {
				a.detectEnvironment()
			}

			if busy {
				ui.Spinner(c).Size(12, 12)
			}
		})

		if st.Err != "" {
			ui.Text(c, st.Err).FontSize(11).TextColor(t.Warning).MaxWidth(560)
		}
	})
}

// envStatusIcon 画一个「已就绪 / 缺失」的状态圆点。
func (a *App) envStatusIcon(c *ui.Context, ok bool) {
	t := c.Theme()
	color := t.Success
	if !ok {
		color = t.Danger
	}
	ui.Icon(c, icon("dot")).FontSize(11).TextColor(color)
}

// detectEnvironment 在后台体检一次环境。
func (a *App) detectEnvironment() {
	if a.isBusy("env-detect") {
		return
	}
	a.setBusy("env-detect", true)
	a.logInfo("env", "开始检测依赖环境")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		st := envsetup.Detect(ctx)

		a.update(func() {
			a.env = st
			a.envChecked = true
			a.setBusy("env-detect", false)
			a.logInfo("env", "检测完成: wslc=%v compose=%v latest=%s upgrader=%s",
				st.WSLCOK(), st.ComposeOK(), st.Latest, st.Upgrader)
			if st.Err != "" {
				a.logWarn("env", "检测提示: %s", st.Err)
			}
		})
	}()
}

// runEnvArgs 执行一个返回 (bin, args) 的命令构造器。
//
// Go 不允许把多返回值展开到一个「前面还有参数」的调用里，
// 所以这里用一个函数值把 key 先绑好。
func (a *App) runEnvArgs(key string, build func() (string, []string)) {
	bin, args := build()
	a.runEnvCommand(key, bin, args)
}

// runEnvCommand 执行安装/升级命令，并把输出流式显示。
func (a *App) runEnvCommand(key string, bin string, args []string) {
	if bin == "" {
		return
	}
	if a.isBusy(key) {
		return
	}
	a.setBusy(key, true)

	title := locales.EnvInstalling(bin)
	a.outputTitle = title
	a.outputText = ""
	a.outputOpen = true
	a.outputBusy = true
	a.outputLines = 0
	a.logInfo("env", "执行 %s %v", bin, args)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, bin, args...)
		hideWindow(cmd)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			a.finishEnv(key, err.Error())
			return
		}
		cmd.Stderr = cmd.Stdout

		if err := cmd.Start(); err != nil {
			a.finishEnv(key, err.Error())
			return
		}

		// 逐行追加，用户能立刻看到进度。
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			a.update(func() {
				a.outputText += line + "\n"
				a.outputLines++
			})
		}
		waitErr := cmd.Wait()

		msg := locales.EnvInstallDone()
		if waitErr != nil {
			msg = locales.EnvInstallFailed(waitErr.Error())
		}
		a.finishEnv(key, msg)
	}()
}

// finishEnv 结束一次安装/升级：收尾面板、提示、重新检测。
func (a *App) finishEnv(key, msg string) {
	a.update(func() {
		a.outputBusy = false
		a.outputText += "\n" + msg
		a.setBusy(key, false)
		a.toast(msg)
	})
	a.logInfo("env", "%s", msg)
	// 环境变了，重新体检一次让界面反映真实状态。
	a.detectEnvironment()
	// 装完 wslc-compose 后，之前不可用的 compose 客户端要重建。
	a.Init()
	a.refreshProjects()
}

// envSummary 给诊断报告用的一行摘要。
func (a *App) envSummary() string {
	if !a.envChecked {
		return "尚未检测"
	}
	return fmt.Sprintf("wslc=%v compose=%v connect=%s",
		a.env.WSLCOK(), a.env.ComposeOK(), a.env.ComposeVersion)
}
