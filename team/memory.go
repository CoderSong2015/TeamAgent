package team

import (
	"chat_server/agent"
	"chat_server/llm"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	TeamMemoryFile = "data/team_memory.json"
	WorkerMemDir   = "data/worker_memory"

	maxTeamMemoryEntries    = 30
	maxWorkerMemoryEntries  = 15
	maxMemoryPromptChars    = 1500
	maxWorkerMemPromptChars = 800
	maxWorkerExpPromptChars = 500
	memoryExpireDays        = 30
)

var teamMemMu sync.Mutex

// ── Public Memory Pool (P0) ──

func LoadTeamMemory() []agent.MemoryEntry {
	data, err := os.ReadFile(TeamMemoryFile)
	if err != nil {
		return []agent.MemoryEntry{}
	}
	var entries []agent.MemoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return []agent.MemoryEntry{}
	}
	return entries
}

func SaveTeamMemory(entries []agent.MemoryEntry) {
	os.MkdirAll(filepath.Dir(TeamMemoryFile), 0755)
	data, _ := json.MarshalIndent(entries, "", "  ")
	os.WriteFile(TeamMemoryFile, data, 0644)
}

func addTeamMemory(newEntries []agent.MemoryEntry) {
	teamMemMu.Lock()
	defer teamMemMu.Unlock()

	existing := LoadTeamMemory()

	for _, ne := range newEntries {
		dup := false
		for j, ex := range existing {
			if ex.Name == ne.Name && ex.Type == ne.Type {
				existing[j] = ne
				dup = true
				break
			}
		}
		if !dup {
			existing = append(existing, ne)
		}
	}

	existing = evictOldEntries(existing, maxTeamMemoryEntries)
	SaveTeamMemory(existing)
}

func evictOldEntries(entries []agent.MemoryEntry, maxCount int) []agent.MemoryEntry {
	now := time.Now()
	var kept []agent.MemoryEntry
	for _, e := range entries {
		t, err := time.Parse(time.RFC3339, e.CreatedAt)
		if err != nil || now.Sub(t).Hours()/24 < float64(memoryExpireDays) {
			kept = append(kept, e)
		}
	}
	if len(kept) > maxCount {
		kept = kept[len(kept)-maxCount:]
	}
	return kept
}

// ── Memory Prompt Formatting (P0) ──

func TeamMemoryPromptForLeader() string {
	entries := LoadTeamMemory()
	agentMem := loadAgentProjectMemories()
	entries = mergeMemories(entries, agentMem)

	if len(entries) == 0 {
		return ""
	}

	typeLabels := map[string]string{
		"user": "用户偏好", "feedback": "行为反馈",
		"project": "项目上下文", "reference": "外部引用",
	}
	typeOrder := []string{"project", "user", "reference", "feedback"}

	grouped := map[string][]agent.MemoryEntry{}
	for _, e := range entries {
		grouped[e.Type] = append(grouped[e.Type], e)
	}

	var sb strings.Builder
	for _, t := range typeOrder {
		items, ok := grouped[t]
		if !ok || len(items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n", typeLabels[t]))
		for _, e := range items {
			age := memoryAgeTag(e.CreatedAt)
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", age, e.Content))
		}
	}

	result := sb.String()
	if len([]rune(result)) > maxMemoryPromptChars {
		result = string([]rune(result)[:maxMemoryPromptChars]) + "\n...(已截断)"
	}
	return result
}

func TeamMemoryPromptForWorker() string {
	entries := LoadTeamMemory()
	agentMem := loadAgentProjectMemories()
	entries = mergeMemories(entries, agentMem)

	var filtered []agent.MemoryEntry
	for _, e := range entries {
		if e.Type == "project" || e.Type == "reference" {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, e := range filtered {
		sb.WriteString(fmt.Sprintf("- %s\n", e.Content))
	}

	result := sb.String()
	if len([]rune(result)) > maxWorkerMemPromptChars {
		result = string([]rune(result)[:maxWorkerMemPromptChars]) + "\n...(已截断)"
	}
	return result
}

func memoryAgeTag(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return "未知"
	}
	days := int(time.Since(t).Hours() / 24)
	switch {
	case days == 0:
		return "今天"
	case days == 1:
		return "昨天"
	case days < 7:
		return fmt.Sprintf("%d天前", days)
	default:
		return fmt.Sprintf("%d天前⚠️", days)
	}
}

// ── P1: Agent Memory Bridge ──

func loadAgentProjectMemories() []agent.MemoryEntry {
	agentDir := agent.Dir
	dirs, err := os.ReadDir(agentDir)
	if err != nil {
		return nil
	}

	var results []agent.MemoryEntry
	seen := map[string]bool{}

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		memFile := filepath.Join(agentDir, d.Name(), "memory.json")
		data, err := os.ReadFile(memFile)
		if err != nil {
			continue
		}
		var entries []agent.MemoryEntry
		if json.Unmarshal(data, &entries) != nil {
			continue
		}
		for _, e := range entries {
			if e.Type == "project" || e.Type == "reference" {
				key := e.Type + "::" + e.Name
				if !seen[key] {
					seen[key] = true
					results = append(results, e)
				}
			}
		}
	}
	return results
}

func mergeMemories(base, extra []agent.MemoryEntry) []agent.MemoryEntry {
	seen := map[string]bool{}
	for _, e := range base {
		seen[e.Type+"::"+e.Name] = true
	}
	for _, e := range extra {
		key := e.Type + "::" + e.Name
		if !seen[key] {
			seen[key] = true
			base = append(base, e)
		}
	}
	return base
}

// ── P2: Worker Role Memory ──

func LoadWorkerMemory(role string) []agent.MemoryEntry {
	data, err := os.ReadFile(filepath.Join(WorkerMemDir, role+".json"))
	if err != nil {
		return []agent.MemoryEntry{}
	}
	var entries []agent.MemoryEntry
	if json.Unmarshal(data, &entries) != nil {
		return []agent.MemoryEntry{}
	}
	return entries
}

func saveWorkerMemory(role string, entries []agent.MemoryEntry) {
	os.MkdirAll(WorkerMemDir, 0755)
	data, _ := json.MarshalIndent(entries, "", "  ")
	os.WriteFile(filepath.Join(WorkerMemDir, role+".json"), data, 0644)
}

func addWorkerMemory(role string, newEntries []agent.MemoryEntry) {
	teamMemMu.Lock()
	defer teamMemMu.Unlock()

	existing := LoadWorkerMemory(role)
	for _, ne := range newEntries {
		dup := false
		for j, ex := range existing {
			if ex.Name == ne.Name {
				existing[j] = ne
				dup = true
				break
			}
		}
		if !dup {
			existing = append(existing, ne)
		}
	}
	existing = evictOldEntries(existing, maxWorkerMemoryEntries)
	saveWorkerMemory(role, existing)
}

func WorkerMemoryPrompt(role string) string {
	entries := LoadWorkerMemory(role)
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("- %s\n", e.Content))
	}

	result := sb.String()
	if len([]rune(result)) > maxWorkerExpPromptChars {
		result = string([]rune(result)[:maxWorkerExpPromptChars]) + "\n...(已截断)"
	}
	return result
}

