package tools

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func init() {
	Register(&Tool{
		Name:            "grep_search",
		SubAgentAllowed: true,
		IsReadOnly:      true,
		MaxResultChars:  2000,
		Description: `在项目文件中搜索匹配正则表达式的内容，返回匹配的文件名和行内容。
使用场景：当你需要查找某个函数定义、变量引用、特定字符串或代码模式时调用此工具。
注意：
- pattern 支持基本正则表达式
- 默认搜索项目根目录，可通过 path 限定子目录
- 可通过 include 过滤文件类型（如 *.go, *.py）`,
		Parameters: json.RawMessage(`{
	"type": "object",
	"properties": {
		"pattern": {
			"type": "string",
			"description": "搜索的正则表达式"
		},
		"path": {
			"type": "string",
			"description": "搜索目录，相对于项目根目录（默认根目录）"
		},
		"include": {
			"type": "string",
			"description": "文件过滤，如 *.go, *.py（可选）"
		}
	},
	"required": ["pattern"]
}`),
		Execute: func(args json.RawMessage, _ *ReadCache) (string, error) {
			return executeGrepSearch(args)
		},
	})
}

func executeGrepSearch(args json.RawMessage) (string, error) {
	var input struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
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

	grepArgs := []string{
		"-rn",
		"--color=never",
		"-E",
	}

	for _, skip := range []string{".git", "node_modules", "__pycache__", ".venv", "venv"} {
		grepArgs = append(grepArgs, "--exclude-dir="+skip)
	}

	if input.Include != "" {
		grepArgs = append(grepArgs, "--include="+input.Include)
	}

	grepArgs = append(grepArgs, input.Pattern, searchDir)

	cmd := exec.Command("grep", grepArgs...)
	out, err := cmd.CombinedOutput()

	output := string(out)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return fmt.Sprintf("未找到匹配 \"%s\" 的内容", input.Pattern), nil
		}
		if len(output) > 0 {
			return "", fmt.Errorf("grep 执行错误: %s", output)
		}
		return "", fmt.Errorf("grep 执行错误: %w", err)
	}

	// Strip workspace prefix from paths for readability
	output = strings.ReplaceAll(output, WorkspacePath+"/", "")

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 100 {
		output = strings.Join(lines[:100], "\n")
		output += fmt.Sprintf("\n\n（共 %d 条匹配，仅显示前 100 条。请缩小搜索范围）", len(lines))
	}

	return fmt.Sprintf("搜索 \"%s\" 结果（%d 条匹配）:\n\n%s", input.Pattern, len(lines), output), nil
}
