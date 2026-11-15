// 本文件负责「从零创建一个 compose 项目」：把用户写的 compose.yaml 与 .env 落盘。
//
// 边界：只做「校验 + 写文件」，不预置任何编排状态、不解析 compose 语义。
// 项目能不能真正跑起来由加载器（loadProject）判断，界面拿它的结果展示。
//
// 为什么不在这里塞一堆参数（服务名、镜像、端口）：那是替用户做决定，
// 生成的模板总有人要改。让用户直接写文件内容更直接——界面里用的就是
// 应用自带的代码编辑器，带 YAML 高亮，比填一堆表单再猜生成结果省事。
package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultComposeYAML 是新建项目时预填的模板。
//
// 刻意给一个「能直接跑」的最小例子（nginx + 端口映射），而不是空文件：
// 空编辑框只会让用户先楞一下，再去找文档。用户改的就是这份内容本身。
const DefaultComposeYAML = `services:
  app:
    image: nginx:alpine
    ports:
      - "8080:80"
`

// NewProjectResult 报告生成了哪些文件。
type NewProjectResult struct {
	Dir   string
	Files []string
}

// CreateProject 在 dir 里写出 compose.yaml 与 .env。
//
// composeText 是 compose.yaml 的完整内容，envText 是 .env 的内容。
//
// 安全策略（这些检查是**故意**严格的）：
//   - 目录里已经有任何文件就拒绝。一个「创建项目」按钮不该覆盖用户已有的东西，
//     哪怕只多出一个 README。
//   - composeText 先做 YAML 语法校验再落盘：语法错就带着行号报错，
//     用户当场改，不会得到一个打不开的「坏项目」。
//     只查语法，不查语义——变量插值、include 之类交给加载器，
//     在这里替用户猜「能不能跑」只会把合理的项目也拦下来。
//   - envText 为空（或只有空白）时不创建 .env：用户没写内容，
//     多半就是不需要这个文件，凭空造一个空文件只会让人困惑。
func CreateProject(dir, composeText, envText string) (NewProjectResult, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return NewProjectResult{}, errors.New("project directory is empty")
	}
	if strings.TrimSpace(composeText) == "" {
		return NewProjectResult{}, errors.New("compose.yaml is empty")
	}
	// YAML 语法校验放在写盘之前：失败时磁盘上什么都没发生。
	if err := checkYAMLSyntax(composeText); err != nil {
		return NewProjectResult{}, err
	}

	// 目录里已有内容就拒绝。先看目录本身在不在：
	// 不存在是正常情况（要新建），存在就要检查是否为空。
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return NewProjectResult{}, fmt.Errorf("%s is a file, not a directory", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return NewProjectResult{}, fmt.Errorf("read %s: %w", dir, err)
		}
		if len(entries) > 0 {
			return NewProjectResult{}, fmt.Errorf("%s is not empty; create the project in a new "+
				"directory or an empty one", dir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return NewProjectResult{}, fmt.Errorf("inspect %s: %w", dir, err)
	}

	// 这里不自己 MkdirAll：WriteEditable 写文件前会把目录建出来。
	// 早先两边都建，删掉上面这个判断也不影响结果——变异验证把它抓了出来，
	// 说明当时那一行是死代码，留着只会让人以为缺了它会失败。

	res := NewProjectResult{Dir: dir}
	composePath := filepath.Join(dir, "compose.yaml")
	// 用 WriteEditable 而不是 os.WriteFile：换行符与权限规则跟编辑器一致，
	// 而且是原子写，失败时不会留下半个文件。
	if err := WriteEditable(composePath, composeText); err != nil {
		return NewProjectResult{}, err
	}
	res.Files = append(res.Files, composePath)

	if strings.TrimSpace(envText) != "" {
		envPath := filepath.Join(dir, ".env")
		if err := WriteEditable(envPath, envText); err != nil {
			// 回滚已经写出的 compose.yaml：半成品项目比没有更让人困惑。
			os.Remove(composePath)
			return NewProjectResult{}, err
		}
		res.Files = append(res.Files, envPath)
	}

	return res, nil
}

// checkYAMLSyntax 报告 compose 文本里的 YAML 语法错误。
//
// 只解到 Node：解析成具体结构会把 compose 的字段规则也一并套上，
// 那属于加载器的职责，在这里做会重复实现一遍并且容易过时。
func checkYAMLSyntax(text string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		// yaml.v3 的错误信息里已经带了行号，直接透出即可。
		return fmt.Errorf("compose.yaml is not valid YAML: %w", err)
	}
	return nil
}