func ExtractWorkerMemories(teamID int, results []workerResult) {
	for _, r := range results {
		if r.Error != "" || r.Result == "" || r.Worker == "reviewer" {
			continue
		}
		go extractSingleWorkerMemory(teamID, r.Worker, r.Result)
	}
}

// ── HTTP API ──

func HandleTeamMemory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		entries := LoadTeamMemory()
		workerMem := map[string][]agent.MemoryEntry{}
		for _, role := range []string{"researcher", "coder", "reviewer"} {
			if mem := LoadWorkerMemory(role); len(mem) > 0 {
				workerMem[role] = mem
			}
		}
		llm.WriteJSON(w, 200, map[string]interface{}{
			"team_memory":   entries,
			"worker_memory": workerMem,
		})
	case http.MethodDelete:
		SaveTeamMemory([]agent.MemoryEntry{})
		for _, role := range []string{"researcher", "coder", "reviewer"} {
			saveWorkerMemory(role, []agent.MemoryEntry{})
		}
		llm.WriteJSON(w, 200, map[string]string{"status": "cleared"})
	default:
		http.Error(w, "不支持的方法", 405)
	}
}

func extractSingleWorkerMemory(teamID int, role, result string) {
	prompt := fmt.Sprintf(`分析以下 %s 角色的工作输出，提取该角色值得长期记住的经验。

## 提取重点

- 有效的工作方法和策略
- 发现的项目特定知识（文件位置、模块关系等）
- 踩过的坑和解决方案
- 代码风格和架构偏好

## 排除规则

- 一次性的具体任务内容
- 已经在项目级记忆中记录的信息
- 推测性内容

## 输出格式

严格输出一个 JSON 数组，每个元素包含 name 和 content 字段。
如果没有值得记住的信息，输出空数组 []。不要包含任何其他文字。

## 工作输出

""" %s """`, role, result)

	msgs := []llm.Message{{Role: "user", Content: prompt}}
	raw, err := llm.Call(msgs, "gpt-5.4")
	if err != nil {
		log.Printf("Team #%d Worker[%s] 经验提取失败: %v", teamID, role, err)
		return
	}

	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "["); start >= 0 {
		if end := strings.LastIndex(raw, "]"); end >= start {
			raw = raw[start : end+1]
		}
	}

	var rawEntries []struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &rawEntries); err != nil {
		return
	}

	now := time.Now().Format(time.RFC3339)
	var entries []agent.MemoryEntry
	for _, re := range rawEntries {
		if re.Name == "" || re.Content == "" {
			continue
		}
		entries = append(entries, agent.MemoryEntry{
			Type:      role,
			Name:      re.Name,
			Content:   re.Content,
			CreatedAt: now,
		})
	}

	if len(entries) > 0 {
		addWorkerMemory(role, entries)
		log.Printf("Team #%d Worker[%s] 提取了 %d 条经验", teamID, role, len(entries))
	}
}
