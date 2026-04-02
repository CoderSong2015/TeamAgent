package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const MaxReadLines = 200

func init() {
	Register(&Tool{
		Name:            "read_file",
		SubAgentAllowed: true,
		IsReadOnly:      true,
		MaxResultChars:  3000,
		Description: `读取指定路径的文件内容，返回带行号的文本。
使用场景：当你需要查看某个文件的代码或内容时调用此工具。
注意：
- 路径相对于项目根目录
- 单文件最大 100KB，超出请用 offset+limit 分段读取
- 超过 200 行的文件必须使用 offset+limit 指定读取范围
- 读取前不确定路径时，先用 list_files 查看目录结构`,
		Parameters: json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "文件路径，相对于项目根目录"
		},
		"offset": {
			"type": "integer",
			"description": "起始行号（从 1 开始，可选）"
		},
		"limit": {
			"type": "integer",
			"description": "读取行数（可选，默认全部）"
		}
	},
	"required": ["path"]
}`),
		Execute: executeReadFile,
	})
}

func executeReadFile(args json.RawMessage, cache *ReadCache) (string, error) {
	var input struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if input.Path == "" {
		return "", fmt.Errorf("path 参数不能为空")
	}

	absPath := resolvePath(input.Path)
	if !isUnderWorkspace(absPath) {
		return "", fmt.Errorf("路径不在允许范围内: %s（工作目录: %s）", input.Path, WorkspacePath)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("文件不存在: %s", input.Path)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s 是目录，请使用 list_files 工具查看目录内容", input.Path)
	}
	if info.Size() > MaxFileSize {
		return "", fmt.Errorf("文件过大（%d bytes，限制 %d bytes），请使用 offset + limit 参数分段读取",
			info.Size(), MaxFileSize)
	}

	if cache != nil && cache.Check(absPath, input.Offset, input.Limit) {
		return fmt.Sprintf("（文件 %s 内容未变化，与上次读取相同，请直接使用之前的结果）", input.Path), nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	hasRange := input.Offset > 0 || input.Limit > 0
	if !hasRange && len(lines) > MaxReadLines {
		return "", fmt.Errorf(
			"文件 %s 共 %d 行，超过单次读取限制（%d 行）。\n"+
				"请使用以下方式精确读取：\n"+
				"- offset + limit 读取特定行范围（如 offset=1, limit=100 读取前100行）\n"+
				"- 或先用 grep_search 定位关键内容的位置，再用 offset+limit 读取对应区域",
			input.Path, len(lines), MaxReadLines)
	}

	start := 0
	end := len(lines)
	if input.Offset > 0 {
		start = input.Offset - 1
		if start >= len(lines) {
			return "", fmt.Errorf("offset %d 超出文件总行数 %d", input.Offset, len(lines))
		}
	}
	if input.Limit > 0 {
		if start+input.Limit < end {
			end = start + input.Limit
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("文件: %s（共 %d 行", input.Path, len(lines)))
	if hasRange {
		sb.WriteString(fmt.Sprintf("，显示第 %d-%d 行", start+1, end))
	}
	sb.WriteString("）\n\n")

	for i := start; i < end; i++ {
		fmt.Fprintf(&sb, "%4d|%s\n", i+1, lines[i])
	}

	if cache != nil {
		cache.Mark(absPath, input.Offset, input.Limit)
	}

	return sb.String(), nil
}
