package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Result 是一条命令的结果。字段与 wslc.Result 对应，这里独立定义，
// 是为了让 compose 包只依赖一个很小的 Runtime 接口，测试里用假实现即可。
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Ok 表示命令是否成功。
func (r Result) Ok() bool { return r.ExitCode == 0 }

// Combined 合并使用 stdout 与 stderr，用于展示进度/日志。
func (r Result) Combined() string {
	var b strings.Builder
	b.WriteString(r.Stdout)
	if r.Stderr != "" {
		if b.Len() > 0 && !strings.HasSuffix(r.Stdout, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(r.Stderr)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Error 把失败结果转成 error；成功返回 nil。
func (r Result) Error() error {
	if r.Ok() {
		return nil
	}
	msg := strings.TrimSpace(r.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(r.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("exit code %d", r.ExitCode)
	}
	return errors.New(msg)
}

// Runtime 是引擎执行 wslc 命令的唯一通道。
//
// 为什么抽成接口：
//   - 生产环境由 internal/wslc.Client 实现，它负责串行化（wslc 预览版并发会
//     ERROR_SHARING_VIOLATION）、隐藏控制台窗口和命令日志；
//   - 测试里用假实现记录每条命令并返回预设结果，编排流程（顺序、失败不连坐、
//     依赖跳过）就能脱离真实的 wslc 验证。
type Runtime interface {
	// Run 执行 wslc 子命令。进程能启动就返回 Result（即使退出码非零）；
	// 只有无法启动、超时、被取消时才返回 error。
	Run(ctx context.Context, args ...string) (Result, error)
	// RunStream 同 Run，但把每一行输出实时交给 onLine（stdout 与 stderr 合并）。
	RunStream(ctx context.Context, onLine func(string), args ...string) (Result, error)
	// Follow 同 RunStream，但**不占用串行锁**，专给 `logs -f` 这类长期存活的只读流。
	// 如果它也排队，一条没结束的 logs -f 就会把后续所有命令堵死。
	Follow(ctx context.Context, onLine func(string), args ...string) (Result, error)
}

// WslcError 表示一条 wslc 命令执行失败。
type WslcError struct{ Msg string }

func (e *WslcError) Error() string { return e.Msg }

// wslc 在这两类错误上可能是暂时性的：容器刚停止时，内核对象不一定已经释放
// （下一次 start 会报 ERROR_ALREADY_EXISTS）；预览版的会话存储不允许调用重叠
// （ERROR_SHARING_VIOLATION）。有限次重试就能越过这个竞态；
// 如果是机器级的持久锁，重试用完后仍会失败。
var transientMarkers = []string{"ERROR_ALREADY_EXISTS", "ERROR_SHARING_VIOLATION"}

const (
	transientRetries = 5
	transientDelay   = 2 * time.Second
)

// engine 把 Runtime 包装成「失败即 error」的便利方法，并负责 dry-run、重试和结果缓存。
type engine struct {
	rt     Runtime
	dryRun bool
	// out 接收 dry-run 打印的命令和命令之外的进度信息（"Creating x ..."）。
	out func(string)
	// sleep 可替换，测试里不真睡。
	sleep func(context.Context, time.Duration) error

	mu         sync.Mutex
	runOptions map[string]bool
	haveRunOpt bool
}

func newEngine(rt Runtime, out func(string)) *engine {
	return &engine{rt: rt, out: out, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// info 输出一行进度。
func (e *engine) info(format string, args ...any) {
	if e.out != nil {
		e.out(fmt.Sprintf(format, args...))
	}
}

// run 执行一条 wslc 命令；退出码非零时返回 *WslcError。
// dry-run 模式下只打印将要执行的命令，不真正执行。
func (e *engine) run(ctx context.Context, args ...string) (Result, error) {
	if e.dryRun {
		e.info("+ wslc %s", strings.Join(args, " "))
		return Result{Args: args}, nil
	}
	res, err := e.rt.Run(ctx, args...)
	if err != nil {
		return res, err
	}
	if !res.Ok() {
		return res, &WslcError{Msg: fmt.Sprintf("wslc %s failed (exit %d): %s",
			strings.Join(firstN(args, 2), " "), res.ExitCode, strings.TrimSpace(res.Error().Error()))}
	}
	return res, nil
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// runStream 与 run 相同，但把输出逐行交给 onLine（用于 pull / build 的进度）。
func (e *engine) runStream(ctx context.Context, onLine func(string), args ...string) (Result, error) {
	if e.dryRun {
		e.info("+ wslc %s", strings.Join(args, " "))
		return Result{Args: args}, nil
	}
	res, err := e.rt.RunStream(ctx, onLine, args...)
	if err != nil {
		return res, err
	}
	if !res.Ok() {
		return res, &WslcError{Msg: fmt.Sprintf("wslc %s failed (exit %d): %s",
			strings.Join(firstN(args, 2), " "), res.ExitCode, lastLines(res.Combined(), 3))}
	}
	return res, nil
}

// lastLines 取输出的最后 n 行，作为错误摘要（完整输出已经通过 onLine 展示过了）。
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// runRetried 与 run 相同，但遇到暂时性错误会重试（最多 transientRetries 次）。
func (e *engine) runRetried(ctx context.Context, args ...string) (Result, error) {
	var (
		res Result
		err error
	)
	for attempt := 1; ; attempt++ {
		res, err = e.run(ctx, args...)
		if err == nil || !isTransient(err) || attempt >= transientRetries {
			return res, err
		}
		e.info("warning: wslc %s hit a transient error, retrying in %s (%d/%d)...",
			strings.Join(firstN(args, 2), " "), transientDelay, attempt, transientRetries-1)
		if serr := e.sleep(ctx, transientDelay); serr != nil {
			return res, serr
		}
	}
}

func isTransient(err error) bool {
	for _, m := range transientMarkers {
		if strings.Contains(err.Error(), m) {
			return true
		}
	}
	return false
}

// --- 查询 -------------------------------------------------------------------

// queryJSON 执行只读命令并把输出解析成 JSON 对象列表。
// wslc 的 --format json 输出是 NDJSON（每行一个对象），inspect 输出是数组，这里两者都接受。
// 查询命令在 dry-run 时也照常执行：它们不改变任何东西，而 dry-run 需要真实状态才能给出准确的预览。
func (e *engine) queryJSON(ctx context.Context, args ...string) ([]map[string]any, error) {
	res, err := e.rt.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !res.Ok() {
		return nil, &WslcError{Msg: fmt.Sprintf("wslc %s failed (exit %d): %s",
			strings.Join(firstN(args, 2), " "), res.ExitCode, strings.TrimSpace(res.Error().Error()))}
	}
	return decodeJSONObjects(res.Stdout)
}

// decodeJSONObjects 把「一个数组」或「每行一个对象」的 JSON 文本解析成对象列表。
func decodeJSONObjects(text string) ([]map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if text[0] == '[' {
		var out []map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			return nil, fmt.Errorf("unexpected non-JSON output from wslc: %w", err)
		}
		return out, nil
	}
	var out []map[string]any
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return nil, fmt.Errorf("unexpected non-JSON output from wslc: %w", err)
		}
		out = append(out, obj)
	}
	return out, nil
}

// inspect 返回对象（容器、网络、镜像……）的 inspect 数据；对象不存在时返回 nil。
func (e *engine) inspect(ctx context.Context, kind, id string) (map[string]any, error) {
	args := []string{"inspect", id}
	if kind != "" {
		args = []string{kind, "inspect", id}
	}
	res, err := e.rt.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !res.Ok() {
		return nil, nil // 不存在（或不可读）：交给调用方按「没有」处理
	}
	objs, err := decodeJSONObjects(res.Stdout)
	if err != nil || len(objs) == 0 {
		return nil, err
	}
	return objs[0], nil
}

// names 返回某类资源（network / volume）现有的名字集合。
func (e *engine) names(ctx context.Context, kind string) (map[string]bool, error) {
	objs, err := e.queryJSON(ctx, kind, "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, o := range objs {
		if n, _ := o["Name"].(string); n != "" {
			out[n] = true
		}
	}
	return out, nil
}

var runOptionRe = regexp.MustCompile(`--[a-z0-9][a-z0-9-]*`)

// supportsRunOption 报告已安装的 wslc 是否支持某个 `run` 选项。
// 不同版本的 wslc 支持的选项不一样（--restart、--cap-add 要 3.0.5 以上），
// 向旧版传不认识的选项会让整条命令失败，所以只在支持时才传。
// 查不到帮助文本时一律按「不支持」处理：宁可少传选项，也不能让 up 整个失败。
func (e *engine) supportsRunOption(ctx context.Context, flag string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.haveRunOpt {
		e.haveRunOpt = true
		e.runOptions = map[string]bool{}
		if res, err := e.rt.Run(ctx, "run", "--help"); err == nil {
			for _, m := range runOptionRe.FindAllString(res.Stdout+"\n"+res.Stderr, -1) {
				e.runOptions[m] = true
			}
		}
	}
	return e.runOptions[flag]
}

// imageEntrypoint 返回本地镜像的 ENTRYPOINT；镜像不在本地时 found 为 false。
func (e *engine) imageEntrypoint(ctx context.Context, name string) (ep []string, found bool) {
	img, err := e.inspect(ctx, "image", name)
	if err != nil || img == nil {
		return nil, false
	}
	cfg, _ := img["Config"].(map[string]any)
	list, _ := cfg["Entrypoint"].([]any)
	for _, v := range list {
		if s, ok := v.(string); ok {
			ep = append(ep, s)
		}
	}
	return ep, true
}

// splitImageName 把镜像名拆成 (仓库, 标签)；标签是最后一个 '/' 之后的 ':' 之后的部分，
// 没写标签就是 latest。
// 例：localhost:5000/app:1.0 → (localhost:5000/app, 1.0)；nginx → (nginx, latest)。
func splitImageName(name string) (repo, tag string) {
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	head := ""
	last := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		head, last = name[:i+1], name[i+1:]
	}
	if repoLast, t, ok := strings.Cut(last, ":"); ok {
		return head + repoLast, t
	}
	return head + last, "latest"
}

// shortRepository 去掉默认仓库前缀：docker.io/library/nginx 与 nginx 指同一个本地镜像。
func shortRepository(repo string) string {
	for _, prefix := range []string{"docker.io/library/", "docker.io/", "index.docker.io/library/"} {
		if strings.HasPrefix(repo, prefix) {
			return repo[len(prefix):]
		}
	}
	return strings.TrimPrefix(repo, "library/")
}

// imageExists 判断本地是否已有某个镜像。
func (e *engine) imageExists(ctx context.Context, name string) bool {
	repo, tag := splitImageName(name)
	imgs, err := e.queryJSON(ctx, "images", "--format", "json")
	if err != nil {
		return false
	}
	for _, img := range imgs {
		r, _ := img["Repository"].(string)
		t, _ := img["Tag"].(string)
		if shortRepository(r) == shortRepository(repo) && t == tag {
			return true
		}
	}
	return false
}
