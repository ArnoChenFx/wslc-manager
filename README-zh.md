# WSLC Manager

在 Windows 上管理 [WSL 容器](https://learn.microsoft.com/windows/wsl/wsl-container)（`wslc`）的桌面应用——类似 Docker Desktop，但管理的是 `wslc`。

用 **Go** 和 [MyGo](https://github.com/egoist/mygo) 的原生 UI 构建：没有 webview，没有 Electron，不需要 Node。单个可执行文件，启动即出界面。

<p align="center">
  <img src="resources/icon.png" width="120" alt="WSLC Manager 图标">
</p>

<p align="center">
  <a href="README.md">English</a>
</p>

---

## 它能做什么

`wslc` 是 WSL 自带的容器命令行工具。WSLC Manager 给它套上一个真正的界面。

### 容器
列出、启动、停止、重启、删除容器。资源占用、`inspect` 详情、流式日志都在同一个窗口里，上下面板可拖动调整比例。

### 镜像
查看本地镜像的大小与创建时间。拉取镜像时**实时显示进度**，查看同仓库的所有标签，跳到详情或资源占用，也可以一次更新全部镜像。

### 卷与网络
创建、查看、删除命名卷与网络。内置网络受保护，不会被误删。

### Compose 项目
指向一个包含 `compose.yaml`（或 `docker-compose.yml`）的目录，就能管理整组服务：启动、停止、重启、构建、拉取、移除，以及逐个服务控制。

编排功能**内置在应用里**，不需要再装任何工具。Compose 文件由 [compose-go](https://github.com/compose-spec/compose-go) 读取——也就是 `docker compose` 自己用的那个库——所以变量插值、`.env`、`include`、`extends`、多个 `-f` 文件和 profile 都遵循 Compose 规范。界面和实际执行的动作共用同一个加载器，界面所示即实际运行。

已支持：`up` / `down` / `start` / `stop` / `restart` / `pull` / `build`，`depends_on` 条件（`service_healthy`、`service_completed_successfully`），健康检查，多网络（别名、静态 IP），基于文件的 secrets 与 configs，`scale`，以及 dry-run 预览。某个服务启动失败不会连累其他服务，只有依赖它的服务会被跳过。

compose 文件与 `.env` 可以直接在应用里查看和编辑：选中项目后点「编辑文件」。编辑器带 YAML 高亮、行号、折叠和查找替换；保存会保留文件原本的换行符与权限。点「新建项目」可以从零建一个：选目录、写 Compose 内容、（可选）写 `.env`，写盘前先检查 YAML 语法。不会覆盖任何已有文件——目录必须是空的或者还不存在。

`wslc` 做不到的设置（`privileged`、`devices`、`sysctls`、`always` / `no` 之外的重启策略等）会给出警告并被忽略；会悄悄出错的用法（匿名卷、外部或非文件来源的 secret、IPv6 网络）会在创建任何东西之前被拒绝。

同一个引擎也提供独立的[命令行工具](#命令行工具)。

### 系统
环境版本、WSL 会话，以及在缺少 `wslc` 时可一键安装或更新 WSL 的体检。

### 虚拟机
`wslc` 运行的一切都在一个轻量虚拟机里。**系统信息**页显示它是否在运行、在 Windows 上占用多少内存（任务管理器里的 `vmmem…` 进程），并可以由你自己启动和关闭。关闭会终止里面正在运行的容器；容器和镜像都会保留。

虚拟机停着的时候，应用不会去问它任何东西，因为一次刷新就会把它重新叫醒。容器、镜像、卷、网络这几页会说明情况，并给出「启动虚拟机」按钮。没有容器在运行时，`wslc` 本身也会在空闲一段时间（默认 30 秒）后自动关闭虚拟机，所以里面什么都没跑时手动启动它，过了这段时间它就又休眠了；想让它留得久一点，可以调大 wslc 设置里的 `session.idleTimeout`。

### 自更新
应用启动后不久检查一次本仓库的发布，之后每天一次；有新版本时弹出一个小窗口，可以选 **安装更新**、**稍后提醒我** 或 **跳过这个版本**。更新包经过签名：应用用内置的公钥校验通过后才会替换自己，并且尽量只下载变化的部分。可以在**系统信息**页手动检查，也可以在**设置**里关闭自动检查和自动安装。检查时会访问 `github.com`。

自更新要求程序能写入自己所在的目录。安装包的当前用户安装、放在普通文件夹里的便携版都可以；放在 `Program Files` 下则不行，系统信息页会说明这一点。

### 诊断
遇到布局错位或点了没反应的按钮？导出一份诊断报告：环境版本、界面上每个元素的精确坐标、最近的命令与错误日志，全在一个 Markdown 文件里。另外还有实时运行日志，记录每一次 `wslc` 调用。

### 中英双语
在设置里切换中文和英文，选择会被记住。

---

## 环境要求

| 组件 | 是什么 | 怎么装 |
|---|---|---|
| **Windows 10/11** | — | — |
| **支持容器的 WSL** | 提供 `wslc.exe` | `winget install Microsoft.WSL`，或 `wsl --update` |

这是唯一的依赖。应用会在**系统信息**页检查它，并在本机有 `winget` 时帮你一键安装或更新 WSL。`wslc` 属于 WSL，所以应用不会单独下载它。Compose 已内置，不需要再安装别的东西。

---

## 安装

### 便携版

从 [Releases](https://github.com/ArnoChenFx/wslc-manager/releases) 下载 `WSLC-Manager-<版本>-windows-amd64.zip`，解压到任意位置，运行 `WSLC Manager.exe`。

### 安装包

下载并运行 `wslc-manager-setup-<版本>-amd64.exe`，会安装应用并创建开始菜单项。

### 命令行工具

Compose 引擎也有独立的 `wslc-compose` 命令行版本，方便在终端和脚本里使用。从 [Releases](https://github.com/ArnoChenFx/wslc-manager/releases) 下载 `wslc-compose-<版本>-windows-amd64.zip`，把里面的 `wslc-compose.exe` 解压到 `PATH` 里的任意目录，在含有 `compose.yaml` 的目录里运行：

```powershell
wslc-compose up -d          # 创建并启动服务
wslc-compose ps             # 查看容器
wslc-compose logs -f web    # 跟随某个服务的日志
wslc-compose exec web sh    # 在容器里开一个 shell
wslc-compose down           # 停止并移除
```

命令：`up`、`down`、`start`、`stop`、`restart`、`ps`、`logs`、`exec`、`pull`、`build`、`config`、`version`。每个命令的选项用 `wslc-compose <命令> --help` 查看。

作用于整个项目的选项要写在命令**之前**：`-f/--file`（可重复）、`-p/--project-name`、`--env-file`、`--profile`，以及 `--dry-run`（只打印将要执行的 `wslc` 命令，不真正执行）。

```powershell
wslc-compose -f compose.yaml -f override.yaml -p demo up -d
wslc-compose --dry-run up -d
```

它和应用管理的是同一批项目（两边用同一个引擎，给容器打同样的标签），只依赖 `wslc`。进度信息写到标准错误；`ps`、`config`、`logs` 写到标准输出，可以直接接管道。退出码：成功 `0`，失败 `1`，用法错误 `2`；`exec` 则是容器内命令自己的退出码。应用不会更新命令行工具，升级时下载新的 zip 即可。

### 从源码构建

```powershell
git clone https://github.com/ArnoChenFx/wslc-manager.git
cd wslc-manager
go build -o "WSLC Manager.exe" .
go build -o wslc-compose.exe ./cmd/wslc-compose   # 命令行工具
```

需要 Go 1.25 或更高版本。

---

## 快速上手

1. **启动一个容器** —— 打开 **Compose 项目** 页，点 **添加项目**，选择包含 `compose.yaml` 的目录。选中项目后点 **启动**。
2. **或者拉一个镜像** —— 打开 **镜像** 页，点 **拉取镜像**，输入 `nginx:alpine` 这样的引用。
3. **看它跑起来** —— **容器** 页会自动更新。双击某一行查看日志。

如果有依赖没装好，**系统信息** 页会告诉你是哪一个，并提供一键安装。

---

## 注意事项与限制

`wslc` 还处在公开预览阶段，有一些真实存在的限制，应用里都会提示：

- 同一时刻只容忍一个 `wslc` 调用。本应用已自动把所有调用串行化。
- 暂不支持 `pause` / `unpause`，以及 `unless-stopped` / `on-failure` 重启策略。
- 单个 WSL 会话最多挂载约 15 个卷，超出会报 *Too many volumes have been mounted*；用 `wsl --shutdown` 重置。
- 发布的端口绑定在 Windows 回环地址上，需要在 Windows 侧测试。
- 不同提权级别（普通 / 管理员）的终端拥有彼此独立的 `wslc` 会话，互相看不到对方的容器。

---

## 技术栈

- **[Go](https://go.dev)** —— 单一静态二进制，目标机器无需安装运行时。
- **[MyGo](https://github.com/egoist/mygo)** —— 纯 Go 的原生桌面 UI。布局、主题、控件、系统原生文件对话框、平台窗口都由它提供。没有 HTML，没有 JavaScript，不打包浏览器。
- **[compose-go](https://github.com/compose-spec/compose-go)** —— 读取并规范化 Compose 文件（`docker compose` 背后的库）。其上的 `wslc` 编排是本仓库自己的 `internal/compose` 包。
- **[cobra](https://github.com/spf13/cobra)** —— `wslc-compose` 命令行工具的参数解析。

---

## 许可

MIT
