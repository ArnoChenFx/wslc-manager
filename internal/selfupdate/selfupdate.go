// Package selfupdate 把 MyGo 自带的更新插件接进应用。
//
// 它只做一件事：在应用和 MyGo 的 updater 插件之间放一层很薄的适配。
//
// 边界：
//   - 负责：注册插件、读取更新状态、触发检查、修改两项偏好、订阅变化；
//   - 不负责：画界面（在 internal/app）、签名与发布（在 mygo.json 和
//     .github/workflows/release.yml）、发现新版本时弹出的更新窗口
//     （由 MyGo 的 native 更新插件绘制，带更新说明、进度条，
//     以及「稍后提醒 / 跳过此版本」）。
//
// 为什么不在界面里直接调 updater 包，而要隔一层：
//  1. updater 的每个函数在插件没注册时都会 panic（"the plugin is not used"），
//     而单元测试、go run 之类的场景从来没有注册过它；
//  2. mygo.Updater.Enabled 会在安装目录里建一个临时目录来探测能否写入，
//     是磁盘 IO，界面不能每帧去问，所以对外给的是一份可以缓存的快照（Status）；
//  3. 界面测试要换成假实现，不能真的联网，也不能弹出窗口。
package selfupdate

import (
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
)

// Status 是更新相关状态的一份快照。
//
// 它是普通数据，可以放心存进 App 的字段、在视图里反复读取。
type Status struct {
	// Version 是当前运行的版本。
	//
	// 来自 mygo.json 的 version，由 mygo build 写进可执行文件；发布流程会把它
	// 改成 tag 的版本号。go run / go test 这类没有打包的场景为空。
	Version string
	// Supported 表示这个构建能不能自己更新。
	//
	// 开发版本、没带更新配置的构建、以及程序所在目录写不进去的安装
	// （比如被放进了 Program Files）都是 false。此时插件不会在后台检查，
	// 手动检查时更新窗口会说明原因。
	Supported bool
	// Available 是最近一次检查发现的新版本，没有就是空串。
	//
	// 用户选择「跳过此版本」后它也是空串——这是插件的约定，
	// 手动点「检查更新」仍会把被跳过的版本重新列出来。
	Available string
	// LastCheck 是最近一次成功检查的时间，零值表示从未成功检查过。
	LastCheck time.Time
	// AutoCheck 表示是否在后台自动检查（启动后一次，之后每天一次）。
	AutoCheck bool
	// AutoInstall 表示发现新版本后是否不再询问、自动下载并在下次启动时安装。
	AutoInstall bool
}

// Controller 是应用与更新插件之间的全部接口。
//
// 界面只依赖它，因此测试可以换成假实现。
type Controller interface {
	// Status 读取当前状态。它会做磁盘 IO（探测安装目录是否可写），
	// 不要在视图里调用；调用方应缓存结果，在 OnChange 通知时再刷新。
	Status() Status
	// CheckNow 按用户的意思立即检查：更新窗口马上弹出，
	// 检查中、已是最新、检查失败、有新版本都在那个窗口里说明。
	// 它立即返回，检查在后台进行。
	CheckNow()
	// SetAutoCheck 打开或关闭后台自动检查。
	SetAutoCheck(on bool)
	// SetAutoInstall 打开或关闭「自动下载并安装更新」。
	SetAutoInstall(on bool)
	// OnChange 订阅状态变化：一次检查成功、用户在更新窗口里做了选择、
	// 或偏好被修改之后调用 fn。fn 在主线程执行。返回取消订阅的函数。
	OnChange(fn func()) (off func())
}

// registered 记录更新插件是否已经注册成功。
//
// updater 包在插件没注册时调用任何函数都会 panic，所以真实实现的每个方法
// 都先看这个标志：没注册就退化成「什么都不做 / 返回零值」。
var registered atomic.Bool

// Plugin 返回要交给 mygo.Use 的更新插件。必须在 mygo.App.Run 之前调用。
//
// 用 native 版本而不是 updater.Plugin：后者的更新窗口是网页，
// Windows 10 上需要 WebView2 运行时；本应用是纯原生界面、不带 webview，
// native 版本用同样的内容画原生窗口，不引入新的运行时依赖。
//
// 选项全用默认值，各自的缘由：
//   - 窗口语言留空：跟随 App.Locale，也就是设置里选的界面语言；
//   - 图标留空：用资源目录里的 icon.png，也就是应用自己的图标；
//   - 检查间隔默认一天，启动后约 10 秒做第一次检查，不占用启动路径。
func Plugin() mygo.Plugin {
	p := native.New(updater.Options{})
	p.Setup = markRegistered(p.Setup, &registered)
	return p
}

// markRegistered 包装插件的 Setup：内层成功之后才把 flag 置位。
//
// 内层失败时不能置位——否则界面会去调用一个根本没装上的插件而 panic。
func markRegistered(setup func() error, flag *atomic.Bool) func() error {
	return func() error {
		if setup != nil {
			if err := setup(); err != nil {
				return err
			}
		}
		flag.Store(true)
		return nil
	}
}

// New 返回连接真实更新插件的 Controller。
//
// 插件没注册时（测试、go run）它依然安全：状态为零值，操作都是空操作。
func New() Controller { return controller{} }

// controller 是 Controller 的真实实现，没有自己的状态，全部委托给 updater 包。
type controller struct{}

func (controller) Status() Status {
	s := Status{Version: mygo.App.Version()}
	if !registered.Load() {
		return s
	}
	s.Supported = mygo.Updater.Enabled()
	s.Available = updater.AvailableVersion()
	s.LastCheck = updater.LastCheck()
	s.AutoCheck = updater.AutomaticChecks()
	s.AutoInstall = updater.AutomaticDownloads()
	return s
}

func (controller) CheckNow() {
	if registered.Load() {
		updater.CheckForUpdates()
	}
}

func (controller) SetAutoCheck(on bool) {
	if registered.Load() {
		updater.SetAutomaticChecks(on)
	}
}

func (controller) SetAutoInstall(on bool) {
	if registered.Load() {
		updater.SetAutomaticDownloads(on)
	}
}

func (controller) OnChange(fn func()) (off func()) {
	if !registered.Load() {
		return func() {}
	}
	return updater.OnChange(fn)
}
