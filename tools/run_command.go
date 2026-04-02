package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var commandBlacklist = []string{
	"rm -rf /", "rm -rf /*", "mkfs", "dd if=",
	"chmod -R 777 /", ":(){ :|:& };:",
	"> /dev/sda", "curl | sh", "wget | sh",
	"curl|sh", "wget|sh",
}

const maxCommandOutput = 8000
const largeOutputDir = "/tmp/chat_server_outputs"

// 命令语义映射：某些命令的非零退出码并非错误
// key = 命令名, value = 错误阈值（退出码 < threshold 时属于正常语义）
var commandSemantics = map[string]struct {
	threshold   int
	explanation string
}{
	"grep":  {2, "退出码 1 = 未找到匹配，非错误"},
	"rg":    {2, "退出码 1 = 未找到匹配，非错误"},
	"diff":  {2, "退出码 1 = 文件有差异，非错误"},
	"test":  {2, "退出码 1 = 条件为假，非错误"},
	"cmp":   {2, "退出码 1 = 文件不同，非错误"},
	"false": {2, "退出码 1 = 预期行为"},
}

func init() {
	Register(&Tool{
		Name:           "run_command",
		MaxResultChars: maxCommandOutput,
		Description: `执行 shell 命令。
适合：编译项目、运行测试、git 操作、安装依赖、查看进程等。
注意：
- 命令在项目根目录下执行
- 默认超时 30 秒，最大 120 秒
- 危险命令（如 rm -rf /）会被拦截
- 需要管理员启用（环境变量 TOOL_COMMAND_ENABLED=true）
- 读文件用 read_file 而非 cat，搜索用 grep_search 而非 grep，改文件用 edit_file 而非 sed`,
		Parameters: json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "要执行的 shell 命令"
		},
		"timeout": {
			"type": "integer",
			"description": "超时秒数（默认 30，最大 120）"
		}
	},
	"required": ["command"]
}`),
		Execute: func(args json.RawMessage, _ *ReadCache) (string, error) {
			return executeRunCommand(args)
		},
	})
}

func executeRunCommand(args json.RawMessage) (string, error) {
	var input struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if input.Command == "" {
		return "", fmt.Errorf("command 参数不能为空")
	}

	if !CommandEnabled {
		return "", fmt.Errorf("命令执行功能未启用。请设置环境变量 TOOL_COMMAND_ENABLED=true 后重启服务")
	}

	cmdLower := strings.ToLower(strings.TrimSpace(input.Command))
	for _, banned := range commandBlacklist {
		if strings.Contains(cmdLower, banned) {
			return "", fmt.Errorf("危险命令已拦截: 不允许执行包含 %q 的命令", banned)
		}
	}

	timeout := input.Timeout
	if timeout <= 0 {
		timeout = CommandTimeout
	}
	if timeout > MaxCommandTimeout {
		timeout = MaxCommandTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)
	cmd.Dir = WorkspacePath

	out, err := cmd.CombinedOutput()
	output := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("命令超时（%d 秒）。已获取输出:\n%s", timeout, truncateCommandOutput(output)), nil
	}

	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	return formatCommandResult(input.Command, exitCode, output), nil
}

// formatCommandResult 根据命令语义映射格式化结果
func formatCommandResult(command string, exitCode int, output string) string {
	result := &strings.Builder{}

	if exitCode != 0 {
		cmdName := extractCommandName(command)
		if sem, ok := commandSemantics[cmdName]; ok && exitCode > 0 && exitCode < sem.threshold {
			fmt.Fprintf(result, "退出码: %d（正常语义: %s）\n\n", exitCode, sem.explanation)
		} else {
			fmt.Fprintf(result, "退出码: %d\n\n", exitCode)
		}
	} else {
		result.WriteString("退出码: 0\n\n")
	}

	// 大输出持久化
	outputRunes := []rune(output)
	if len(outputRunes) > maxCommandOutput {
		savedPath := persistLargeOutput(output)
		truncated := truncateCommandOutput(output)
		result.WriteString(truncated)
		if savedPath != "" {
			fmt.Fprintf(result, "\n\n[完整输出（%d 字符）已保存到 %s]", len(outputRunes), savedPath)
		}
	} else {
		result.WriteString(output)
	}

	return result.String()
}

// extractCommandName 从完整命令行中提取命令名（如 "grep -r foo" → "grep"）
func extractCommandName(command string) string {
	trimmed := strings.TrimSpace(command)
	// 跳过环境变量赋值（如 FOO=bar cmd）
	for strings.Contains(strings.SplitN(trimmed, " ", 2)[0], "=") {
		parts := strings.SplitN(trimmed, " ", 2)
		if len(parts) < 2 {
			break
		}
		trimmed = strings.TrimSpace(parts[1])
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// persistLargeOutput 将大输出保存到文件，返回保存路径
func persistLargeOutput(output string) string {
	if err := os.MkdirAll(largeOutputDir, 0755); err != nil {
		return ""
	}
	name := "cmd_" + time.Now().Format("20060102_150405") + ".txt"
	path := filepath.Join(largeOutputDir, name)
	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		return ""
	}
	return path
}

func truncateCommandOutput(output string) string {
	runes := []rune(output)
	if len(runes) <= maxCommandOutput {
		return output
	}
	half := maxCommandOutput / 2
	return string(runes[:half]) +
		fmt.Sprintf("\n\n... 省略 %d 字符 ...\n\n", len(runes)-maxCommandOutput) +
		string(runes[len(runes)-half:])
}
