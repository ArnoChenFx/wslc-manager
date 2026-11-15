// Package app 存放应用状态与视图。
//
// 设计约定（遵循 MyGo 的原生 UI 模型）：
//   - 视图 view(c) 是「状态 → 界面」的纯函数，可在同一帧内被多次调用，
//     因此它只读状态、只把用户操作记成「意图」，不在里面做 IO。
//   - 所有慢操作（调用 wslc / wslc-compose）都在后台 goroutine 里执行，
//     结果通过 Window.Update 回到主线程写回状态。
//   - 视图里不保存 Element 或任何构建期值，只保存普通数据与控件状态。
package app

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/arno/wslc-manager/internal/compose"
	"github.com/arno/wslc-manager/internal/composert"
	"github.com/arno/wslc-manager/internal/envsetup"
	"github.com/arno/wslc-manager/internal/selfupdate"
	"github.com/arno/wslc-manager/internal/store"
	"github.com/arno/wslc-manager/internal/wslc"
	"github.com/arno/wslc-manager/locales"
)

// Page 是侧边栏里可选的主页面。
type Page int

const (
	PageContainers Page = iota
	PageImages
	PageVolumes
	PageNetworks
	PageCompose
	PageSystem
)

// pageInfo 描述一个页面在侧边栏里的展示信息。
type pageInfo struct {
	Page  Page
	Title string
	Icon  string // SVG 资源名，见 icons.go
}

// pages 是侧边栏的条目顺序。
// 标题在渲染时从 locales 取，因此切换语言后侧边栏立刻跟着变。
var pages = []pageInfo{
	{PageContainers, "", "box"},
	{PageImages, "", "layers"},
	{PageVolumes, "", "database"},
	{PageNetworks, "", "share"},
	{PageCompose, "", "stack"},
	{PageSystem, "", "cpu"},
}

// title 返回页面标题；每次调用都读当前语言，不缓存。
func (p pageInfo) title() string {
	switch p.Page {
	case PageContainers:
		return locales.NavContainers()
	case PageImages:
		return locales.NavImages()
	case PageVolumes:
		return locales.NavVolumes()
	case PageNetworks:
		return locales.NavNetworks()
	case PageCompose:
		return locales.NavCompose()
	case PageSystem:
		return locales.NavSystem()
	}
	return ""
}

