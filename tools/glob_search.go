package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	Register(&Tool{
		Name:            "glob_search",
		SubAgentAllowed: true,
		IsReadOnly:      true,
		MaxResultChars:  2000,
		Description: `按文件名模式搜索项目文件，结果按修改时间排序（最近修改的在前）。
使用场景：知道文件名但不确定位置时快速定位。
支持 glob 模式：
- **/*.go — 递归查找所有 .go 文件
- **/test_*.py — 递归查找所有 test_*.py 文件
- src/**/config*.yaml — src 目录下的 config*.yaml 文件
注意：不含 ** 的模式会自动加 **/ 前缀以递归搜索。最多返回 100 个结果。`,
		Parameters: json.RawMessage(`{
	"type": "object",
	"properties": {
		"pattern": {
			"type": "string",
			"description": "Glob 模式（如 **/*.go, **/test_*.py）"
		},
		"path": {
			"type": "string",
			"description": "搜索目录，相对于项目根目录（默认根目录）"
		}
	},
	"required": ["pattern"]
}`),
		Execute: func(args json.RawMessage, _ *ReadCache) (string, error) {
			return executeGlobSearch(args)
		},
	})
}

type globMatch struct {
	RelPath string
	ModTime int64
}

func executeGlobSearch(args json.RawMessage) (string, error) {
	var input struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if input.Pattern == "" {
		return "", fmt.Errorf("pattern 参数不能为空")
	}

	searchDir := resolvePath(input.Path)
	if !isUnderWorkspace(searchDir) {
		return "", fmt.Errorf("路径不在允许范围内: %s", input.Path)
	}

	pattern := input.Pattern
	if !strings.Contains(pattern, "**") {
		pattern = "**/" + pattern
	}

	var matches []globMatch
	const maxResults = 100

	_ = filepath.WalkDir(searchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(searchDir, path)
		if matchDoublestar(pattern, rel) {
			info, infoErr := d.Info()
			var mtime int64
			if infoErr == nil {
				mtime = info.ModTime().UnixNano()
			}
			matches = append(matches, globMatch{RelPath: rel, ModTime: mtime})
		}
		return nil
	})

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].ModTime > matches[j].ModTime
	})

	if len(matches) == 0 {
		return fmt.Sprintf("未找到匹配 \"%s\" 的文件", input.Pattern), nil
	}

	total := len(matches)
	if total > maxResults {
		matches = matches[:maxResults]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("匹配 \"%s\" 的文件（%d 个", input.Pattern, total))
	if total > maxResults {
		sb.WriteString(fmt.Sprintf("，仅显示最近修改的 %d 个", maxResults))
	}
	sb.WriteString("）：\n\n")
	for _, m := range matches {
		sb.WriteString(m.RelPath + "\n")
	}
	return sb.String(), nil
}

// matchDoublestar 实现支持 ** 的 glob 匹配
func matchDoublestar(pattern, path string) bool {
	patParts := strings.Split(filepath.ToSlash(pattern), "/")
	pathParts := strings.Split(filepath.ToSlash(path), "/")
	return matchParts(patParts, pathParts)
}

func matchParts(pat, path []string) bool {
	for len(pat) > 0 && len(path) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if matchParts(rest, path[i:]) {
					return true
				}
			}
			return false
		}
		matched, _ := filepath.Match(pat[0], path[0])
		if !matched {
			return false
		}
		pat = pat[1:]
		path = path[1:]
	}
	return len(pat) == 0 && len(path) == 0
}
