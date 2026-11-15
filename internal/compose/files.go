// 本文件负责 compose 项目里「可编辑文件」的发现与读写。
//
// 边界：这里只做**文件 IO**，不解析 compose 语义。
// 谁是可编辑文件（compose.yaml / compose.override.yaml / .env）由 compose-go
// 的加载结果与命名约定决定，不在这里猜；文件的语义仍然只由 loadProject 负责。
// 界面拿到的是纯文本，改完写回，再由 refreshProjects 重新加载验证。
package compose

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 可编辑文件的种类。界面用它决定语言高亮与文案。
const (
	FileKindCompose = "compose" // compose.yaml 及其变体
	FileKindEnv     = "env"     // .env
)

// maxEditableSize 是允许在编辑器里打开的文件上限。
//
// 编辑器本身百万行也撑得住，但 compose 文件与 .env 正常都是几 KB；
// 有人误把 --env-file 指向一个巨大的日志时，整读进内存再交给编辑器没有意义，
// 不如直接拒绝并说明。
const maxEditableSize = 4 << 20 // 4 MiB

// EditableFile 是一个可以在界面里查看与编辑的文件。
type EditableFile struct {
	// Path 是完整路径，读写都用它。
	Path string
	// Name 是显示用的文件名（不含目录），如 compose.yaml。
	Name string
	// Kind 决定高亮语言与文案。
	Kind string
	// Missing 表示文件还不存在（例如项目里没有 .env）。
	// 界面仍然列出来，让用户能新建；此时 Content 为空。
	Missing bool
	// Err 说明这个文件为什么读不出来（权限、超过大小上限）；为空表示读成功。
	Err string
	// Content 是文件的当前内容（已统一成 \n 换行）。
	Content string
}

// EditableFiles 列出项目的可编辑文件，顺序稳定：
// 先是加载时实际用到的 compose 文件（含 override），然后是 .env。
//
// 为什么用加载结果里的 ComposeFiles 而不是重新扫描目录：
// 多文件叠加、COMPOSE_FILE、显式 -f 都会改变「哪些文件真的生效」，
// 只有加载器知道答案。列出来的就一定是参与编排的，不会误导用户。
//
// 读不出来的文件（权限、太大）不会让整次列举失败：它仍然出现在列表里，
// 只是 Content 为空并在 Err 里说明原因，用户能在界面看到。
func (c *Client) EditableFiles(ctx context.Context, dir string) ([]EditableFile, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("empty project directory")
	}

	files := c.editTargets(ctx, dir)

	out := make([]EditableFile, 0, len(files))
	for _, t := range files {
		f := EditableFile{Path: t.path, Name: filepath.Base(t.path), Kind: t.kind}
		content, err := readEditable(t.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// 不存在是正常情况（.env 常常没有），标记出来供界面新建。
			f.Missing = true
		case err != nil:
			// 单个文件读不了不该毁掉整个列表：标出来，其余照常可编辑。
			f.Err = err.Error()
		default:
			f.Content = content
		}
		out = append(out, f)
	}
	return out, nil
}

// editTarget 是一个待读写的文件及其种类。
type editTarget struct {
	path string
	kind string
}

