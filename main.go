// Command wslc-manager 是 WSL 容器（wslc）的图形化管理器。
//
// 目标：提供类似 Docker Desktop 的体验，但管理的是 wslc 会话里的
// 容器、镜像、卷、网络，以及基于 wslc-compose 的 Compose 项目编排。
//
// 界面用 MyGo 的原生 UI（package ui）绘制：纯 Go、无 webview、
// 单个可执行文件、启动即出界面。
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/app"
	"github.com/arno/wslc-manager/internal/selfupdate"
)

func main() {
	// --shot 是开发期调试开关：启动后自动把窗口渲染成 PNG，
	// 便于在没有人工介入、且不受其它窗口遮挡的情况下检查界面。
	// 也支持用环境变量 WSLC_MANAGER_SHOT 指定文件名（mygo dev 不转发参数）。
	shot := flag.String("shot", "", "把首帧渲染到指定 PNG 文件（调试用）")
	flag.Parse()

	a := app.New()
	// 截图延迟：首帧还没有后台数据，默认等 3 秒让数据到位。
	shotDelay := 3 * time.Second
	if v := os.Getenv("WSLC_MANAGER_SHOT_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			shotDelay = d
		}
	}
	// WSLC_MANAGER_SHOT_PAGE 让截图直接落在某一页（调试用）。
	if p := os.Getenv("WSLC_MANAGER_SHOT_PAGE"); p != "" {
		switch p {
		case "containers":
			a.ShootPage(app.PageContainers)
		case "images":
			a.ShootPage(app.PageImages)
		case "volumes":
			a.ShootPage(app.PageVolumes)
		case "networks":
			a.ShootPage(app.PageNetworks)
		case "compose":
			a.ShootPage(app.PageCompose)
		case "system":
			a.ShootPage(app.PageSystem)
		}
	}
	if os.Getenv("WSLC_MANAGER_SHOT_SETTINGS") != "" {
		a.OpenSettingsForShot()
	}
	if name := *shot; name != "" {
		a.ScheduleAutoShot(name, shotDelay)
	} else if name := os.Getenv("WSLC_MANAGER_SHOT"); name != "" {
		a.ScheduleAutoShot(name, shotDelay)
	}
	// 自更新：注册 MyGo 的原生更新插件，必须在 Run 之前。
	useSelfUpdate()

	// 后端探测（wslc / wslc-compose 路径、配置文件）必须在开窗前完成，
	// 这样首帧就能展示真实的可用状态。
	a.Init()

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "WSLC Manager",
			Width:     1180,
			Height:    760,
			MinWidth:  900,
			MinHeight: 560,
			// 记住窗口位置与尺寸
			StateKey: "main",
			Content:  ui.View(a.View),
		})
		a.AttachWindow(win)
		// 首帧之后立刻拉一次数据。
		a.Refresh()
		// 窗口建好之后再接上更新状态：插件的后台检查也是应用就绪后才开始（约 10 秒后），
		// 不占用启动路径。
		a.WatchUpdates()
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// useSelfUpdate 注册 MyGo 的原生更新插件。
//
// mygo.Use 在插件无效或 Setup 失败时会 panic。更新只是附属功能，
// 不能因为它没装上就让整个应用起不来，所以这里接住 panic 记一行日志继续启动。
// 注册失败时 selfupdate 的标志不会置位，界面上会显示「此版本不支持自动更新」，
// 而不是去调一个没装上的插件。
func useSelfUpdate() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("自更新插件注册失败，已跳过: %v", r)
		}
	}()
	mygo.Use(selfupdate.Plugin())
}
