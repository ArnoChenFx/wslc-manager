package app

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// LogLevel 是日志级别。
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String 返回级别的显示名。
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return "?"
}

// LogEntry 是一行日志。
type LogEntry struct {
	// Time 是记录时刻。
	Time time.Time
	// Level 是级别。
	Level LogLevel
	// Source 是来源标签，例如 "wslc"、"compose"、"ui"、"refresh"。
	Source string
	// Message 是正文（可含多行）。
	Message string
}

// Line 渲染成一行文本，格式：15:04:05.123 INFO  [wslc] 正文
func (e LogEntry) Line() string {
	msg := strings.ReplaceAll(e.Message, "\n", " ⏎ ")
	return fmt.Sprintf("%s %-5s [%s] %s",
		e.Time.Format("15:04:05.000"), e.Level.String(), e.Source, msg)
}

// logBufferSize 是环形缓冲保留的日志行数。
//
// 取 500：足够覆盖一次完整的刷新 + 若干次用户操作，
// 又不会让导出报告过大。诊断问题通常只需要最后几十行。
const logBufferSize = 500

// logBuffer 是固定容量的环形日志缓冲，可并发安全读写。
type logBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry // 环形数组，长度固定
	start   int        // 最旧一条的位置
	count   int        // 当前条数
	// version 每次追加都自增，用于让 UI 判断是否需要重绘。
	version uint64
	// echo 为真时同时写 stderr，便于在 `mygo dev` 终端里直接看。
	// 命令行工具（cmd/diagtree 等）会关掉它，避免污染标准输出。
	echo bool
}

// SetEcho 控制是否同时输出到 stderr。
func (b *logBuffer) SetEcho(on bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.echo = on
}

func newLogBuffer(capacity int) *logBuffer {
	if capacity <= 0 {
		capacity = logBufferSize
	}
	return &logBuffer{entries: make([]LogEntry, capacity), echo: true}
}

// Add 追加一条日志。
func (b *logBuffer) Add(e LogEntry) {
	if b == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b.mu.Lock()
	if b.count < len(b.entries) {
		b.entries[(b.start+b.count)%len(b.entries)] = e
		b.count++
	} else {
		b.entries[b.start] = e
		b.start = (b.start + 1) % len(b.entries)
	}
	b.version++
	echo := b.echo
	b.mu.Unlock()

	// 终端回显放在锁外，避免慢 IO 阻塞日志写入。
	if echo {
		fmt.Fprintln(os.Stderr, e.Line())
	}
}

// Snapshot 返回按时间顺序排列的日志副本。
func (b *logBuffer) Snapshot() []LogEntry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]LogEntry, 0, b.count)
	for i := 0; i < b.count; i++ {
		out = append(out, b.entries[(b.start+i)%len(b.entries)])
	}
	return out
}

// Version 返回当前版本号，UI 用它判断是否有新日志。
func (b *logBuffer) Version() uint64 {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.version
}

// Tail 返回最后 n 条日志。
func (b *logBuffer) Tail(n int) []LogEntry {
	all := b.Snapshot()
	if n <= 0 || n >= len(all) {
		return all
	}
	return all[len(all)-n:]
}

// Clear 清空日志。
func (b *logBuffer) Clear() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.start, b.count = 0, 0
	b.version++
}

// Text 把日志渲染成纯文本，用于导出。
func (b *logBuffer) Text() string {
	var sb strings.Builder
	for _, e := range b.Snapshot() {
		sb.WriteString(e.Line())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ---- App 上的日志便捷方法 ----

// log 追加一条日志。它可从任意协程调用。
func (a *App) log(level LogLevel, source, format string, args ...any) {
	if a.logs == nil {
		return
	}
	a.logs.Add(LogEntry{
		Level:   level,
		Source:  source,
		Message: fmt.Sprintf(format, args...),
	})
}

// logDebug 记录调试信息。
func (a *App) logDebug(source, format string, args ...any) {
	a.log(LevelDebug, source, format, args...)
}

// logInfo 记录普通信息。
func (a *App) logInfo(source, format string, args ...any) {
	a.log(LevelInfo, source, format, args...)
}

// logWarn 记录警告。
func (a *App) logWarn(source, format string, args ...any) {
	a.log(LevelWarn, source, format, args...)
}

// logError 记录错误。
func (a *App) logError(source, format string, args ...any) {
	a.log(LevelError, source, format, args...)
}

// Quiet 关闭日志的终端回显，供命令行工具使用。
func (a *App) Quiet() {
	if a.logs != nil {
		a.logs.SetEcho(false)
	}
}
