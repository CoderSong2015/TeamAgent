package tools

import (
	"chat_server/llm"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

type Tool struct {
	Name            string
	Description     string
	Parameters      json.RawMessage
	Execute         func(args json.RawMessage, cache *ReadCache) (string, error)
	SubAgentAllowed bool
	IsSubAgentTool  bool
	IsReadOnly      bool
	MaxResultChars  int // >0: 截断到此长度; 0: 使用 DefaultMaxResultChars
}

var registry = map[string]*Tool{}

func Register(t *Tool) {
	registry[t.Name] = t
	log.Printf("工具注册: %s", t.Name)
}

const DefaultMaxResultChars = 1500

func Execute(name string, args json.RawMessage, cache *ReadCache) (string, error) {
	t, ok := registry[name]
	if !ok {
		return "", fmt.Errorf("未知工具: %s（可用工具: %s）", name, availableNames())
	}
	result, err := t.Execute(args, cache)
	if err != nil {
		return result, err
	}
	limit := t.MaxResultChars
	if limit <= 0 {
		limit = DefaultMaxResultChars
	}
	runes := []rune(result)
	if len(runes) > limit {
		result = string(runes[:limit]) +
			fmt.Sprintf("\n...(结果已截断，共 %d 字符，仅显示前 %d。请缩小查询范围)", len(runes), limit)
	}
	return result, nil
}

func IsSubAgentTool(name string) bool {
	t, ok := registry[name]
	return ok && t.IsSubAgentTool
}

func IsReadOnlyTool(name string) bool {
	t, ok := registry[name]
	return ok && t.IsReadOnly
}

func GetToolDefs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(registry))
	for _, t := range registry {
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return defs
}

func GetToolDefsNoSubAgent() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(registry))
	for _, t := range registry {
		if t.IsSubAgentTool {
			continue
		}
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return defs
}

func GetSubAgentAllowedToolDefs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(registry))
	for _, t := range registry {
		if t.IsSubAgentTool || !t.SubAgentAllowed {
			continue
		}
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return defs
}

func GetReadOnlyToolDefs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0)
	for _, t := range registry {
		if t.IsReadOnly && !t.IsSubAgentTool {
			defs = append(defs, llm.ToolDef{
				Type: "function",
				Function: llm.FunctionDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
	}
	return defs
}

// ── Partitioned Execution ──

type ToolCallResult struct {
	ToolCallID string
	Name       string
	Content    string
	IsError    bool
}

// ExecutePartitioned 按读写属性分流执行：连续只读工具并行，写工具串行。
func ExecutePartitioned(calls []llm.ToolCall, cache *ReadCache) []ToolCallResult {
	results := make([]ToolCallResult, len(calls))
	i := 0
	for i < len(calls) {
		if IsReadOnlyTool(calls[i].Function.Name) {
			j := i
			for j < len(calls) && IsReadOnlyTool(calls[j].Function.Name) {
				j++
			}
			batch := calls[i:j]
			if len(batch) == 1 {
				results[i] = executeSingle(batch[0], cache)
			} else {
				var wg sync.WaitGroup
				for k := range batch {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						results[i+idx] = executeSingle(batch[idx], cache)
					}(k)
				}
				wg.Wait()
			}
			i = j
		} else {
			results[i] = executeSingle(calls[i], cache)
			i++
		}
	}
	return results
}

func executeSingle(tc llm.ToolCall, cache *ReadCache) ToolCallResult {
	result, err := Execute(tc.Function.Name, json.RawMessage(tc.Function.Arguments), cache)
	if err != nil {
		return ToolCallResult{
			ToolCallID: tc.ID,
			Name:       tc.Function.Name,
			Content:    fmt.Sprintf("工具执行错误: %v", err),
			IsError:    true,
		}
	}
	return ToolCallResult{
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
		Content:    result,
	}
}

func availableNames() string {
	names := ""
	for name := range registry {
		if names != "" {
			names += ", "
		}
		names += name
	}
	return names
}
