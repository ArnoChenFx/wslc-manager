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
internal/app      → 只依赖 wslc / compose / store / selfupdate，不直接 exec
internal/wslc     → 只封装 wslc 命令行
internal/compose  → 只封装 wslc-compose
internal/store    → 只读写设置文件
internal/selfupdate → 只封装 MyGo 的更新插件（注册、读状态、触发检查）
```

- **内部包不允许反向依赖 `internal/app`。**
- 视图层不做 IO。所有慢操作走 `runAsync` / 后台 goroutine，
  结果经 `Window.Update` 回主线程。

## 关于 compose 解析

**不要自己解析 compose YAML。** 一律调用 `wslc-compose config` 拿已解析结果。
自己解析会漏掉变量插值、`.env`、`include`、profile、命名卷前缀等语义，
导致界面所示与实际运行不一致。

## 测试要求

改动这些地方必须补测试：

| 改动 | 测试类型 |
|---|---|
| 解析逻辑（标签、表格、JSON、配置） | 表驱动单测 |
| 目录扫描 | 用 `t.TempDir()` 造真实目录树 |
| 视图布局/文案 | `ui.NewTester` + `Access`/`Find` 量几何 |
| 与 wslc-compose 的契约 | 集成测试，未安装时 `t.Skip` |
| 更新配置 / 发布流程（`mygo.json`、`release.yml`） | 契约测试：解析文件断言约定，见 `internal/selfupdate/release_test.go` |
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

写日志：`a.logInfo("source", "format", ...)`，source 用 `wslc` / `compose` / `action` / `ui` / `refresh` / `diag`。
每条 `wslc`、`wslc-compose` 命令由客户端钩子自动记录，不需要手动埋点。

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
