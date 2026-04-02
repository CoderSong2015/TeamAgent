package agent

import (
	"chat_server/llm"
	"chat_server/tools"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Dir           = "data/agents"
	MetaFile      = "data/meta.json"
	MaxHistory    = 20
	MaxToolRounds = 10
)

// ── Data Models ──

type Meta struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
}

type Store struct {
	mu     sync.RWMutex
	NextID int    `json:"next_id"`
	Agents []Meta `json:"agents"`
}

type MemoryEntry struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	CreatedAt   string `json:"created_at"`
}

var S = &Store{}

func Init() {
	os.MkdirAll(Dir, 0755)
	S.load()
}

// ── Store persistence ──

func (s *Store) load() {
	data, err := os.ReadFile(MetaFile)
	if err != nil {
		s.NextID = 1
		s.Agents = []Meta{}
		return
	}
	if err := json.Unmarshal(data, s); err != nil {
		s.NextID = 1
		s.Agents = []Meta{}
	}
}

func (s *Store) save() {
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(MetaFile, data, 0644)
}

func (s *Store) Create(model string) Meta {
	s.mu.Lock()
	defer s.mu.Unlock()

	a := Meta{
		ID:        s.NextID,
		Title:     "新对话",
		Model:     model,
		CreatedAt: time.Now().Format("2006-01-02 15:04"),
	}
	s.NextID++
	s.Agents = append(s.Agents, a)
	s.save()

	d := agentDir(a.ID)
	os.MkdirAll(d, 0755)
	os.WriteFile(filepath.Join(d, "memory.json"), []byte("[]"), 0644)
	os.WriteFile(filepath.Join(d, "history.json"), []byte("[]"), 0644)

	log.Printf("Agent #%d 创建完成 → %s", a.ID, d)
	return a
}

func (s *Store) Delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, a := range s.Agents {
		if a.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return false
	}
	s.Agents = append(s.Agents[:idx], s.Agents[idx+1:]...)
	s.save()
	os.RemoveAll(agentDir(id))
	log.Printf("Agent #%d 已删除", id)
	return true
}

func (s *Store) UpdateTitle(id int, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.Agents {
		if a.ID == id {
			s.Agents[i].Title = title
			s.save()
			return
		}
	}
}

func (s *Store) UpdateModel(id int, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.Agents {
		if a.ID == id {
			s.Agents[i].Model = model
			s.save()
			return
		}
	}
}

func (s *Store) Get(id int) *Meta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.Agents {
		if a.ID == id {
			cp := a
			return &cp
		}
	}
	return nil
}

func (s *Store) List() []Meta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Meta, len(s.Agents))
	copy(out, s.Agents)
	return out
}

func agentDir(id int) string {
	return filepath.Join(Dir, strconv.Itoa(id))
}

// ── Memory ──

func LoadMemory(id int) []MemoryEntry {
	data, err := os.ReadFile(filepath.Join(agentDir(id), "memory.json"))
	if err != nil {
		return []MemoryEntry{}
	}
	var entries []MemoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		entries = nil
		var old []string
		if json.Unmarshal(data, &old) == nil {
			for _, s := range old {
				entries = append(entries, MemoryEntry{
					Type: "user", Name: s, Description: s, Content: s,
					CreatedAt: time.Now().Format(time.RFC3339),
				})
			}
			SaveMemory(id, entries)
		}
	}
	return entries
}

func SaveMemory(id int, entries []MemoryEntry) {
	data, _ := json.MarshalIndent(entries, "", "  ")
	os.WriteFile(filepath.Join(agentDir(id), "memory.json"), data, 0644)
}

func addMemory(id int, newEntries []MemoryEntry) {
	existing := LoadMemory(id)
	for _, ne := range newEntries {
		dup := false
		for _, ex := range existing {
			if ex.Name == ne.Name && ex.Type == ne.Type {
				dup = true
				break
			}
		}
		if !dup {
			existing = append(existing, ne)
		}
	}
	SaveMemory(id, existing)
}

