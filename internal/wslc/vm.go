package wslc

import "context"

// 这个文件放「会话 / 虚拟机」相关的命令。
//
// 背景（已对照 WSL 源码和微软官方文档核实）：
//   - wslc 的每个会话都有自己的虚拟机，容器、镜像、卷、网络都存放在里面；
//   - 会话和虚拟机都是按需创建的：第一条需要它们的命令会把它们建起来；
//   - 虚拟机空闲一段时间（session.idleTimeout，默认 30 秒）会自己关掉，
//     会话还在，下一条命令再把虚拟机建起来；
//   - 只问会话管理器的命令（info、system session list、--version）不会建会话、不会拉起虚拟机，
//     而 list、images、volume、network 这类要和虚拟机里的 Docker 引擎对话的命令会。
//
// 最后一条决定了应用的刷新策略：虚拟机被用户关掉之后，后台刷新不能再发那类命令，
// 否则刚关掉就被叫醒了。

// StartSession 让默认会话的虚拟机运行起来。
//
// wslc 没有单独的「启动虚拟机」命令。「启动」就是跑一条最轻的、必须用到虚拟机的命令；
// 微软自己的端到端测试也是用 container list 来「创建默认会话」的。
// 选 list 而不是 info：info 只问会话管理器，不碰虚拟机，起不来。
//
// 命令返回时虚拟机里的 Docker 引擎已经就绪（list 就是在等它），
// 所以调用方随后立刻刷新数据是安全的。首次启动要几秒，调用方应当放后台，
// 并给 ctx 一个比默认超时更长的 deadline。
func (c *Client) StartSession(ctx context.Context) error {
	_, err := c.RunOK(ctx, "list", "--format", "json")
	return err
}

// TerminateSession 终止默认会话：关掉它的虚拟机，里面运行的容器随之终止。
//
// 容器和镜像存放在会话的存储盘里，不会丢。下次启动后，原来运行的容器是「已停止」状态，
// 需要重新启动。
//
// 只终止会话，不碰别的。绝不能改成 wsl --shutdown：它会关掉所有 WSL 虚拟机，
// 包括普通 WSL2 的发行版。
//
// 会话不存在时 wslc 会报错，而不是新建一个再杀掉（terminate 打开的是已有的默认会话）。
// 这里原样返回错误；调用方在已经知道虚拟机没在运行时，不该调用它。
func (c *Client) TerminateSession(ctx context.Context) error {
	_, err := c.RunOK(ctx, "system", "session", "terminate")
	return err
}
