package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const editBackupDir = "/tmp/chat_server_backups"

func init() {
	Register(&Tool{
		Name: "edit_file",
		Description: `对文件进行精确的字符串替换编辑。
使用前必须先用 read_file 读取文件内容（系统会强制检查）。
注意：
- old_string 必须与文件中的内容完全匹配（包括缩进和空格）
- old_string 在文件中必须唯一（除非设置 replace_all=true）
- 修改后建议运行 run_command 验证（如 go build ./...）
- 适合：修改代码、更新配置、修复 bug
- 工具优先：改文件用 edit_file，不要用 run_command("sed ...")`,
		Parameters: json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "文件路径，相对于项目根目录"
		},
		"old_string": {
			"type": "string",
			"description": "要替换的原始文本（必须与文件中的内容完全匹配）"
		},
		"new_string": {
			"type": "string",
			"description": "替换后的新文本（空字符串表示删除）"
		},
		"replace_all": {
			"type": "boolean",
			"description": "是否替换所有匹配（默认 false，只替换第一个）"
		}
	},
	"required": ["path", "old_string", "new_string"]
}`),
		Execute: executeEditFile,
	})
}

func executeEditFile(args json.RawMessage, cache *ReadCache) (string, error) {
	var input struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if input.Path == "" || input.OldString == "" {
		return "", fmt.Errorf("path 和 old_string 参数不能为空")
	}
	if input.OldString == input.NewString {
		return "", fmt.Errorf("old_string 和 new_string 相同，无需修改")
	}

	absPath := resolvePath(input.Path)
	if !isUnderWorkspace(absPath) {
		return "", fmt.Errorf("路径不在允许范围内: %s", input.Path)
	}
	if err := isWriteAllowed(absPath); err != nil {
		return "", err
	}

	// ── 先读后改检查（Read-Before-Write） ──
	if cache != nil && !cache.HasRead(absPath) {
		return "", fmt.Errorf(
			"请先使用 read_file 读取 %s 的内容，确认要修改的文本后再编辑。"+
				"这样可以确保 old_string 与文件内容完全匹配。", input.Path)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("文件不存在或无法读取: %s", input.Path)
	}
	content := string(data)

	// ── 竞态保护（mtime 检查） ──
	if cache != nil {
		readTime := cache.GetReadTime(absPath)
		if !readTime.IsZero() {
			info, statErr := os.Stat(absPath)
			if statErr == nil && info.ModTime().After(readTime) {
				return "", fmt.Errorf(
					"文件 %s 在你上次读取后已被修改（可能被其他进程更新）。"+
						"请重新使用 read_file 读取最新内容后再编辑。", input.Path)
			}
		}
	}

	// ── 匹配：先精确匹配，失败则尝试引号归一化 ──
	actualOld := input.OldString
	count := strings.Count(content, actualOld)
	usedQuoteNorm := false

	if count == 0 {
		normActual, normCount := tryQuoteNormalization(content, input.OldString)
		if normCount > 0 {
			actualOld = normActual
			count = normCount
			usedQuoteNorm = true
		}
	}

	if count == 0 {
		preview := input.OldString
		runes := []rune(preview)
		if len(runes) > 100 {
			preview = string(runes[:100]) + "..."
		}
		return "", fmt.Errorf(
			"未在文件中找到匹配文本。请确认内容完全正确（包括缩进和空格）。\n"+
				"搜索文本预览: %q", preview)
	}
	if count > 1 && !input.ReplaceAll {
		return "", fmt.Errorf(
			"匹配到 %d 处，存在歧义。请提供更多上下文使匹配唯一，"+
				"或设置 replace_all=true 替换全部", count)
	}

	// ── 删除代码尾部换行处理 ──
	newStr := input.NewString
	if newStr == "" && !strings.HasSuffix(actualOld, "\n") {
		if strings.Contains(content, actualOld+"\n") {
			actualOld = actualOld + "\n"
		}
	}

	// ── 行尾空白清理（非 .md 文件） ──
	if newStr != "" && !strings.HasSuffix(input.Path, ".md") {
		newStr = cleanTrailingWhitespace(newStr)
	}

	// ── 编辑前备份 ──
	backupFile(absPath, data)

	// ── 执行替换 ──
	replaced := count
	var newContent string
	if input.ReplaceAll {
		newContent = strings.ReplaceAll(content, actualOld, newStr)
	} else {
		newContent = strings.Replace(content, actualOld, newStr, 1)
		replaced = 1
	}

	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	if cache != nil {
		cache.Invalidate(absPath)
	}

	// ── 返回结构化 diff ──
	result := buildEditDiff(input.Path, actualOld, newStr, replaced)
	if usedQuoteNorm {
		result += "\n（注：通过引号归一化匹配到目标文本）"
	}
	return result, nil
}

// ── 引号智能匹配 ──

func normalizeQuotes(s string) string {
	s = strings.ReplaceAll(s, "\u201c", "\"") // "
	s = strings.ReplaceAll(s, "\u201d", "\"") // "
	s = strings.ReplaceAll(s, "\u2018", "'")  // '
	s = strings.ReplaceAll(s, "\u2019", "'")  // '
	return s
}

// tryQuoteNormalization 在引号归一化后尝试匹配，返回文件中的原始文本和匹配数
func tryQuoteNormalization(content, search string) (actual string, count int) {
	normSearch := normalizeQuotes(search)
	if normSearch == search {
		return "", 0
	}

	origRunes := []rune(content)
	normRunes := make([]rune, len(origRunes))
	copy(normRunes, origRunes)
	for i, r := range normRunes {
		switch r {
		case '\u201c', '\u201d':
			normRunes[i] = '"'
		case '\u2018', '\u2019':
			normRunes[i] = '\''
		}
	}

	searchRunes := []rune(normSearch)
	searchLen := len(searchRunes)
	if searchLen == 0 || searchLen > len(normRunes) {
		return "", 0
	}

	found := 0
	firstIdx := -1
	for i := 0; i <= len(normRunes)-searchLen; i++ {
		match := true
		for j := 0; j < searchLen; j++ {
			if normRunes[i+j] != searchRunes[j] {
				match = false
				break
			}
		}
		if match {
			if firstIdx < 0 {
				firstIdx = i
			}
			found++
		}
	}

	if found == 0 || firstIdx < 0 {
		return "", 0
	}
	return string(origRunes[firstIdx : firstIdx+searchLen]), found
}

// ── 行尾空白清理 ──

func cleanTrailingWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

// ── 编辑前备份 ──

func backupFile(absPath string, content []byte) {
	_ = os.MkdirAll(editBackupDir, 0755)
	// 纳秒级时间戳 + 路径哈希前缀，避免同名文件在同一秒编辑时冲突
	pathHash := fmt.Sprintf("%08x", fnvHash(absPath))
	name := filepath.Base(absPath) + "." + pathHash + "." + time.Now().Format("20060102_150405.000000000")
	_ = os.WriteFile(filepath.Join(editBackupDir, name), content, 0644)
}

func fnvHash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// ── Diff 输出 ──

func buildEditDiff(path, oldStr, newStr string, count int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("已修改 %s: 替换 %d 处\n", path, count))

	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	const maxDiffLines = 30
	if len(oldLines)+len(newLines) > maxDiffLines {
		sb.WriteString(fmt.Sprintf("（变更较大: %d 行 → %d 行，省略 diff 详情）", len(oldLines), len(newLines)))
		return sb.String()
	}

	sb.WriteString("```diff\n")
	for _, line := range oldLines {
		sb.WriteString("- " + line + "\n")
	}
	for _, line := range newLines {
		sb.WriteString("+ " + line + "\n")
	}
	sb.WriteString("```")
	return sb.String()
}