func SystemPrompt(id int) string {
	entries := LoadMemory(id)
	if len(entries) == 0 {
		return ""
	}

	typeLabels := map[string]string{
		"user": "用户画像", "feedback": "行为反馈",
		"project": "项目上下文", "reference": "外部引用",
	}
	typeOrder := []string{"user", "feedback", "project", "reference"}
	grouped := map[string][]MemoryEntry{}
	for _, e := range entries {
		grouped[e.Type] = append(grouped[e.Type], e)
	}

	var sb strings.Builder
	sb.WriteString("以下是你的永久记忆，按类型分组。请在回答时自然地考虑这些信息。\n")
	sb.WriteString("注意：超过 2 天的记忆可能已过时，使用前请验证。\n\n")

	for _, t := range typeOrder {
		items, ok := grouped[t]
		if !ok || len(items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %s（%s）\n", typeLabels[t], t))
		for _, e := range items {
			days := memoryAgeDays(e.CreatedAt)
			age := memoryAgeLabel(days)
			sb.WriteString(fmt.Sprintf("- [%s] %s", age, e.Content))
			if days >= 2 {
				sb.WriteString(" ⚠️ 可能已过时")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("记忆使用规则：\n")
	sb.WriteString("- 记忆说\"X存在\"不等于\"X现在存在\"，请先验证\n")
	sb.WriteString("- 用户说\"忽略某条记忆\"时，当它不存在，不要提及\n")
	sb.WriteString("- feedback 类记忆中的 Why 帮助你判断边界情况，而非盲从规则\n")
	return sb.String()
}

func memoryAgeDays(createdAt string) int {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return 0
	}
	d := int(time.Since(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

func memoryAgeLabel(days int) string {
	if days == 0 {
		return "今天"
	}
	if days == 1 {
		return "昨天"
	}
	return fmt.Sprintf("%d天前", days)
}

func ExtractMemory(agentID int, userMsg, assistantReply string) {
	prompt := `分析以下对话，提取值得长期记住的信息。

## 四种记忆类型

1. **user** — 用户画像：姓名、角色、位置、偏好、技术栈、工作习惯
2. **feedback** — 行为反馈：用户对你的回答的纠正、喜好、约束（如"不要使用X"、"我更喜欢Y"），尤其关注 Why
3. **project** — 项目上下文：当前项目的架构、技术选型、文件结构、依赖、部署方式
4. **reference** — 外部引用：用户提供的文档链接、API 文档、参考资料、命名规范

## 排除规则

- 一次性问题/答案
- 临时代码片段
- 当前对话中已隐含的上下文
- 可以通过读取文件轻松获取的信息
- 推测性内容

## 输出格式

严格输出一个JSON数组，每个元素包含 type、name、description、content 字段。
如果没有值得记住的信息，输出空数组 []。不要包含任何其他文字。

示例：
[
  {"type":"user","name":"用户姓名","description":"用户自我介绍了名字","content":"用户叫小明"},
  {"type":"feedback","name":"代码风格偏好","description":"用户明确表示不喜欢某种写法","content":"用户不喜欢使用var声明变量，偏好const/let"},
  {"type":"project","name":"后端技术栈","description":"项目使用的后端框架","content":"后端使用Go + net/http，端口8088"}
]

## 对话内容

用户说：""" ` + userMsg + ` """
助手回复：""" ` + assistantReply + ` """`

	msgs := []llm.Message{{Role: "user", Content: prompt}}
	raw, err := llm.Call(msgs, "gpt-5.4")
	if err != nil {
		log.Printf("Agent #%d 记忆提取失败: %v", agentID, err)
		return
	}

	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "["); start >= 0 {
		if end := strings.LastIndex(raw, "]"); end >= start {
			raw = raw[start : end+1]
		}
	}

	var entries []MemoryEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		limit := len(raw)
		if limit > 200 {
			limit = 200
		}
		log.Printf("Agent #%d 记忆 JSON 解析失败: %v, raw: %s", agentID, err, raw[:limit])
		return
	}

	now := time.Now().Format(time.RFC3339)
	for i := range entries {
		entries[i].CreatedAt = now
		if entries[i].Type == "" {
			entries[i].Type = "user"
		}
	}

	if len(entries) > 0 {
		addMemory(agentID, entries)
		counts := map[string]int{}
		for _, e := range entries {
			counts[e.Type]++
		}
		var parts []string
		for _, t := range []string{"user", "feedback", "project", "reference"} {
			if c, ok := counts[t]; ok {
				parts = append(parts, fmt.Sprintf("%s:%d", t, c))
			}
		}
		log.Printf("Agent #%d 记忆更新: +%d 条 (%s)", agentID, len(entries), strings.Join(parts, " "))
	}
}

// ── History ──

func LoadHistory(id int) []llm.Message {
	data, err := os.ReadFile(filepath.Join(agentDir(id), "history.json"))
	if err != nil {
		return []llm.Message{}
	}
	var msgs []llm.Message
	json.Unmarshal(data, &msgs)
	return msgs
}

func SaveHistory(id int, msgs []llm.Message) {
	data, _ := json.MarshalIndent(msgs, "", "  ")
	os.WriteFile(filepath.Join(agentDir(id), "history.json"), data, 0644)
}

// ── HTTP Handlers ──

func HandleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		llm.WriteJSON(w, 200, map[string]interface{}{"agents": S.List()})
	case http.MethodPost:
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "" {
			body.Model = "gpt-5.4"
		}
		a := S.Create(body.Model)
		llm.WriteJSON(w, 201, a)
	default:
		http.Error(w, "不支持的方法", 405)
	}
}

func HandleAgent(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agent/")
	parts := strings.SplitN(path, "/", 2)

	id, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "无效的 Agent ID", 400)
		return
	}
	if S.Get(id) == nil {
		llm.WriteJSON(w, 404, map[string]string{"error": "Agent 不存在"})
		return
	}

	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}

	switch sub {
	case "":
		handleRoot(w, r, id)
	case "chat":
		handleChat(w, r, id)
	case "memory":
		handleMemory(w, r, id)
	case "history":
		handleHistory(w, r, id)
	case "title":
		handleTitle(w, r, id)
	case "model":
		handleModel(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		llm.WriteJSON(w, 200, map[string]interface{}{
			"agent": S.Get(id), "memory": LoadMemory(id), "history": LoadHistory(id),
		})
	case http.MethodDelete:
		S.Delete(id)
		llm.WriteJSON(w, 200, map[string]string{"status": "deleted"})
	default:
		http.Error(w, "不支持的方法", 405)
	}
}