// App 是应用的全部状态。
//
// 字段分组：设置、后端客户端、数据快照、界面状态、后台任务。
type App struct {
	// ---- 后端 ----
	wslc    *wslc.Client
	compose *compose.Client
	store   *store.Store
	win     *mygo.Window

	// pullStream 是流式拉取镜像的入口。默认指向 wslc 客户端；
	// 测试可替换它来验证「进度实时显示」而不真的联网。
	pullStream func(ctx context.Context, ref string, onLine func(string)) (wslc.Result, error)

	// composeStream 是流式执行编排动作的入口。
	// 默认走 wslc-compose；测试可替换它来验证进度反馈。
	composeStream func(ctx context.Context, dir string, action compose.Action, opt compose.ActionOptions, onLine func(string)) (compose.Result, error)

	// 后端可用性（启动时探测一次）。
	wslcErr    string
	composeErr string

	// logs 是运行日志的环形缓冲，可在任意协程写入。
	// 系统信息页展示它，诊断报告也包含它。
	logs *logBuffer
	// logScroll 保存日志面板的滚动位置，logFollow 表示跟随末尾。
	logScroll ui.ScrollState
	logFollow bool
	// diagPath 是上一次导出的诊断报告路径。
	diagPath string
	// pendingClipboard 待写入剪贴板的文本（在主线程消费）。
	pendingClipboard string

	// ---- 持久化设置 ----
	settings store.Settings
	// saveWG 记录还没写完的异步保存。测试结束时要等它们写完再清理临时目录，
	// 否则写盘协程和清理抢文件，会偶发「The directory is not empty」。
	saveWG sync.WaitGroup

	// ---- 数据快照（由后台刷新写入）----
	containers []wslc.Container
	images     []wslc.Image
	volumes    []wslc.Volume
	networks   []wslc.Network
	sessions   []wslc.Session
	info       wslc.Info
	wslcVer    string
	composeVer string
	projects   []compose.Project

	// ---- 加载与错误 ----
	loading     bool
	lastRefresh time.Time
	lastError   string

	// ---- 依赖环境 ----
	// env 是最近一次体检结果；envChecked 表示是否已经检测过。
	env        envsetup.Status
	envChecked bool

	// ---- 应用自更新 ----
	// selfUpdate 是与 MyGo 更新插件之间的接口，默认指向真实实现；
	// 测试替换成假的，避免联网和弹出窗口。
	selfUpdate selfupdate.Controller
	// upd 是更新状态的快照，视图只读它。
	//
	// 不让视图直接问插件：读状态要探测安装目录是否可写（磁盘 IO），
	// 每帧都调就会拖慢界面。快照在启动时、插件通知变化时刷新（见 refreshUpdateStatus）。
	upd selfupdate.Status
	// updSeq 给每次刷新编号，晚到的旧结果据此丢弃，避免覆盖更新的状态。
	updSeq int
	// updOff 取消对插件变化通知的订阅。
	updOff func()

	// ---- 虚拟机 ----
	// vmx 是虚拟机的状态与依赖，见 vmcard.go。
	vmx vmState

	// ---- 表格宽度 ----
	// 表格自己占多宽，要问引擎上一帧的布局结果（tablefit.go 的 tableWidth）。
	// 不能自己拿窗口宽度去减：表格在分栏里、还可能有内边距，减错了会算多，
	// 算多就会把最后一列挤出可见范围。
	containerTableH ui.Handle
	serviceTableH   ui.Handle
	imageTableH     ui.Handle
	volumeTableH    ui.Handle
	networkTableH   ui.Handle

	// ---- 界面状态 ----
	page        Page
	query       string // 顶部搜索框
	hideStopped bool
	sidebarW    float32

	containerSel  int
	imageSel      int
	volumeSel     int
	networkSel    int
	projectSel    int
	serviceSel    int
	containerList ui.ListState
	imageList     ui.ListState
	volumeList    ui.ListState
	networkList   ui.ListState
	projectList   ui.ListState
	serviceList   ui.ListState

	// 容器输出面板（日志/检查/统计）。
	outputTitle string
	outputText  string
	outputOpen  bool
	outputBusy  bool
	// outputPercent 是表格与输出面板之间分隔条的位置，
	// 表示上半栏（表格）占可用高度的百分比（0-100）。
	//
	// 用百分比而不是像素：像素值在窗口变矮或用户拖到极限时会超出
	// 可用高度，把日志面板整个推到窗口之外，滚动条也跟着滚到看不见的
	// 区域（用户截图确认过）。百分比天然不会越界。
	outputPercent float32

	// composeListWidth 是 Compose 详情里「服务表 / 启动选项」之间
	// 分隔条的宽度（服务表那一侧）。可拖动，并写入设置。
	composeListWidth float32

	// composeProjectsWidth 是「项目列表 / 项目详情」之间分隔条的宽度
	// （项目列表那一侧）。可拖动，并写入设置。
	composeProjectsWidth float32
	// outputLines 是流式输出已收到的行数，用于状态提示。
	outputLines int
	// outputScroll 保存输出面板的滚动位置，配合 FollowEnd 让新行自动可见。
	outputScroll ui.ScrollState

	// autoShot 非空时，每次视图构建都会把窗口写成该文件名的 PNG，
	// 并在写完后清空自身（只在 --shot 调试模式下使用）。
	autoShot string

	// 项目动作选项。
	upDetach    bool
	upBuild     bool
	downVolumes bool
	dryRun      bool

	// 对话框。
	confirmOpen   bool
	confirmTitle  string
	confirmBody   string
	confirmAction func()
	confirmDanger bool

	addProjectOpen bool
	addProjectPath string

	// 设置对话框里的临时文本字段。
	pollSecondsText string
	scanDepthText   string
	newScanRoot     string

	settingsOpen bool

	// 拉刷新镜像对话框。
	pullOpen bool
	pullRef  string

	// 新建卷 / 网络对话框。
	newVolumeOpen  bool
	newVolumeName  string
	newNetworkOpen bool
	newNetworkName string

	// ---- 后台任务 ----
	// 用互斥量保护「刷新中」标记，避免重入。
	refreshMu sync.Mutex
	// projectLoadSeq 用于丢弃过期的项目加载结果。
	projectLoadSeq int
	busy           map[string]bool
	// busyMu 保护 busy：它在视图（主线程）里被读，
	// 也可能在后台协程里被写，必须加锁。
	busyMu sync.Mutex
	// viewMu 在无窗口模式下串行化所有状态写入与读取，避免数据竞争。
	// 无窗口时没有真正的主线程，所以用一把互斥锁来充当主线程。
	viewMu sync.Mutex
	// muOwner / owner 记录当前持锁协程，用于识别同协程重入。
	muOwner sync.Mutex
	owner   uint64

	// pendingToasts 是等待在下一帧弹出的提示消息。
	//	 ui.Context.Toast 只能在构建期调用，所以后台任务把消息放这里，
	//	 由视图在构建时消费并真正弹出。
	pendingToasts []string
}

