// Package wslc 封装 Windows 上 WSL 容器 CLI（wslc）的命令行调用。
//
// 本包只做三件事：
//  1. 找到 wslc 可执行文件；
//  2. 用统一的方式执行命令并采集 stdout / stderr / 退出码；
//  3. 把 wslc 的 --format json 输出解析成 Go 结构体。
//
// 所有导出的类型都刻意保持"扁平 + 可 JSON 反序列化"，方便上层 UI 直接使用。
package wslc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 常见安装位置，按优先级排列。wslc 随 WSL 预览版一起安装到 Program Files。
var defaultCandidates = []string{
	`C:\Program Files\WSL\wslc.exe`,
	`C:\Program Files\WSL\wslc`,
}

// ErrNotFound 表示在系统中找不到 wslc 可执行文件。
var ErrNotFound = errors.New("找不到 wslc 可执行文件")

// Client 是 wslc 命令行的封装。零值不可用，请用 New。
//
// Client 可以并发使用：内部的串行锁保证同一时刻只有一个 wslc 进程在跑。
// wslc 预览版对并发调用非常敏感（会报 ERROR_SHARING_VIOLATION），
// 因此这里主动把所有调用串行化。
type Client struct {
	bin string

	mu sync.Mutex
	// timeout 是单条命令的默认超时。0 表示不限时。
	timeout time.Duration
	// onRun 在每条命令执行完毕后被调用，用于记录诊断日志。
	// 它在调用方协程里同步执行，实现应尽量轻量。
	onRun func(Result)
}

// SetRunHook 设置命令执行后的回调，用于诊断日志。
func (c *Client) SetRunHook(fn func(Result)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onRun = fn
}

// callHook 触发执行回调（在锁外调用，避免回调里再调用客户端时自锁）。
func (c *Client) callHook(r Result) {
	c.mu.Lock()
	fn := c.onRun
	c.mu.Unlock()
	if fn != nil {
		fn(r)
	}
}

// New 创建客户端并定位 wslc 可执行文件。
//
// 查找顺序：环境变量 WSLC_BIN（显式覆盖）→ 默认安装路径 → PATH。
func New() (*Client, error) {
	bin, err := locate()
	if err != nil {
		return nil, err
	}
	return &Client{bin: bin, timeout: 60 * time.Second}, nil
}

// NewWithBinary 用指定的 wslc 路径创建客户端，主要用于测试。
func NewWithBinary(bin string) *Client {
	return &Client{bin: bin, timeout: 60 * time.Second}
}

// Binary 返回实际使用的 wslc 可执行文件路径。
func (c *Client) Binary() string { return c.bin }

// SetTimeout 修改单条命令的默认超时时间。
func (c *Client) SetTimeout(d time.Duration) { c.timeout = d }

