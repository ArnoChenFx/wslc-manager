package app

import (
	"embed"
	"sync"

	"github.com/egoist/mygo/ui"
)

// iconsFS 内嵌所有图标 SVG。图标来自 Lucide（MIT 许可），
// 统一 24×24 viewBox、线性描边、currentColor 着色，
// 因此能自动跟随文字颜色（浅色/深色主题都适用）。
//
//go:embed icons/*.svg
var iconsFS embed.FS

var (
	iconOnce sync.Once
	iconSet  map[string]*ui.SVG
)

// icon 返回指定名字的图标，未找到时返回 nil（ui.Icon 对 nil 是安全的）。
func icon(name string) *ui.SVG {
	iconOnce.Do(func() {
		iconSet = map[string]*ui.SVG{}
		entries, err := iconsFS.ReadDir("icons")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := iconsFS.ReadFile("icons/" + e.Name())
			if err != nil {
				continue
			}
			svg, perr := ui.ParseSVG(data)
			if perr != nil {
				// 图标解析失败不影响主流程，只是不显示这个图标。
				continue
			}
			name := e.Name()
			// 去掉 .svg 后缀作为键。
			if n := len(name) - 4; n > 0 {
				name = name[:n]
			}
			iconSet[name] = svg
		}
	})
	return iconSet[name]
}
