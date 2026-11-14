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
internal/app      → 只依赖 wslc / compose / store，不直接 exec
internal/wslc     → 只封装 wslc 命令行
internal/compose  → 只封装 wslc-compose
internal/store    → 只读写设置文件
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
| 崩溃/死锁类修复 | 必须补回归测试，注释写明崩溃现象 |

回归测试的注释要写明**它防的是什么缺陷**，例如：

```go
// 回归测试：早先多画了一个独立的刷新图标，右上角出现两个刷新图标。
```

## 布局注意事项

这些是踩过的坑，写新界面时要避开：

- 同一容器里不要有两个元素都要求占满剩余空间（`Grow(1)` 与 `Fill()` 并存）。
- `Scroll` 的 `Padding` 会随内容滚动，底部留白要加在内部容器上。
- 表格选中行会被填强调色，行内次级文字须切到 `AccentText`。
- 同一 `Row` 里各子块的行数要一致，否则基线错位。
- 文字行里的装饰圆点用 `ui.Icon`，不要用裸 `Box`。
- `ui.Divider` 在 `Row` 中是横线；竖分隔条用 `Width(1)` 的 `Box`。

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