// locate 按优先级查找 wslc 可执行文件。
func locate() (string, error) {
	if v := strings.TrimSpace(os.Getenv("WSLC_BIN")); v != "" {
		if fileExists(v) {
			return v, nil
		}
		return "", fmt.Errorf("%w: WSLC_BIN 指向的 %q 不存在", ErrNotFound, v)
	}
	for _, p := range defaultCandidates {
		if fileExists(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath("wslc"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w：请确认已安装 WSL 容器预览版（C:\\Program Files\\WSL\\wslc.exe），或用 WSLC_BIN 指定路径", ErrNotFound)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Result 是一条命令的完整结果。
type Result struct {
	// Args 是实际执行的参数（不含可执行文件本身）。
	Args []string
	// Stdout 与 Stderr 是原始输出。
	Stdout string
	Stderr string
	// ExitCode 是进程退出码；命令超时或被中断时可能为 -1。
	ExitCode int
	// Duration 是这次调用消耗的时间。
	Duration time.Duration
}

// Ok 表示命令是否成功（退出码为 0）。
func (r Result) Ok() bool { return r.ExitCode == 0 }

// Output 返回去掉首尾空白的 stdout。
func (r Result) Output() string { return strings.TrimSpace(r.Stdout) }

// Error 把失败的命令结果转成可读的 error，成功时返回 nil。
//
// wslc 的错误信息通常在 stderr，但个别命令会写进 stdout，这里都带上。
func (r Result) Error() error {
	if r.Ok() {
		return nil
	}
	msg := strings.TrimSpace(r.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(r.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("退出码 %d", r.ExitCode)
	}
	return fmt.Errorf("wslc %s 失败: %s", strings.Join(r.Args, " "), msg)
}

// Combined 拼接 stdout 与 stderr，用于日志类输出。
func (r Result) Combined() string {
	out := r.Stdout
	if r.Stderr == "" {
		return out
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + r.Stderr
}

// withTimeout 给 ctx 套上默认超时，但**调用方已经设了 deadline 就尊重它**。
//
// 为什么：默认的 60 秒是给 list / inspect 这类快命令兜底的。
// 编排引擎里 pull、build 可能跑几分钟，它们自己带了更长的 deadline；
// 如果这里不分青红皂白再叠加 60 秒，调用方给的 5 分钟就形同虚设，
// 拉大镜像总会在一分钟时被无声截断。
func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, has := ctx.Deadline(); has || c.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

// Run 执行一条 wslc 子命令，例如 Run(ctx, "list", "-a", "--format", "json")。
//
// 它不会因为非零退出码返回 error——那是 Result 的一部分——只有在无法启动
// 进程（找不到文件、超时）时才返回 error。这样上层可以同时拿到输出和退出码。
func (c *Client) Run(ctx context.Context, args ...string) (Result, error) {
	start := time.Now()

	// 串行化：wslc 预览版同一时刻只容忍一个调用。
	// 用显式 Unlock 而不是 defer：命令结束后要在锁外触发 onRun 回调，
	// 否则回调里若再调用本客户端就会自锁。
	c.mu.Lock()

	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, args...)
	// 隐藏自动弹出的控制台窗口：GUI 程序里调用命令行工具时必须这么做，
	// 否则每次刷新都会闪一下黑窗口。
	hideWindow(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// 不设置任何额外的环境变量：wslc 是原生程序，结构化输出（--format json）始终是 UTF-8，
	// 所以我们只解析 JSON，不依赖可能随代码页变化的表格文本。

	err := cmd.Run()
	res := Result{
		Args:     args,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(start),
	}
	// 锁已释放，现在可以在锁外触发回调。
	c.mu.Unlock()

	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			c.callHook(res)
			return res, nil
		}
		// 上下文取消 / 超时 / 启动失败
		res.ExitCode = -1
		if ctx.Err() != nil {
			res.Stderr = strings.TrimSpace(res.Stderr + "\n" + ctx.Err().Error())
		}
		c.callHook(res)
		return res, fmt.Errorf("执行 %s 失败: %w", strings.Join(args, " "), err)
	}
	c.callHook(res)
	return res, nil
}

// RunOK 执行命令并在失败时返回 error，适合不需要看退出码的场景。
func (c *Client) RunOK(ctx context.Context, args ...string) (Result, error) {
	res, err := c.Run(ctx, args...)
	if err != nil {
		return res, err
	}
	return res, res.Error()
}

// runJSON 执行命令并把 stdout 当作「一行一个 JSON 对象」解析到 out。
//
// wslc 的 --format json 输出是 NDJSON（每行一个独立对象），而不是数组，
// 因此这里逐行解码。空行会被忽略。
func (c *Client) runJSON(ctx context.Context, out any, args ...string) error {
	res, err := c.RunOK(ctx, args...)
	if err != nil {
		return err
	}
	return decodeNDJSON(res.Stdout, out)
}

// decodeNDJSON 把 NDJSON（每行一个 JSON 值）解码到 slice 指针。
//
// out 必须是指向切片的指针，例如 *[]ContainerRow。
func decodeNDJSON(data string, out any) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil
	}
	// 兼容偶尔出现的整体数组格式。
	if strings.HasPrefix(trimmed, "[") {
		return json.Unmarshal([]byte(trimmed), out)
	}

	lines := strings.Split(trimmed, "\n")
	objs := make([][]byte, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		objs = append(objs, []byte(ln))
	}
	// 用 json.RawMessage 中转，逐个放进目标切片。
	raw := make([]json.RawMessage, 0, len(objs))
	for _, o := range objs {
		raw = append(raw, json.RawMessage(o))
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(buf, out)
}

// absPath 用于把相对路径转成绝对路径，避免工作目录差异导致的错误。
func absPath(p string) string {
	if p == "" {
		return ""
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// RunStream 执行命令并把每一行输出实时交给 onLine。
//
// 用于 `wslc pull` 这类会输出进度的命令：调用方可以边执行边展示，
// 而不是等整个命令结束才拿到输出。
//
// onLine 在后台 goroutine 里被调用，调用方需自行保证线程安全
// （本应用的做法是通过 Window.Update 把行转发到主线程）。
// 返回的 Output 是完整输出，便于结束后留档。
func (c *Client) RunStream(ctx context.Context, onLine func(line string), args ...string) (Result, error) {
	c.mu.Lock()
	return c.stream(ctx, onLine, args, true)
}

// Follow 与 RunStream 相同，但**不占用串行锁**，专给 `logs -f` 这类长期存活的只读流使用。
//
// 为什么要单独一个方法：串行锁的目的是不让两个会改状态的 wslc 调用重叠；
// 而一条 `logs -f` 可能挂几个小时。如果它也排队，在它结束之前，
// 界面上所有的刷新、启动、停止都会被堵死。读日志不改任何状态，不需要这把锁。
//
// 没有默认超时（跟随日志本来就是长期的）：调用方用 ctx 控制何时结束。
func (c *Client) Follow(ctx context.Context, onLine func(line string), args ...string) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.stream(ctx, onLine, args, false)
}

// Attach 执行命令，并把子进程的 stdin / stdout / stderr 直接接到当前进程的标准流。
//
// 用于 `exec -i -t` 这类需要终端的交互命令：输出不经过我们，直接写到用户的终端，
// 所以 Result 里没有 Stdout / Stderr，只有退出码。
//
// 和 Follow 一样不占串行锁、也不套默认超时：交互会话可能开几个小时，
// 而且它占着终端，同一时刻本来就只会有这一个。
//
// 故意不调用 hideWindow：CREATE_NO_WINDOW 会让子进程没有控制台，
// 需要终端的程序（wslc exec -t）就拿不到可用的控制台句柄。命令行程序从终端里启动子进程
// 本来就不会弹窗，所以只有命令行程序会用这个方法；图形界面不要调用它。
func (c *Client) Attach(ctx context.Context, args ...string) (Result, error) {
	start := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Run()
	res := Result{Args: args, Duration: time.Since(start)}
	if err != nil {
		// 被取消（用户按了 Ctrl+C）时进程是被我们杀掉的，退出码没有意义，
		// 要如实报告「被取消」，而不是伪装成一次普通的失败。
		if ctx.Err() != nil {
			res.ExitCode = -1
			c.callHook(res)
			return res, ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			c.callHook(res)
			return res, nil
		}
		res.ExitCode = -1
		c.callHook(res)
		return res, fmt.Errorf("执行 %s 失败: %w", strings.Join(args, " "), err)
	}
	c.callHook(res)
	return res, nil
}

// stream 是 RunStream 与 Follow 的共同实现。
//
// locked 为 true 表示调用方已经持有串行锁，本函数负责在命令结束后释放；
// 为 false 表示不走串行锁，也不套默认超时。
func (c *Client) stream(ctx context.Context, onLine func(line string), args []string, locked bool) (Result, error) {
	start := time.Now()

	unlock := func() {
		if locked {
			c.mu.Unlock()
			locked = false
		}
	}
	if locked {
		var cancel context.CancelFunc
		ctx, cancel = c.withTimeout(ctx)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.bin, args...)
	hideWindow(cmd)

	// 把 stdout 与 stderr 合并到同一个管道：wslc 的进度信息两个流都可能有。
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		unlock()
		res := Result{Args: args, ExitCode: -1, Duration: time.Since(start)}
		c.callHook(res)
		return res, fmt.Errorf("启动 %s 失败: %w", strings.Join(args, " "), err)
	}

	var all strings.Builder
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(pr)
		// 某些进度行可能很长，放宽缓冲上限。
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			all.WriteString(line)
			all.WriteString("\n")
			if onLine != nil {
				onLine(line)
			}
		}
		// 读取结束，关闭管道读端，让写入端不再阻塞。
		_ = pr.Close()
	}()

	waitErr := cmd.Wait()
	// 命令结束后关闭写端，让读取协程退出。
	_ = pw.Close()
	<-readDone

	res := Result{
		Args:     args,
		Stdout:   all.String(),
		ExitCode: 0,
		Duration: time.Since(start),
	}
	unlock()

	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			res.ExitCode = ee.ExitCode()
			c.callHook(res)
			return res, nil
		}
		res.ExitCode = -1
		if ctx.Err() != nil {
			res.Stderr = ctx.Err().Error()
		}
		c.callHook(res)
		return res, fmt.Errorf("执行 %s 失败: %w", strings.Join(args, " "), waitErr)
	}
	c.callHook(res)
	return res, nil
}

// PullImageStream 拉取镜像，并把每一行进度实时交给 onLine。
func (c *Client) PullImageStream(ctx context.Context, ref string, onLine func(string)) (Result, error) {
	return c.RunStream(ctx, onLine, "pull", ref)
}
