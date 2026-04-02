package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxEntries = 200

func init() {
	Register(&Tool{
		Name:            "list_files",
		SubAgentAllowed: true,
		IsReadOnly:      true,
		MaxResultChars:  2000,
		Description: `列出指定目录下的文件和子目录，返回树形结构。
使用场景：当你需要了解项目结构、查找文件位置时调用此工具。
注意：
- 路径相对于项目根目录，不传 path 则列出根目录
- 默认递归深度 3 层，最多返回 200 个条目
- 自动跳过 .git、node_modules、__pycache__、.venv 等目录`,
		Parameters: json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "目录路径，相对于项目根目录（默认根目录）"
		},
		"max_depth": {
			"type": "integer",
			"description": "最大递归深度（默认 3）"
		}
	}
}`),
		Execute: func(args json.RawMessage, _ *ReadCache) (string, error) {
			return executeListFiles(args)
		},
	})
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true,
	".venv": true, "venv": true, ".idea": true, ".vscode": true,
}

func executeListFiles(args json.RawMessage) (string, error) {
	var input struct {
		Path     string `json:"path"`
		MaxDepth int    `json:"max_depth"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if input.MaxDepth <= 0 {
		input.MaxDepth = 3
	}
	if input.MaxDepth > 6 {
		input.MaxDepth = 6
	}

	root := resolvePath(input.Path)
	if !isUnderWorkspace(root) {
		return "", fmt.Errorf("路径不在允许范围内: %s", input.Path)
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("路径不存在: %s", input.Path)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s 不是目录，请使用 read_file 读取文件内容", input.Path)
	}

	var sb strings.Builder
	displayRoot := input.Path
	if displayRoot == "" {
		displayRoot = "."
	}
	sb.WriteString(fmt.Sprintf("目录: %s\n\n", displayRoot))

	count := 0
	walkDir(root, root, 0, input.MaxDepth, &sb, &count)

	if count >= maxEntries {
		sb.WriteString(fmt.Sprintf("\n（已达 %d 条上限，部分内容未显示。请缩小目录范围或减少深度）\n", maxEntries))
	}
	sb.WriteString(fmt.Sprintf("\n共 %d 个条目\n", count))
	return sb.String(), nil
}

func walkDir(root, dir string, depth, maxDepth int, sb *strings.Builder, count *int) {
	if *count >= maxEntries || depth >= maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	prefix := strings.Repeat("  ", depth)
	for _, e := range entries {
		if *count >= maxEntries {
			return
		}

		name := e.Name()
		if skipDirs[name] && e.IsDir() {
			continue
		}

		*count++
		if e.IsDir() {
			fmt.Fprintf(sb, "%s%s/\n", prefix, name)
			walkDir(root, filepath.Join(dir, name), depth+1, maxDepth, sb, count)
		} else {
			info, err := e.Info()
			sizeStr := ""
			if err == nil {
				sizeStr = formatSize(info.Size())
			}
			fmt.Fprintf(sb, "%s%s  %s\n", prefix, name, sizeStr)
		}
	}
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("(%.1fMB)", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("(%.1fKB)", float64(bytes)/1024)
	default:
		return fmt.Sprintf("(%dB)", bytes)
	}
}