// editTargets 决定有哪些文件可编辑。
//
// 顺序与去重都要稳定：界面按顺序生成标签页，每次刷新都换顺序会让用户
// 正在编辑的标签跳来跳去。
func (c *Client) editTargets(ctx context.Context, dir string) []editTarget {
	var out []editTarget
	seen := map[string]bool{}

	add := func(path, kind string) {
		if path == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, editTarget{path: filepath.Clean(path), kind: kind})
	}

	// 先问加载器实际用了哪些 compose 文件。
	//
	// 加载失败（YAML 写坏了、变量没定义）时仍然要能编辑——恰恰是这种时候用户
	// 最需要打开文件改，所以这里失败就退回目录探测，而不是把错误抛给界面。
	if p, err := c.LoadProject(ctx, dir); err == nil {
		for _, f := range p.Config.ComposeFiles {
			add(absIn(dir, f), FileKindCompose)
		}
		if p.Config.EnvFile != nil {
			add(absIn(dir, *p.Config.EnvFile), FileKindEnv)
		}
	}
	if len(out) == 0 {
		// 加载失败或加载器没报告文件：用命名约定探测。
		if f := FindComposeFile(dir); f != "" {
			add(f, FileKindCompose)
			// 与主文件同目录、同主名的 override 文件也列出来。
			// compose-go 会自动叠加 compose.override.yaml，用户常常要在其中改端口。
			base := strings.TrimSuffix(f, filepath.Ext(f))
			for _, suffix := range []string{".override", ".override" + filepath.Ext(f)} {
				if st, err := os.Stat(base + suffix); err == nil && !st.IsDir() {
					add(base+suffix, FileKindCompose)
					break
				}
			}
		}
	}

	// .env 总是列出来，即便不存在：新建 .env 是很常见的操作。
	add(filepath.Join(dir, ".env"), FileKindEnv)

	return out
}

// readEditable 读出一个可编辑文件，并统一成 \n 换行。
//
// 统一换行是必要的：编辑器内部一律用 \n 组织行，写入时再由 WriteEditable
// 还原成文件原本的换行符。保留 \r\n 进缓冲区会让每行末尾多出一个不可见字符，
// 光标与列号随之错位。
func readEditable(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if st.Size() > maxEditableSize {
		return "", fmt.Errorf("%s is %d bytes, larger than the %d bytes an editor opens",
			filepath.Base(path), st.Size(), maxEditableSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

// WriteEditable 把内容写回文件，返回写到的路径。
//
// 三件事与「直接用 os.WriteFile」不同：
//
//  1. **保留原换行符。** Windows 上的文件多是 CRLF，而编辑器给的是 LF。
//     若原样写入，整个文件的行尾都会被改写，diff 里全是噪声。
//  2. **原子替换。** 先写同目录临时文件再改名。编辑器里 Ctrl+S 是随时会按的，
//     写到一半崩溃或断电就得到一个截断的 compose.yaml——那会让项目直接起不来。
//     同目录是关键：跨卷改名不是原子的。
//  3. **保留文件原有权限。** 临时文件默认 0600，改名会连带改掉权限。
func WriteEditable(path, content string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("empty file path")
	}

	// 判断原文件用哪种换行：读原文，看到第一处 \r\n 就按 CRLF 写。
	// 新文件（不存在）沿用平台习惯：Windows 上 CRLF，其它平台 LF。
	crlf := defaultCRLF()
	if data, err := os.ReadFile(path); err == nil {
		crlf = strings.Contains(string(data), "\r\n")
	}

	text := strings.ReplaceAll(content, "\r\n", "\n")
	if crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}

	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		if st.IsDir() {
			return fmt.Errorf("%s is a directory", path)
		}
		mode = st.Mode().Perm()
	}

	dir := filepath.Dir(path)
	// MkdirAll 而不是 Mkdir：新建项目时目录可能整条都不存在。
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// 任何一步失败都清掉临时文件，别在用户的目录里留垃圾。
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if _, err := tmp.WriteString(text); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	// 先把内容刷到磁盘，再改名：否则崩溃时可能改完名但内容还没落盘。
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("set mode of %s: %w", tmpName, err)
	}
	// Windows 上 os.Rename 不能覆盖已存在的文件，所以先删旧的。
	// 中间有个极短的空窗期，但比「写一半的 compose.yaml」安全得多。
	if err := os.Rename(tmpName, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			os.Remove(tmpName)
			return fmt.Errorf("replace %s: %w", path, err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("replace %s: %w", path, err)
		}
	}
	return nil
}

// defaultCRLF 报告新文件应当使用哪种换行符。
//
// 只在「原文件不存在」时用得上：Windows 上的编辑器与 Git 默认写 CRLF，
// 新建的 compose.yaml 跟随这个习惯，用户用记事本打开才不会看到整片挤在一行。
func defaultCRLF() bool { return runtime.GOOS == "windows" }
