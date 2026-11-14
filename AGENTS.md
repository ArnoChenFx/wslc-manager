# 项目约定

## 语言

- 所有文档、注释、界面文案、提交说明一律用**中文**。
- 代码标识符（变量、函数、类型）用英文。

## 目录

- 计划文档放 `docs/plan/`，命名 `NNN-主题.md`。
- 开发笔记放 `docs/note/`，截图放 `docs/note/images/`。

## 代码风格

- 每个包都有包级注释，说明它负责什么、边界在哪。
- 导出的类型和函数必须有注释，且注释讲**为什么**，不只是复述签名。
- 非显而易见的地方要解释原因，例如：
  ```go
  // 串行化：wslc 预览版同一时刻只容忍一个调用。
  c.mu.Lock()
  defer c.mu.Unlock()
  ```

## 分层约束

```
internal/app      → 只依赖 wslc / compose / store / selfupdate / wslvm，不直接 exec
internal/wslc     → 只封装 wslc 命令行
internal/compose  → 内置的 Compose 引擎：依赖 compose-go，经 Runtime 接口调用 wslc；
                    不 import internal/wslc，也不 import internal/app
internal/store    → 只读写设置文件
internal/selfupdate → 只封装 MyGo 的更新插件（注册、读状态、触发检查）
internal/wslvm    → 只在宿主机一侧观测会话的虚拟机（vmmem 进程：在不在、占多少内存），不调用 wslc
internal/composert → 把 wslc.Client 适配成 compose.Runtime（图形界面与命令行共用）
cmd/wslc-compose  → 命令行版 Compose：只依赖 compose / composert / wslc，
                    **不 import internal/app，也不 import MyGo**
```

- **内部包不允许反向依赖 `internal/app`。**
- `internal/composert` 是唯一把 `wslc.Client` 适配成 `compose.Runtime` 的地方（图形界面与命令行共用）。
- 视图层不做 IO。所有慢操作走 `runAsync` / 后台 goroutine，
  结果经 `Window.Update` 回主线程。

## 关于 compose 解析

**不要自己解析 compose YAML。** 一律走 `internal/compose` 的 `loadProject`（它调用 compose-go）。
自己解析会漏掉变量插值、`.env`、`include`、`extends`、profile、命名卷前缀等语义，
导致界面所示与实际运行不一致。

界面展示用的 `compose.Project` / `ServiceConfig` 是 compose-go 模型的精简投影（`view.go`），
不要把 compose-go 的类型穿透到 `internal/app`。

## 关于 compose 引擎

编排（up/down/start/…）由本仓库自己实现，不依赖任何外部编排工具。

- 所有 wslc 调用经 `Runtime` 接口。测试用 `fakeWslc`（`fake_test.go`）验证流程，
  **不要**为了测流程去碰真实 wslc。
- 配置指纹（`hash.go`）是对**生成出来的 wslc 参数**求哈希：改 `flags.go` 里参数的顺序或格式，
  会让所有现有容器在下次 `up` 时重建一次。改之前想清楚，必要时升级 `hashPrefix`。
- compose-go 的各种映射遍历顺序是随机的：凡是会进入命令行、输出或指纹的地方都必须先排序。
- 清理半成品容器只能动「本次操作创建的」（`create-operation` 标签），绝不能按名字删。
- 真实 wslc 测试（`realwslc_test.go`）只用本机已有的小镜像、唯一项目名，结束必清理；
  没有 wslc 或没有镜像时自动跳过。绝不碰用户已有的容器、网络、卷。
- `Runtime.Attach`（子进程直接接到当前进程的标准流）只给命令行的 `exec` 用，图形界面不要调用：
  它故意不隐藏控制台窗口，否则 `wslc exec -t` 拿不到可用的终端。
- 所有能改动状态的操作都要支持 `ActionOptions.DryRun`，包括 `Exec`（dry-run 时只返回参数、绝不执行）。

## 关于命令行工具

`cmd/wslc-compose` 是同一个引擎的命令行版本，每次发布都会作为单独的 zip 带上。

- 它必须是个小而独立的程序：不能 import `internal/app` 与 MyGo（`guard_test.go` 守着）。
  `CGO_ENABLED=0` 拦不住这件事，因为 MyGo 本身不需要 cgo。
- 全局选项（`-f`、`-p` ……）是根命令的**本地**选项，写在命令之前（`TraverseChildren`），
  这样子命令才能有自己的 `-f`（`logs -f` 是「跟随」）。不要改成持久（persistent）选项，简写会冲突。
