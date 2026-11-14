package compose

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 扫描时跳过的目录名：这些目录要么没有 compose 文件，要么体量巨大
// 且几乎不可能包含用户想管理的项目。
var skipDirs = map[string]bool{
	"node_modules":              true,
	".git":                      true,
	".svn":                      true,
	".hg":                       true,
	"vendor":                    true,
	"dist":                      true,
	"build":                     true,
	"target":                    true,
	".venv":                     true,
	"venv":                      true,
	"__pycache__":               true,
	".idea":                     true,
	".vscode":                   true,
	"AppData":                   true,
	"Windows":                   true,
	"$Recycle.Bin":              true,
	"System Volume Information": true,
	"Program Files":             true,
	"Program Files (x86)":       true,
}

// ScanOptions 控制项目扫描行为。
type ScanOptions struct {
	// Roots 是扫描起点目录。
	Roots []string
	// Depth 是最大递归深度，0 表示只看 Roots 本身。
	Depth int
	// MaxDirs 是访问目录数的上限，防止在超大目录树上卡住。
	MaxDirs int
}

// Found 是一个被发现的项目目录。
type Found struct {
	// Dir 是包含 compose 文件的目录。
	Dir string
	// File 是命中的 compose 文件路径。
	File string
}

// Scan 在给定根目录里递归查找包含 compose 文件的目录。
//
// 返回结果按目录名排序，并做了去重（同一个目录只看作一个项目）。
// 扫描是「尽力而为」的：没有权限的目录会被静默跳过。
func Scan(opts ScanOptions) []Found {
	if opts.MaxDirs <= 0 {
		opts.MaxDirs = 4000
	}
	seen := map[string]bool{}
	var out []Found
	visited := 0

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if visited >= opts.MaxDirs {
			return
		}
		visited++

		// 命中 compose 文件的目录就是一个项目；命中后不再深入，
		// 因为子目录通常属于同一个项目（例如 examples/）。
		if file := FindComposeFile(dir); file != "" {
			key := strings.ToLower(filepath.Clean(dir))
			if !seen[key] {
				seen[key] = true
				out = append(out, Found{Dir: dir, File: file})
			}
			return
		}
		if depth >= opts.Depth {
			return
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			// 跳过点开头目录（.git 等）和已知噪声目录。
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				continue
			}
			walk(filepath.Join(dir, name), depth+1)
		}
	}

	for _, root := range opts.Roots {
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			continue
		}
		walk(root, 0)
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Dir) < strings.ToLower(out[j].Dir)
	})
	return out
}

// Ensure fs 被引用（ReadDir 的返回值类型在早期版本中需要显式导入）。
