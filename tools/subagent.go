package tools

import (
	"chat_server/llm"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	ExploreMaxRounds   = 3
	ExploreTimeout     = 60 * time.Second
	ExploreMaxResult   = 800
	DelegateMaxRounds  = 3
	DelegateTimeout    = 120 * time.Second
	DelegateMaxResult  = 1200
	MaxParallelAgents  = 3
	MaxSubAgentsPerRun = 10
)

type SubAgentResult struct {
	Content string
	Error   string
}

func RunExplore(task, scope, model string, session *ReadCache) SubAgentResult {
	if scope == "" {
		scope = WorkspacePath
	}

	sysPrompt := fmt.Sprintf(`你是一个只读文件搜索专家。

=== 严格只读模式 ===
你只能使用 list_files、read_file、grep_search、glob_search 工具。

工作原则：
- 快速、精炼：尽快完成搜索并返回核心发现
- 大文件只读前 50 行或关键段落（用 offset+limit）
- 不要原样复制文件内容，用自己的话总结关键发现
- 返回结构化的简要结论

搜索范围：%s`, scope)

	return runSubAgent(sysPrompt, task, GetReadOnlyToolDefs(), ExploreMaxRounds, ExploreTimeout, ExploreMaxResult, model, session)
}

func RunDelegate(task, model string, session *ReadCache) SubAgentResult {
	sysPrompt := `你是一个任务执行专家。

给定任务后，使用可用工具高效完成。完成后用精炼的文字报告：
1. 做了什么
2. 关键发现
3. 如有问题，说明原因

原则：
- 不要过度打磨，完成核心任务即可
- 大文件只读关键部分（用 offset+limit）
- 用自己的话总结，不要复制原文`

	return runSubAgent(sysPrompt, task, GetSubAgentAllowedToolDefs(), DelegateMaxRounds, DelegateTimeout, DelegateMaxResult, model, session)
}

// session 用于降级计数（属于父 Worker 的 per-session 状态），子 Agent 内部使用独立的 ReadCache
func runSubAgent(sysPrompt, task string, toolDefs []llm.ToolDef, maxRounds int, timeout time.Duration, maxResult int, model string, session *ReadCache) SubAgentResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	msgs := []llm.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: task},
	}

	cache := NewReadCache()

	for round := 0; round < maxRounds; round++ {
		if round >= 1 {
			CleanOldToolResults(msgs)
		}

		select {
		case <-ctx.Done():
			return SubAgentResult{Error: "子 Agent 超时"}
		default:
		}

		msg, err := llm.CallWithToolsRetry(msgs, toolDefs, model, 1)
		if err != nil {
			if session != nil {
				session.IncrSubAgentFail()
			}
			return SubAgentResult{Error: fmt.Sprintf("子 Agent LLM 调用失败: %v", err)}
		}

		if len(msg.ToolCalls) == 0 {
			if session != nil {
				session.ResetSubAgentFail()
			}
			return SubAgentResult{Content: truncateRunes(msg.Content, maxResult)}
		}

		msgs = append(msgs, *msg)

		for _, tc := range msg.ToolCalls {
			result, execErr := Execute(tc.Function.Name, json.RawMessage(tc.Function.Arguments), cache)
			content := result
			if execErr != nil {
				content = fmt.Sprintf("工具执行错误: %v", execErr)
			}
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    content,
			})
		}
	}

	msgs = append(msgs, llm.Message{
		Role:    "user",
		Content: "你已达到工具调用上限。请立即用精炼文字总结你找到的所有信息，不要再调用工具。",
	})

	finalMsg, err := llm.CallWithToolsRetry(msgs, nil, model, 1)
	if err != nil {
		if session != nil {
			session.IncrSubAgentFail()
		}
		return SubAgentResult{Error: "子 Agent 总结生成失败"}
	}

	if session != nil {
		session.ResetSubAgentFail()
	}
	return SubAgentResult{Content: truncateRunes(finalMsg.Content, maxResult)}
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "\n...(已截断)"
}