func workspaceSystemPrompt(agentID int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("你是一个能够访问本地项目文件的 AI 助手。\n\n项目根目录: %s\n\n", tools.WorkspacePath))
	sb.WriteString("当用户提到文件或代码时，请主动使用工具读取实际内容，不要凭猜测回答。\n")
	sb.WriteString("不确定文件位置时，先用 list_files 查看目录结构。\n\n")

	sb.WriteString(`== 搜索工具 ==
- grep_search：按内容搜索（正则匹配文件内容）
- glob_search：按文件名搜索（glob 模式匹配文件路径，如 **/*.go）
- list_files：目录结构概览（树形列表）
搜索策略：知道文件名 → glob_search；知道代码内容 → grep_search；了解目录结构 → list_files

== 文件编辑工具 ==
- edit_file：精确字符串替换（修改已有文件，先 read_file 确认内容）
- write_file：创建新文件或完全覆盖（优先用 edit_file 修改已有文件）

== 子 Agent 工具 ==
- explore：轻量级只读搜索，独立上下文，适合查目录、搜函数、读配置。可同时调用多个并行搜索。
- delegate_task：只读多步子任务，比 explore 超时更长、上下文更大，仅在 explore 不够时使用。
决策：能用 1 次 grep/read 解决的直接用工具；需要 3+ 次搜索的用 explore；可分解为多个独立子问题的用多个 explore 并行。

== 代码修改行为准则 ==
1. 先搜后读再改：修改前先用 grep_search/glob_search 定位，再用 read_file 确认，最后用 edit_file 修改。
2. 工具优先于命令：
   - 读文件用 read_file，不要用 run_command("cat ...")
   - 改文件用 edit_file，不要用 run_command("sed ...")
   - 搜索用 grep_search，不要用 run_command("grep ...")
   - 查文件名用 glob_search，不要用 run_command("find ...")
3. 修改后验证：edit_file 或 write_file 后，主动运行 run_command("go build ./...") 或相关测试验证。
4. 如实报告：测试失败就附上输出说明，不要声称"已通过"。
5. 失败后诊断：修复失败先分析原因再换策略，不要盲目重试同样的操作。

`)

	if sp := SystemPrompt(agentID); sp != "" {
		sb.WriteString(sp)
	}
	return sb.String()
}

