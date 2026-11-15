package app

// 本文件是「演示模式」：让宣传片和截图渲染应用**真实的界面**，而不是另画一张仿制品。
//
// 为什么要这样做：
//   - 仿制品会过时。界面一改，截图和宣传片就和产品对不上；
//     直接用真实的根视图（App.View）渲染，宣传素材重新生成一遍就跟上了。
//   - 中英双语、深浅色、强调色都是真实视图本来就支持的，不用另做一份。
//
// 演示实例的约束（这些是它与正常运行的全部区别）：
//   - **不连后端**：不经过 Init，wslc、Compose 引擎、设置文件、依赖体检、更新插件一概不碰。
//     所以演示出来的数据全是调用方给的，与这台机器上有什么容器无关，
//     也不会把本机的路径、容器名泄露进宣传素材；
//   - **不起后台协程**：没有 Refresh、没有轮询，视图每一帧只读状态，
//     于是「同一帧构建多次结果相同」，这正是渲染成视频所要求的；
//   - **只有数据和「选中/打开」这类状态由调用方给**：控件自己的内部状态
//     （滚动位置、列表状态）仍归应用管，不在这里重置。
//
// 边界：这里只提供机制。演示用的数据集和剧情（哪一帧显示什么）在 cmd/promo，
// 不在应用里——应用不该背着宣传内容。

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/envsetup"
	"github.com/arno/wslc-manager/internal/selfupdate"
	"github.com/arno/wslc-manager/internal/store"
	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/internal/wslvm"
)

// NoRow 表示「不选中任何一行」。
//
// 选中行的零值是第 0 行，容易在没写这个字段时悄悄选中第一行，
// 所以场景一律从 NewDemoScene 起步，那里把所有选中行设成 NoRow。
const NoRow = -1

// DemoOutput 是演示里输出面板（日志、拉取进度、编排输出）的内容。
type DemoOutput struct {
	// Title 是面板标题，调用方按界面语言自己拼（和真实界面一样用 locales 的词条）。
	Title string
	// Text 是面板里的全部文字，一次给全：「逐行冒出来」是调用方按帧截取出来的。
	Text string
	// Busy 为 true 时面板显示「运行中」转圈与行数，并把滚动跟到末尾，
	// 和真实的流式输出一样；为 false 表示已结束。
	Busy bool
}

// DemoVMAction 是系统页虚拟机卡片上正在进行的动作。
type DemoVMAction int

const (
	// DemoVMIdle 没有动作在进行。
	DemoVMIdle DemoVMAction = iota
	// DemoVMStarting 正在启动：状态行显示转圈与「正在启动」，两个按钮都禁用。
	DemoVMStarting
	// DemoVMStopping 正在关闭。
	DemoVMStopping
)

// DemoScene 是演示实例在某一帧要显示的全部内容。
//
// 它是一份完整的描述而不是「增量」：每一帧都整份灌进去，所以画面只取决于场景本身，
// 与前面画过什么无关。这是渲染成视频的前提（视图拿同一帧必须得到同一个结果）。
// 请用 NewDemoScene 创建，别直接用零值：零值会选中每个列表的第 0 行。
type DemoScene struct {
	// Page 是当前页面，Query 是顶部搜索框里的文字，HideStopped 是容器页「隐藏已停止」。
	Page        Page
	Query       string
	HideStopped bool

	// 各页面的数据。容器带 compose 标签（com.wslc-compose.project / .service），
	// Compose 页项目头部的「运行 n/m」就是按它数出来的，和真实运行时一致。
	Containers []wslc.Container
	Images     []wslc.Image
	Volumes    []wslc.Volume
	Networks   []wslc.Network
	Projects   []compose.Project
	// Sessions 和 Info 是系统页的会话表与环境信息。
	Sessions []wslc.Session
	Info     wslc.Info

	// 各列表选中的行（NoRow 为不选）。注意它是「过滤之后那份列表」里的序号，
	// 和真实界面一致：搜索框里有字时，行号按过滤结果数。
	ContainerRow, ImageRow, VolumeRow, NetworkRow, ProjectRow, ServiceRow int

	// Output 不为 nil 时打开输出面板；OutputPercent 是表格占上半栏的百分比，0 表示默认。
	Output        *DemoOutput
	OutputPercent float32

	// ComposeBusyDir 非空表示这个项目目录上有编排动作在执行，
	// 项目详情的「启动」按钮会显示「执行中…」并禁用。
	ComposeBusyDir string

	// VM 是虚拟机的观测快照，VMAction 是正在进行的启动 / 关闭。
	VM       wslvm.Snapshot
	VMAction DemoVMAction

	// Update 是应用更新的状态快照，系统页的更新卡片和侧边栏的「新」徽标读它。
	Update selfupdate.Status

	// Accent 是强调色标识（magenta / violet / blue / teal / amber / green），空为默认。
	Accent string

	// 状态栏：两个版本号、上次刷新时间（零值显示「尚未刷新」）、是否正在加载。
	WSLCVersion    string
	ComposeVersion string
	UpdatedAt      time.Time
	Loading        bool
}

