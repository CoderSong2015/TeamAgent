package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	Register(&Tool{
		Name: "write_file",
		Description: `创建新文件或完全覆盖现有文件。
注意：
- 如果文件已存在，将被完全覆盖
- 修改现有文件请优先使用 edit_file 工具
- 自动创建不存在的父目录
- 最大写入 200KB
适合：创建新文件、生成配置文件、写入全新内容。`,
		Parameters: json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "文件路径，相对于项目根目录"
		},
		"content": {
			"type": "string",
			"description": "文件的完整内容"
		}
	},
	"required": ["path", "content"]
}`),
		Execute: executeWriteFile,
	})
}

func executeWriteFile(args json.RawMessage, cache *ReadCache) (string, error) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if input.Path == "" {
		return "", fmt.Errorf("path 参数不能为空")
	}

	absPath := resolvePath(input.Path)
	if !isUnderWorkspace(absPath) {
		return "", fmt.Errorf("路径不在允许范围内: %s", input.Path)
	}
	if err := isWriteAllowed(absPath); err != nil {
		return "", err
	}

	contentBytes := []byte(input.Content)
	if len(contentBytes) > MaxWriteSize {
		return "", fmt.Errorf("内容过大（%d bytes，限制 %d bytes）", len(contentBytes), MaxWriteSize)
	}

	_, existErr := os.Stat(absPath)
	isNew := os.IsNotExist(existErr)

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	if err := os.WriteFile(absPath, contentBytes, 0644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	if cache != nil {
		cache.Invalidate(absPath)
	}

	lines := strings.Count(input.Content, "\n") + 1
	action := "覆盖写入"
	if isNew {
		action = "新建"
	}
	return fmt.Sprintf("已%s %s（%d 行, %d bytes）", action, input.Path, lines, len(contentBytes)), nil
}