type exploreArgs struct {
	Task  string `json:"task"`
	Scope string `json:"scope"`
}

type delegateArgs struct {
	Task string `json:"task"`
}

func RegisterSubAgentTools() {
	Register(&Tool{
		Name: "explore",
		Description: `启动一个轻量级只读子 Agent 来搜索和探索代码库。
子 Agent 有独立上下文（不会膨胀你的上下文），会使用 list_files、read_file、grep_search、glob_search 进行探索，返回精炼摘要。
适合：了解目录结构、查找函数定义、读取配置文件摘要等。
你可以在一次回复中调用多个 explore 来并行探索不同问题。
注意：如果只需 1 次简单搜索（如 grep 一个关键字），直接用工具即可，不必启动 explore。`,
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {
					"type": "string",
					"description": "要探索的具体问题。像对同事下达搜索指令：找什么、在哪找、返回什么。"
				},
				"scope": {
					"type": "string",
					"description": "搜索范围目录路径，默认项目根目录。"
				}
			},
			"required": ["task"]
		}`),
		Execute: func(args json.RawMessage, cache *ReadCache) (string, error) {
			var a exploreArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %v", err)
			}
			if cache != nil && cache.ShouldDegradeSubAgent() {
				return "", fmt.Errorf("子 Agent 连续失败，已降级。请直接使用 read_file/list_files/grep_search 工具")
			}
			model := cache.GetModel()
			result := RunExplore(a.Task, a.Scope, model, cache)
			if result.Error != "" {
				return result.Error, fmt.Errorf("explore 失败: %s", result.Error)
			}
			return SubAgentResultPrefix + result.Content, nil
		},
		IsSubAgentTool: true,
	})

	Register(&Tool{
		Name: "delegate_task",
		Description: `启动一个只读子 Agent 执行多步子任务（比 explore 有更长的超时和更大的上下文）。
子 Agent 有独立上下文，使用与 explore 相同的只读工具集（list_files、read_file、grep_search、glob_search）。
适合：需要多步搜索和分析的复杂问题，当 explore 的轮次不够用时使用。`,
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {
					"type": "string",
					"description": "要执行的具体任务，提供完整背景和期望输出格式。"
				}
			},
			"required": ["task"]
		}`),
		Execute: func(args json.RawMessage, cache *ReadCache) (string, error) {
			var a delegateArgs
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %v", err)
			}
			if cache != nil && cache.ShouldDegradeSubAgent() {
				return "", fmt.Errorf("子 Agent 连续失败，已降级。请直接使用工具完成任务")
			}
			model := cache.GetModel()
			result := RunDelegate(a.Task, model, cache)
			if result.Error != "" {
				return result.Error, fmt.Errorf("delegate_task 失败: %s", result.Error)
			}
			return SubAgentResultPrefix + result.Content, nil
		},
		IsSubAgentTool: true,
	})

	log.Printf("子 Agent 工具注册完成（explore + delegate_task）")
}

type ParallelSubAgentTask struct {
	ToolCallID string
	Name       string
	Args       json.RawMessage
}

type ParallelSubAgentResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

func ExecuteSubAgentsParallel(tasks []ParallelSubAgentTask, cache *ReadCache) []ParallelSubAgentResult {
	results := make([]ParallelSubAgentResult, len(tasks))
	var wg sync.WaitGroup

	sem := make(chan struct{}, MaxParallelAgents)

	for i, task := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, t ParallelSubAgentTask) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := Execute(t.Name, t.Args, cache)
			if err != nil {
				results[idx] = ParallelSubAgentResult{
					ToolCallID: t.ToolCallID,
					Content:    fmt.Sprintf("工具执行错误: %v", err),
					IsError:    true,
				}
			} else {
				results[idx] = ParallelSubAgentResult{
					ToolCallID: t.ToolCallID,
					Content:    result,
				}
			}
		}(i, task)
	}
	wg.Wait()
	return results
}