// NewDemoScene 返回一个空白场景：容器页、没有数据、不选任何行、不开输出面板。
func NewDemoScene() DemoScene {
	return DemoScene{
		Page:         PageContainers,
		ContainerRow: NoRow, ImageRow: NoRow, VolumeRow: NoRow,
		NetworkRow: NoRow, ProjectRow: NoRow, ServiceRow: NoRow,
	}
}

// Demo 是一个脱离后端的应用实例，专给宣传片和截图用。
//
// 它不是并发安全的：渲染视频时所有帧都在同一个协程里顺序构建，这里就不加锁。
type Demo struct {
	a *App
}

// NewDemo 创建演示实例。width、height 是窗口内容区的尺寸（DIP）。
//
// 需要尺寸是因为有些布局要问「窗口多大」（见 App.viewSize）——例如 Compose 详情里
// 分隔条的上限。真实运行时问窗口，演示里没有窗口，就用这里给的值。
func NewDemo(width, height float32) *Demo {
	a := New()
	a.sizeOverride = [2]float32{width, height}

	// 不经过 Init。Init 会读设置文件、探测本机的 wslc、启动后台体检，
	// 演示必须与这台机器无关。设置用空值：不带任何扫描根目录与项目，
	// 万一哪里想扫盘也无处可扫。
	a.settings = store.Settings{SidebarWidth: 220, PollSeconds: 0}

	// 依赖体检结果固定为「一切就绪」，并标记已检测过：
	// 系统页的依赖卡片据此显示绿色的就绪状态，而不是「尚未检测」。
	// 路径用 wslc 的标准安装位置，不是本机实际路径。
	a.envChecked = true
	a.env = envsetup.Status{
		WSLCPath:   `C:\Program Files\WSL\wslc.exe`,
		WSLVersion: "2.6.1.0",
		CheckedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	return &Demo{a: a}
}

// View 先把场景灌进应用状态，再用应用真实的根视图画一帧。
//
// 必须在视图构建期间调用（也就是渲染合成的 View 回调里）。
func (d *Demo) View(c *ui.Context, s DemoScene) {
	d.apply(s)
	d.a.View(c)
}

// apply 把场景写进应用的状态。
//
// 只动「数据」和「选中 / 打开」这类状态。ListState、ScrollState 这些控件自己的内部状态
// 一概不碰：重置它们等于每一帧都把滚动位置拉回开头，输出面板「滚到最新一行」就废了。
func (d *Demo) apply(s DemoScene) {
	a := d.a

	a.page = s.Page
	a.query = s.Query
	a.hideStopped = s.HideStopped

	a.containers, a.images, a.volumes, a.networks = s.Containers, s.Images, s.Volumes, s.Networks
	a.projects, a.sessions, a.info = s.Projects, s.Sessions, s.Info
	a.wslcVer, a.composeVer = s.WSLCVersion, s.ComposeVersion

	a.containerSel, a.imageSel, a.volumeSel = s.ContainerRow, s.ImageRow, s.VolumeRow
	a.networkSel, a.projectSel, a.serviceSel = s.NetworkRow, s.ProjectRow, s.ServiceRow

	// 输出面板。行数按文字里的换行数，和真实流式输出「已收到 n 行」的含义一致。
	if o := s.Output; o != nil {
		a.outputOpen = true
		a.outputTitle, a.outputText, a.outputBusy = o.Title, o.Text, o.Busy
		a.outputLines = 0
		if strings.TrimSpace(o.Text) != "" {
			a.outputLines = strings.Count(strings.TrimRight(o.Text, "\n"), "\n") + 1
		}
	} else {
		a.outputOpen, a.outputTitle, a.outputText, a.outputBusy, a.outputLines = false, "", "", false, 0
	}
	if s.OutputPercent > 0 {
		a.outputPercent = s.OutputPercent
	}

	// 忙碌标记：先清空再按场景设置，保证每一帧只由场景决定。
	// 编排动作的键只要求以 compose- 开头、以项目目录结尾（见 composeBusy）。
	a.busyMu.Lock()
	for k := range a.busy {
		delete(a.busy, k)
	}
	a.busyMu.Unlock()
	if s.ComposeBusyDir != "" {
		a.setBusy("compose-up-"+s.ComposeBusyDir, true)
	}
	switch s.VMAction {
	case DemoVMStarting:
		a.setBusy(vmStartKey, true)
	case DemoVMStopping:
		a.setBusy(vmStopKey, true)
	}

	a.vmx.snap = s.VM
	a.upd = s.Update
	a.settings.Accent = s.Accent

	a.lastRefresh = s.UpdatedAt
	a.loading = s.Loading
}