// New 创建应用实例。
func New() *App {
	a := &App{
		logs:         newLogBuffer(logBufferSize),
		selfUpdate:   selfupdate.New(),
		page:         PageContainers,
		upDetach:     true,
		busy:         map[string]bool{},
		hideStopped:  false,
		sidebarW:     220,
		containerSel: -1,
		imageSel:     -1,
		volumeSel:    -1,
		networkSel:   -1,
		projectSel:   -1,
		serviceSel:   -1,
	}
	// 默认让表格占 58%，下面留给日志。
	a.outputPercent = 58
	// 服务表与启动选项的默认分界：给选项栏留足宽度，
	// 否则描述文字会逐词换行（"Equival / ent to / up -d"）。
	a.composeListWidth = 400
	a.composeProjectsWidth = 280
	a.logFollow = true
	return a
}

// Init 初始化后端客户端与设置。必须在创建窗口之前调用。
func (a *App) Init() {
	a.store = store.New("")
	if s, err := a.store.Load(); err == nil {
		a.settings = s
		a.hideStopped = s.HideStopped
		if s.SidebarWidth > 0 {
			a.sidebarW = s.SidebarWidth
		}
		if s.ComposeListWidth > 0 {
			a.composeListWidth = s.ComposeListWidth
		}
		if s.ComposeProjectsWidth > 0 {
			a.composeProjectsWidth = s.ComposeProjectsWidth
		}
	} else {
		a.settings = store.DefaultSettings()
	}

	// 首次依赖体检。
	//
	// 放在 Init 而不是视图里：视图必须是无副作用的，
	// 且检测结果的异步回填会改变卡片高度，渲染中途变化会让布局抖动。
	a.detectEnvironment()

	// 恢复上次选定的界面语言。
	// MyGo 自己也记得选择，但设置文件是更可靠的一份：它跟着应用数据目录走，
	// 而 MyGo 的存储位置随平台不同。只在用户明确选过时才覆盖。
	if lang := a.settings.Language; lang != "" && mygo.App.IsReady() {
		mygo.App.SetLocale(lang)
	}

	a.logInfo("app", "启动 WSLC Manager")

	if c, err := wslc.New(); err != nil {
		a.wslcErr = err.Error()
		a.logError("wslc", "定位 wslc 失败: %v", err)
	} else {
		a.wslc = c
		a.pullStream = c.PullImageStream
		a.logInfo("wslc", "可执行文件: %s", c.Binary())
		// 每条 wslc 命令都记一行：参数、退出码、耗时。
		// 排查「点了没反应」时，第一件事就是看这里有没有真的发出命令。
		c.SetRunHook(func(r wslc.Result) {
			src := "wslc"
			if !r.Ok() {
				a.logError(src, "%s → 退出码 %d（%s）",
					strings.Join(r.Args, " "), r.ExitCode, r.Duration.Round(time.Millisecond))
				if msg := strings.TrimSpace(r.Stderr); msg != "" {
					a.logError(src, "  stderr: %s", firstLine(msg))
				}
			} else {
				a.logDebug(src, "%s → 0（%s）",
					strings.Join(r.Args, " "), r.Duration.Round(time.Millisecond))
			}
		})
	}

	// Compose 引擎是内置的，不再需要定位外部可执行文件；它唯一的前提是 wslc 可用，
	// 所有命令都经 wslc 客户端发出（共用同一把串行锁）。
	// wslc 找不到时，Compose 页展示的就是 wslc 缺失的错误。
	if a.wslc == nil {
		a.composeErr = a.wslcErr
		a.logError("compose", "Compose 引擎不可用：wslc 不可用")
		return
	}
	c := compose.New(composert.New(a.wslc))
	a.compose = c
	a.composeStream = c.RunActionStream
	a.logInfo("compose", "内置引擎 %s", compose.Version)
	// 每个编排动作结束后记一行：动作、退出码、耗时。
	// 具体执行了哪些 wslc 命令，由 wslc 客户端的命令日志记录（上面的 SetRunHook）。
	c.SetRunHook(func(r compose.Result) {
		src := "compose"
		if !r.Ok() {
			a.logError(src, "%s → 退出码 %d（%s）",
				strings.Join(r.Args, " "), r.ExitCode, r.Duration.Round(time.Millisecond))
			if msg := strings.TrimSpace(r.Stderr); msg != "" {
				a.logError(src, "  stderr: %s", firstLine(msg))
			}
		} else {
			a.logDebug(src, "%s → 0（%s）",
				strings.Join(r.Args, " "), r.Duration.Round(time.Millisecond))
		}
	})
}

