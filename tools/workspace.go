package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var WorkspacePath = "/root/stockAnalysis"

const MaxFileSize = 100 * 1024 // 100KB per file

// ── Write Safety ──

var (
	WriteEnabled   = true
	WriteDenyPaths = []string{".git/", "go.sum"}
	MaxWriteSize   = 200 * 1024 // 200KB
)

// ── Command Safety ──

var (
	CommandEnabled    = false
	CommandTimeout    = 30  // seconds
	MaxCommandTimeout = 120 // seconds
)

func init() {
	if os.Getenv("TOOL_WRITE_ENABLED") == "false" {
		WriteEnabled = false
	}
	if os.Getenv("TOOL_COMMAND_ENABLED") == "true" {
		CommandEnabled = true
	}
}

func SetWorkspace(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	WorkspacePath = filepath.Clean(abs)
}

func resolvePath(rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(WorkspacePath, rel))
}

func isUnderWorkspace(abs string) bool {
	return strings.HasPrefix(abs, WorkspacePath+string(filepath.Separator)) || abs == WorkspacePath
}

func isWriteAllowed(absPath string) error {
	if !WriteEnabled {
		return fmt.Errorf("文件写入功能已禁用（设置环境变量 TOOL_WRITE_ENABLED=true 开启）")
	}
	rel, _ := filepath.Rel(WorkspacePath, absPath)
	for _, deny := range WriteDenyPaths {
		if strings.HasSuffix(deny, "/") {
			// 目录前缀匹配（如 ".git/"）
			if strings.HasPrefix(rel, deny) {
				return fmt.Errorf("不允许修改受保护路径: %s", deny)
			}
		} else {
			// 精确文件名匹配（如 "go.sum"），不会误匹配 go.summary
			if rel == deny || strings.HasPrefix(rel, deny+"/") {
				return fmt.Errorf("不允许修改受保护路径: %s", deny)
			}
		}
	}
	return nil
}