func handleChat(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", 405)
		return
	}
	var req llm.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		llm.WriteJSON(w, 400, llm.ChatResponse{Error: "请求格式错误"})
		return
	}
	if req.Message == "" {
		llm.WriteJSON(w, 400, llm.ChatResponse{Error: "消息不能为空"})
		return
	}

	a := S.Get(id)
	model := a.Model
	if req.Model != "" {
		model = req.Model
	}

	history := LoadHistory(id)
	var messages []llm.Message
	messages = append(messages, llm.Message{Role: "system", Content: workspaceSystemPrompt(id)})
	if len(history) > MaxHistory {
		history = history[len(history)-MaxHistory:]
	}
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: "user", Content: req.Message})

	toolDefs := tools.GetToolDefs()
	cache := tools.NewReadCache()
	cache.Model = model
	var finalReply string
	var toolsUsed []string
	startTime := time.Now()

	for round := 0; round < MaxToolRounds; round++ {
		if round >= 3 {
			tools.CleanOldToolResults(messages)
		}

		msg, err := llm.CallWithToolsRetry(messages, toolDefs, model, 1)
		if err != nil {
			log.Printf("Agent #%d LLM 调用失败 (round %d): %v", id, round, err)
			llm.WriteJSON(w, 500, llm.ChatResponse{Error: fmt.Sprintf("调用失败: %v", err)})
			return
		}

		if len(msg.ToolCalls) == 0 {
			finalReply = msg.Content
			break
		}

		messages = append(messages, *msg)

		for _, tc := range msg.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Function.Name)
			log.Printf("Agent #%d 工具调用 [round %d]: %s(%s)", id, round+1, tc.Function.Name, tc.Function.Arguments)
		}
		results := tools.ExecutePartitioned(msg.ToolCalls, cache)
		for _, r := range results {
			if r.IsError {
				log.Printf("Agent #%d 工具执行失败: %s", id, r.Name)
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: r.ToolCallID,
				Content:    r.Content,
			})
		}
	}

	if finalReply == "" {
		log.Printf("Agent #%d 达到 %d 轮工具上限，强制生成总结", id, MaxToolRounds)
		tools.CleanOldToolResults(messages)
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: "你已达到工具调用轮数上限。请立即根据以上所有工具返回的信息，给出完整的回答。不要再调用任何工具。",
		})
		summary, err := llm.CallWithToolsRetry(messages, nil, model, 1)
		if err != nil {
			finalReply = "处理过程达到了最大工具调用轮数，请尝试更具体的问题。"
		} else {
			finalReply = summary.Content
		}
	}

	elapsed := time.Since(startTime)
	if len(toolsUsed) > 0 {
		log.Printf("Agent #%d 对话完成: %d 轮工具调用 [%s], 耗时 %v",
			id, len(toolsUsed), strings.Join(toolsUsed, ", "), elapsed.Round(time.Millisecond))
	}

	full := LoadHistory(id)
	full = append(full, llm.Message{Role: "user", Content: req.Message})
	full = append(full, llm.Message{Role: "assistant", Content: finalReply})
	SaveHistory(id, full)

	if a.Title == "新对话" {
		title := req.Message
		if len([]rune(title)) > 20 {
			title = string([]rune(title)[:20]) + "..."
		}
		S.UpdateTitle(id, title)
	}

	go ExtractMemory(id, req.Message, finalReply)

	llm.WriteJSON(w, 200, llm.ChatResponse{Reply: finalReply, Model: model})
}

func handleMemory(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		llm.WriteJSON(w, 200, map[string]interface{}{"entries": LoadMemory(id)})
	case http.MethodDelete:
		SaveMemory(id, []MemoryEntry{})
		llm.WriteJSON(w, 200, map[string]string{"status": "cleared"})
	default:
		http.Error(w, "不支持的方法", 405)
	}
}

func handleHistory(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		llm.WriteJSON(w, 200, map[string]interface{}{"history": LoadHistory(id)})
	case http.MethodDelete:
		SaveHistory(id, []llm.Message{})
		llm.WriteJSON(w, 200, map[string]string{"status": "cleared"})
	default:
		http.Error(w, "不支持的方法", 405)
	}
}

func handleTitle(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodPut {
		http.Error(w, "仅支持 PUT", 405)
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Title != "" {
		S.UpdateTitle(id, body.Title)
	}
	llm.WriteJSON(w, 200, map[string]string{"status": "ok"})
}

func handleModel(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodPut {
		http.Error(w, "仅支持 PUT", 405)
		return
	}
	var body struct {
		Model string `json:"model"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Model != "" {
		S.UpdateModel(id, body.Model)
	}
	llm.WriteJSON(w, 200, map[string]string{"status": "ok"})
}