// AttachWindow 绑定主窗口，之后后台任务才能把结果送回主线程。
func (a *App) AttachWindow(w *mygo.Window) { a.win = w }

// update 在窗口存在时把 fn 调度到主线程执行并重绘。
// update 在窗口存在时把 fn 调度到主线程执行并重绘。
//
// 没有窗口时（单元测试、启动早期）直接执行 fn：
// 早先这里直接 return，导致测试里任何“操作 → 写状态”的链路都静默失效，
// 也让真实缺陷难以被发现。
func (a *App) update(fn func()) {
	if a.win != nil {
		// 有窗口时交给主循环：它保证在 UI 线程、且不会重入。
		a.win.Update(fn)
		return
	}
	// 无窗口（单元测试、启动早期）：同步执行，用互斥锁充当 UI 线程。
	a.runGuarded(fn)
}

// runGuarded 在视图锁下执行 fn，并允许同一协程内的重入。
//
// 重入检测用协程 ID：update 的回调里可能再次调用 update（例如 toast），
// 若不识别就会自己锁死。无窗口模式只有测试会用，开销可忽略。
func (a *App) runGuarded(fn func()) {
	gid := goroutineID()
	a.muOwner.Lock()
	if a.owner == gid {
		a.muOwner.Unlock()
		fn()
		return
	}
	a.muOwner.Unlock()

	a.viewMu.Lock()
	a.muOwner.Lock()
	prev := a.owner
	a.owner = gid
	a.muOwner.Unlock()
	defer func() {
		a.muOwner.Lock()
		a.owner = prev
		a.muOwner.Unlock()
		a.viewMu.Unlock()
	}()
	fn()
}

// ViewLocked 在持有视图锁的情况下执行 fn。
//
// 供测试在读取 App 状态时使用，避免与后台协程写入竞争。
// 正常运行时视图已经跑在主线程上，不需要调用它。
func (a *App) ViewLocked(fn func()) {
	a.runGuarded(fn)
}

// toast 显示一条提示。
//
// 后台任务不能直接调用 ui.Context.Toast（它只能在构建期用），
// 因此把消息放进入待弹出队列，视图在下一帧取走。
func (a *App) toast(msg string) {
	a.update(func() {
		a.addToastLocked(msg)
	})
}

// addToastLocked 把消息加入待弹出队列。
//
// 它不自己调用 update：调用方通常已经在 update 回调（也就是视图锁）里，
// 再套一层会重复加锁而死锁。
func (a *App) addToastLocked(msg string) {
	a.pendingToasts = append(a.pendingToasts, msg)
}

// saveSettings 异步保存设置。
func (a *App) saveSettings() {
	if a.store == nil {
		return
	}
	s := a.settings
	s.HideStopped = a.hideStopped
	s.SidebarWidth = a.sidebarW
	a.saveWG.Add(1)
	go func() {
		defer a.saveWG.Done()
		_ = a.store.Save(s)
	}()
}

// waitSaves 等待所有已发起的异步保存写完。只给测试用。
func (a *App) waitSaves() { a.saveWG.Wait() }

// ---- 后台任务 ----