- 输出分流：进度写 stderr，`ps` / `config` / `logs` 这类数据写 stdout；`--dry-run` 的预览写 stdout。
- 退出码：0 成功，1 运行失败，2 用法错误，130 被 Ctrl+C 打断，`exec` 是容器内命令的退出码。
- 找 wslc 是惰性的（`runtime.go`）：`config`、`--version` 在没装 wslc 的机器上也要能用。
- 版本号用 `-ldflags "-X main.version=…"` 注入；链接器对不存在的符号静默忽略，
  所以 `internal/selfupdate/release_test.go` 会校验变量名没写错。
- 发布流程里的构建、打包、校验和、上传，以及 CI 里的冒烟步骤，都有契约测试守着。

## 测试要求

改动这些地方必须补测试：

| 改动 | 测试类型 |
|---|---|
| 解析逻辑（标签、表格、JSON、配置） | 表驱动单测 |
| 目录扫描 | 用 `t.TempDir()` 造真实目录树 |
| 视图布局/文案 | `ui.NewTester` + `Access`/`Find` 量几何 |
| 编排流程（up/down/重建/依赖等待/失败不连坐） | 假 `Runtime` 记录命令并断言 |
| 与真实 wslc 的契约 | 集成测试（`realwslc_test.go`），未安装时 `t.Skip` |
| 命令行（选项映射、输出流、退出码） | 假引擎（`cmd/wslc-compose/cli_test.go`）；`config` 这类不需要 wslc 的命令用真实引擎 + 一旦被调用就失败的 `Runtime` |
| 更新配置 / 发布流程（`mygo.json`、`release.yml`） | 契约测试：解析文件断言约定，见 `internal/selfupdate/release_test.go` |
| 虚拟机的状态与刷新门控 | `wslctest` 假 wslc 记录命令 + `fakeHost` 假宿主机，断言「没有发出会叫醒虚拟机的命令」，见 `internal/app/vmgate_test.go` |
| 崩溃/死锁类修复 | 必须补回归测试，注释写明崩溃现象 |

回归测试的注释要写明**它防的是什么缺陷**，例如：

```go
// 回归测试：早先多画了一个独立的刷新图标，右上角出现两个刷新图标。
```

## 布局注意事项

这些是踩过的坑，写新界面时要避开：

- 同一容器里不要有两个元素都要求占满剩余空间（`Grow(1)` 与 `Fill()` 并存）。
- `Scroll` 的 `Padding` 会随内容滚动，底部留白要加在内部容器上。
- 「滚到底最后几行还是被裁」先量 `Scroll` 的视口底边有没有伸出窗口
  （`tt.AccessAll()` 里 `RoleScroll` 的 `Bounds`）。多半是外层容器用了 `Fill()`，
  把工具栏、状态栏也算进了高度，应改 `Grow(1)`。不要靠把文本拆成逐行、
  或在末尾垫空白来凑——那只是把症状盖住。
- 要让用户能跨行拖选、批量复制的文字，整段放进同一个 `Text`（或把 `Selectable()`
  加在外层容器上）。单个 `Text` 上的 `Selectable()` 选区只停留在该段落内，
  每行各一个 `Text` 就复制不了多行。
- 表格选中行会被填强调色，行内次级文字须切到 `AccentText`。
- 同一 `Row` 里各子块的行数要一致，否则基线错位。
- 文字行里的装饰圆点用 `ui.Icon`，不要用裸 `Box`。
- `ui.Divider` 在 `Row` 中是横线；竖分隔条用 `Width(1)` 的 `Box`。

## 发布与自更新

应用用 MyGo 自带的更新插件自更新：发布时 `mygo build` 用私钥给更新包签名，
应用里的插件从 GitHub **最新发布**读取 `update-windows-amd64.json`，校验签名后下载、替换、重启。

- 应用侧只通过 `internal/selfupdate` 接入。**不要**在界面里直接调 MyGo 的 `updater` 包：
  插件没注册时它的每个函数都会 panic（单测、`go run` 都是这种情况），
  而且读状态要探测安装目录能否写入（磁盘 IO），所以视图只读 `App.upd` 这份快照。
- 版本号只有一个来源：`mygo.json` 的 `version`，发布流程按 tag 改写它。
  不要在代码里写死版本号。
- 公钥在 `mygo.json` 的 `updates.publicKey`（可以入库）。**私钥绝不入库**：
  本机在 `%APPDATA%\mygo\update-keys\wslc-manager\mygo-update.key`，CI 里是仓库
  secret `MYGO_UPDATER_PRIVATE_KEY`。丢了私钥，已安装的程序就再也收不到更新，
  请另存一份到密码管理器。
- `release.yml` 缺私钥、或私钥与 `mygo.json` 里的公钥不配对，都会在第一步直接失败
  （签名用任何密钥都能成功，配错了会发出所有已装程序都拒收的更新）；
  会校验更新文件与 tag 一致；先建草稿、传完再公开
  （应用从「最新发布」读 manifest，不能让它早于更新包可见）。
  改动它之前先跑 `go test ./internal/selfupdate/`，契约测试会指出哪条约定被破坏。
