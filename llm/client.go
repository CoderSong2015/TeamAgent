package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	ApiURL = getEnvDefault("LLM_API_URL", "")
	ApiKey = getEnvDefault("LLM_API_KEY", "")
)

func init() {
	if ApiURL == "" || ApiKey == "" {
		fmt.Fprintln(os.Stderr, "[警告] LLM_API_URL 或 LLM_API_KEY 环境变量未设置，LLM 功能将不可用")
		fmt.Fprintln(os.Stderr, "  设置方式: export LLM_API_URL=https://your-llm-api/v1/chat/completions")
		fmt.Fprintln(os.Stderr, "  设置方式: export LLM_API_KEY=your-api-key")
	}
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── Tool Use types ──

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ── Message ──

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ChatRequest struct {
	Message string    `json:"message"`
	Model   string    `json:"model"`
	History []Message `json:"history"`
}

type ChatResponse struct {
	Reply string `json:"reply"`
	Model string `json:"model"`
	Error string `json:"error,omitempty"`
}

type request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func Call(messages []Message, model string) (string, error) {
	payload := request{Model: model, Messages: messages}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化失败: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, ApiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ApiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(respBody))
	}

	var llmResp response
	if err := json.Unmarshal(respBody, &llmResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	if len(llmResp.Choices) == 0 {
		return "", fmt.Errorf("API 返回空结果")
	}

	return llmResp.Choices[0].Message.Content, nil
}

func isRetryableStatus(errMsg string) bool {
	for _, code := range []string{"429", "500", "502", "503"} {
		if strings.Contains(errMsg, "API 返回 "+code) {
			return true
		}
	}
	return strings.Contains(errMsg, "请求失败:") || strings.Contains(errMsg, "读取响应失败:")
}

func CallWithRetry(messages []Message, model string, maxRetries int) (string, error) {
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		reply, err := Call(messages, model)
		if err == nil {
			return reply, nil
		}
		lastErr = err
		if !isRetryableStatus(err.Error()) {
			return "", err
		}
		if i < maxRetries {
			backoff := time.Duration(1<<uint(i)) * time.Second
			time.Sleep(backoff)
		}
	}
	return "", fmt.Errorf("重试 %d 次后仍然失败: %w", maxRetries, lastErr)
}

// ── Tool-aware call ──

func CallWithTools(messages []Message, tools []ToolDef, model string) (*Message, error) {
	type req struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
		Tools    []ToolDef `json:"tools,omitempty"`
	}
	type resp struct {
		Choices []struct {
			Message struct {
				Role      string     `json:"role"`
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	payload := req{Model: model, Messages: messages, Tools: tools}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化失败: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, ApiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ApiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回 %d: %s", httpResp.StatusCode, string(respBody))
	}

	var llmResp resp
	if err := json.Unmarshal(respBody, &llmResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if len(llmResp.Choices) == 0 {
		return nil, fmt.Errorf("API 返回空结果")
	}

	choice := llmResp.Choices[0]
	return &Message{
		Role:      choice.Message.Role,
		Content:   choice.Message.Content,
		ToolCalls: choice.Message.ToolCalls,
	}, nil
}

func CallWithToolsRetry(messages []Message, tools []ToolDef, model string, maxRetries int) (*Message, error) {
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		msg, err := CallWithTools(messages, tools, model)
		if err == nil {
			return msg, nil
		}
		lastErr = err
		if !isRetryableStatus(err.Error()) {
			return nil, err
		}
		if i < maxRetries {
			backoff := time.Duration(1<<uint(i)) * time.Second
			time.Sleep(backoff)
		}
	}
	return nil, fmt.Errorf("重试 %d 次后仍然失败: %w", maxRetries, lastErr)
}

func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