// Refresh 在后台刷新全部数据快照。
//
// 多处触发时只会有一个刷新在跑，其余的立即返回。
func (a *App) Refresh() {
	if a.wslc == nil {
		return
	}
	if !a.refreshMu.TryLock() {
		return
	}
	a.update(func() { a.loading = true })

	go func() {
		defer a.refreshMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		// 虚拟机确定没在运行时，不去问容器、镜像、卷、网络：那些查询会把它拉起来。
		if a.refreshGated(ctx) {
			return
		}

		type snapshot struct {
			containers []wslc.Container
			images     []wslc.Image
			volumes    []wslc.Volume
			networks   []wslc.Network
			sessions   []wslc.Session
			info       wslc.Info
			wslcVer    string
			composeVer string
			errs       []string
		}
		var snap snapshot

		var wg sync.WaitGroup
		// 注意：wslc.Client 内部已串行化，这里的并发只是让代码结构清晰，
		// 实际仍是一条条执行。
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := a.wslc.ListContainers(ctx, true); err != nil {
				snap.errs = append(snap.errs, "容器: "+err.Error())
			} else {
				snap.containers = v
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := a.wslc.ListImages(ctx, false); err != nil {
				snap.errs = append(snap.errs, "镜像: "+err.Error())
			} else {
				snap.images = v
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := a.wslc.ListVolumes(ctx); err != nil {
				snap.errs = append(snap.errs, "卷: "+err.Error())
			} else {
				snap.volumes = v
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := a.wslc.ListNetworks(ctx); err != nil {
				snap.errs = append(snap.errs, "网络: "+err.Error())
			} else {
				snap.networks = v
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := a.wslc.Info(ctx); err == nil {
				snap.info = v
			}
			if v, err := a.wslc.Version(ctx); err == nil {
				snap.wslcVer = v
			}
			if v, err := a.wslc.ListSessions(ctx); err == nil {
				snap.sessions = v
			}
		}()
		if a.compose != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if v, err := a.compose.Version(ctx); err == nil {
					snap.composeVer = v
				}
			}()
		}
		wg.Wait()

		a.logInfo("refresh", "容器 %d / 镜像 %d / 卷 %d / 网络 %d / 项目 %d",
			len(snap.containers), len(snap.images), len(snap.volumes),
			len(snap.networks), len(a.projects))

		a.update(func() {
			a.containers = snap.containers
			a.images = snap.images
			a.volumes = snap.volumes
			a.networks = snap.networks
			a.sessions = snap.sessions
			a.info = snap.info
			a.wslcVer = snap.wslcVer
			a.composeVer = snap.composeVer
			a.loading = false
			a.lastRefresh = time.Now()
			if len(snap.errs) > 0 {
				a.lastError = strings.Join(snap.errs, "；")
				a.logWarn("refresh", "%s", a.lastError)
			} else {
				a.lastError = ""
			}
			a.clampSelections()
		})

		// 数据刷新后顺带刷新已加载的 compose 项目内容。
		a.refreshProjects()
	}()
}

// refreshProjects 重新解析所有已知项目。
func (a *App) refreshProjects() {
	if a.compose == nil {
		return
	}
	dirs := append([]string{}, a.settings.Projects...)
	a.projectLoadSeq++
	seq := a.projectLoadSeq

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		loaded := make([]compose.Project, 0, len(dirs))
		for _, d := range dirs {
			p, err := a.compose.LoadProject(ctx, d)
			if err != nil {
				// 项目目录被删或 compose 文件有语法错误：保留一个占位项，
				// 让用户能在界面上看到它并移除/修复。
				loaded = append(loaded, compose.Project{Dir: d, Name: baseName(d), Err: err.Error()})
				continue
			}
			loaded = append(loaded, p)
		}
		sort.SliceStable(loaded, func(i, j int) bool {
			return strings.ToLower(loaded[i].Name) < strings.ToLower(loaded[j].Name)
		})

		a.update(func() {
			// 丢弃过期结果：用户可能在加载期间又添加/移除了项目。
			if seq != a.projectLoadSeq {
				return
			}
			a.projects = loaded
			if a.projectSel >= len(a.projects) {
				a.projectSel = len(a.projects) - 1
			}
			if a.serviceSel >= a.serviceCount() {
				a.serviceSel = -1
			}
		})
	}()
}

