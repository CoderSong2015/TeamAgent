package tools

import (
	"chat_server/llm"
	"strings"
)

const KeepRecentToolResults = 3

const SubAgentResultPrefix = "[SubAgent] "

func CleanOldToolResults(messages []llm.Message) {
	toolCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			if strings.HasPrefix(messages[i].Content, SubAgentResultPrefix) {
				continue
			}
			toolCount++
			if toolCount > KeepRecentToolResults {
				messages[i].Content = "[旧工具结果已清理，请参考后续的工具调用结果]"
			}
		}
	}
}