- 第一个带自更新的版本只能让**之后**的版本被更新到；已装的旧版（没有更新插件）
  要手动装一次。

## 虚拟机（wslc 会话）

wslc 的容器、镜像、卷、网络都在会话的虚拟机里。以下事实已对照 WSL 源码和微软官方文档核实（证据见 `docs/note/dev-notes.md`）：

- 会话和虚拟机都是**按需创建**的。要和虚拟机里的 Docker 引擎对话的命令（`list`、`images`、
  `volume`、`network`、`run`……）在虚拟机没运行时会把它**拉起来**；只问会话管理器的命令
  （`info`、`system session list`、`--version`）不会。
- 虚拟机空闲 `session.idleTimeout` 秒（默认 30）后自己关闭，会话还在，下一条命令再把它建起来。
  有运行中的容器或进行中的操作就不会空闲关闭。
- 「启动」= 跑一条要用到虚拟机的命令（`wslc list`，微软自己的测试也这么建默认会话）；
  「关闭」= `wslc system session terminate`。**不要用 `wsl --shutdown`**，它会关掉所有 WSL 虚拟机，
  包括普通 WSL2 的发行版。`terminate` 在会话不存在时报错，不会先建再杀。
- 默认会话名是 `wslc-cli-<用户名>`，提权后是 `wslc-cli-admin-<用户名>`；它的虚拟机在宿主机上是
  `vmmem<会话名>` 进程，工作集就是占用的内存。普通 WSL2 的虚拟机叫 `vmmemWSL`，是另一个东西。

由此得到的规则：

- **后台刷新不得发会拉起虚拟机的命令。** `Refresh` 开头调 `refreshGated`：确定没在运行时只刷新版本、
  `info`、会话列表。往 `Refresh` 或别的后台流程里加任何新的 wslc 调用之前，先想它会不会叫醒虚拟机；
  `vmgate_test.go` 的 `vmBoundCalls` 是反着列的白名单，新增的调用会被它抓出来。
- **状态只问操作系统**（`internal/wslvm`），不为了「看看它在不在」去调 wslc。
- **观测不了（Unknown）一律照常行事。** 只有「确定已停止」才能拦，宁可多问一次，
  也不能把运行中的虚拟机拦在界面外。启动成功却始终见不到进程时，自动停用状态判断。
- 关闭动作要**等进程真正消失**再结束：`runAsync` 结束时会立刻刷新，那时进程还在的话，
  刷新会照常发命令，把刚关掉的虚拟机又叫醒。
- 测试里**不要去关真实的虚拟机**：里面可能有用户正在运行的容器。用 `internal/wslc/wslctest`
  （批处理假 wslc）记录命令。带活 Tester 的测试里等异步动作的结果，要用 `ViewLocked` 把渲染和协程里的
  `update` 串行起来（否则 `-race` 报数据竞争），并等「动作结束时一定会出现的那条提示」，
  不要用「不忙且不在刷新」判断动作已结束（`runAsync` 清忙磌标记与发起刷新之间有个空档）。

## 诊断与自查

**排查界面问题不要靠看截图猜。** 用工具拿数据：

```powershell
# 元素树 + 精确坐标（DIP）
.\scripts\diag.ps1 -Page <page> -Wait 10

# 加上截图与完整报告
.\scripts\diag.ps1 -Page compose -Shot compose.png -Report
```

判断标准：

- `bounds` 高度为 `0` 通常是**布局冲突**（两个元素抢空间），不是元素不存在。
- 「没对齐」要比较数字，不要凭观感。
- 「点了没反应」先看运行日志里有没有对应命令，区分「没触发」还是「失败」。

写日志：`a.logInfo("source", "format", ...)`，source 用 `wslc` / `compose` / `action` / `ui` / `refresh` / `diag` / `vm`。
每条 `wslc` 命令由客户端钩子自动记录；每个编排动作（up/down/…）的结果由 compose 客户端的钩子记录一行，
不需要手动埋点。

详见 [docs/note/diagnostics.md](docs/note/diagnostics.md)。

## 验证清单

提交前跑：

```sh
go build ./...
go vet ./...
go test ./...
go tool mygo vet .
```

改了界面还要实际看一眼：

```sh
WSLC_MANAGER_SHOT=shot.png WSLC_MANAGER_SHOT_DELAY=14s go tool mygo dev
```

（延迟要够长，首帧还没有后台数据。）

## 更新文档

新增功能后同步更新：

- `README.md` 的功能表与项目结构；
- `docs/note/dev-notes.md` 里新发现的 wslc / MyGo 行为。