func (a *App) serviceCount() int {
	if a.projectSel < 0 || a.projectSel >= len(a.projects) {
		return 0
	}
	return len(a.projects[a.projectSel].Config.Services)
}

// DiscoverProjects 扫描扫描根目录下的 compose 项目并合并进设置。
func (a *App) DiscoverProjects() {
	roots := append([]string{}, a.settings.ScanRoots...)
	depth := a.settings.ScanDepth

	a.update(func() { a.loading = true })
	go func() {
		found := compose.Scan(compose.ScanOptions{Roots: roots, Depth: depth})
		dirs := make([]string, 0, len(found))
		for _, f := range found {
			dirs = append(dirs, f.Dir)
		}
		a.update(func() {
			merged := mergeDirs(a.settings.Projects, dirs)
			a.settings.Projects = merged
			a.loading = false
			a.saveSettings()
			a.toast(locales.ComposeScanDone(len(merged)))
		})
		a.refreshProjects()
	}()
}

// AddProject 手动添加一个项目目录。
func (a *App) AddProject(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	if !compose.IsComposeDir(dir) {
		a.toast(locales.ComposeNoComposeFile())
		return
	}
	a.settings.Projects = mergeDirs(a.settings.Projects, []string{dir})
	a.saveSettings()
	a.refreshProjects()
}

// RemoveProject 从设置中移除一个项目（不删除磁盘文件）。
func (a *App) RemoveProject(dir string) {
	keep := make([]string, 0, len(a.settings.Projects))
	for _, d := range a.settings.Projects {
		if !strings.EqualFold(d, dir) {
			keep = append(keep, d)
		}
	}
	a.settings.Projects = keep
	a.saveSettings()
	a.refreshProjects()
}

// mergeDirs 合并两个目录列表并去重（忽略大小写）。
func mergeDirs(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, d := range list {
			key := strings.ToLower(strings.TrimRight(d, `\/`))
			if d == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, d)
		}
	}
	return out
}

// baseName 返回路径的最后一段，兼容 Windows 分隔符。
func baseName(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// clampSelections 保证选中下标不越界。
func (a *App) clampSelections() {
	if a.containerSel >= len(a.containers) {
		a.containerSel = len(a.containers) - 1
	}
	if a.imageSel >= len(a.images) {
		a.imageSel = len(a.images) - 1
	}
	if a.volumeSel >= len(a.volumes) {
		a.volumeSel = len(a.volumes) - 1
	}
	if a.networkSel >= len(a.networks) {
		a.networkSel = len(a.networks) - 1
	}
}

// markBusy 记录某个操作正在运行，用于在界面上禁用重复点击。
func (a *App) markBusy(key string, on bool) {
	a.setBusy(key, on)
}

// setBusy 线程安全地设置忙碌标记。
func (a *App) setBusy(key string, on bool) {
	a.busyMu.Lock()
	defer a.busyMu.Unlock()
	if a.busy == nil {
		return
	}
	a.busy[key] = on
}

// isBusy 线程安全地查询忙碌标记。
func (a *App) isBusy(key string) bool {
	a.busyMu.Lock()
	defer a.busyMu.Unlock()
	return a.busy[key]
}

// runAsync 在后台执行 fn 并把结果交给 apply（在主线程）。
//
// 这是所有「动作 → 刷新 → 提示」流程的统一入口。
func (a *App) runAsync(key, doneMsg string, fn func(ctx context.Context) (string, error)) {
	a.logInfo("action", "开始 %s", key)
	a.markBusy(key, true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		out, err := fn(ctx)

		a.markBusy(key, false)
		a.update(func() {
			if err != nil {
				a.lastError = err.Error()
				a.logError("action", "%s 失败: %s", key, firstLine(err.Error()))
				a.toast("操作失败: " + firstLine(err.Error()))
				if out != "" {
					a.showOutput("错误输出", out)
				}
				return
			}
			a.logInfo("action", "完成 %s", key)
			if doneMsg != "" {
				a.toast(doneMsg)
			}
			if strings.TrimSpace(out) != "" {
				a.showOutput(doneMsg, out)
			}
		})
		a.Refresh()
	}()
}

// showOutput 打开输出面板并写入文本。
func (a *App) showOutput(title, text string) {
	a.update(func() {
		a.outputTitle = title
		a.outputText = text
		a.outputOpen = true
	})
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
