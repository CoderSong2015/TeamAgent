package team

import (
	"chat_server/agent"
	"chat_server/llm"
	"chat_server/tools"
	"context"
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
	Dir               = "data/teams"
	MetaFile          = "data/teams_meta.json"
	workerTimeout       = 600 * time.Second
	maxWorkerResult     = 3000
	maxWorkerToolRounds = 4
	maxRevisionRounds = 2
	maxLeaderTurns    = 6
	maxWorkerHistory  = 8
)

// ── Data Models ──

type WorkerSpec struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Color     string `json:"color"`
	Specialty string `json:"specialty"`
}

type Info struct {
	ID        int          `json:"id"`
	Name      string       `json:"name"`
	Model     string       `json:"model"`
	Workers   []WorkerSpec `json:"workers"`
	CreatedAt string       `json:"created_at"`
}

type MetaStore struct {
	mu     sync.RWMutex
	NextID int    `json:"next_id"`
	Teams  []Info `json:"teams"`
}

type Message struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

type leaderCommand struct {
	Action  string       `json:"action"`
	Content string       `json:"content"`
	Plan    string       `json:"plan"`
	Tasks   []taskAssign `json:"tasks"`
	Reason  string       `json:"reason"`
}

type taskAssign struct {
	Worker    string   `json:"worker"`
	Task      string   `json:"task"`
	Mode      string   `json:"mode"`
	DependsOn []string `json:"depends_on"`
}

type workerResult struct {
	Worker       string
	Label        string
	Result       string
	Error        string
	ReworkFailed bool
	ReworkError  string
}

// ── Worker State (session-level, in-memory) ──

type WorkerState struct {
	Name       string
	Status     string // running / idle / stopped
	History    []llm.Message
	LastResult string
	TurnCount  int
}

type SessionContext struct {
	TeamID  int
	Workers map[string]*WorkerState
	mu      sync.Mutex
}

func newSessionContext(teamID int) *SessionContext {
	return &SessionContext{
		TeamID:  teamID,
		Workers: make(map[string]*WorkerState),
	}
}

func (sc *SessionContext) GetOrCreateWorker(name string) *WorkerState {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if w, ok := sc.Workers[name]; ok {
		return w
	}
	history := loadWorkerSessionHistory(sc.TeamID, name)
	w := &WorkerState{
		Name:    name,
		Status:  "idle",
		History: history,
	}
	sc.Workers[name] = w
	return w
}

func (sc *SessionContext) Cleanup() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for _, w := range sc.Workers {
		w.Status = "stopped"
	}
}

// ── Store & persistence ──

var S = &MetaStore{}

var defaultWorkers = []WorkerSpec{
	{Name: "researcher", Label: "研究员", Color: "#4f9cf7",
		Specialty: `你是团队中的研究员（Researcher）。你的核心策略是「委派搜索，综合分析」。

## 工作方式
1. 收到任务后，先规划需要了解哪些方面
2. 用 explore 工具并行委派搜索任务（每个 explore 负责一个方面）
3. 收到所有 explore 返回的摘要后，综合分析并输出结论
4. 只有 explore 返回信息不足时，才自己直接使用 read_file/grep_search 补充

## explore 使用示例
比如分析一个项目，你可以一次调用 3 个 explore：
- explore("列出项目根目录结构，识别主要模块和入口文件")
- explore("读取 README 或配置文件，了解项目依赖和启动方式")
- explore("搜索核心业务逻辑的主函数或路由定义")

## 关键原则
- 优先用 explore 委派搜索，保持自己的上下文精简
- 可以同时调用多个 explore 并行探索不同方面
- 只需 1 次简单搜索时（如 grep 一个关键字），直接用工具即可
- 综合分析时基于 explore 返回的摘要，给出有条理的结论`},
	{Name: "coder", Label: "编码者", Color: "#22c55e",
		Specialty: "你是团队中的编码者（Coder）。职责：\n- 编写高质量、可运行的代码\n- 修复 bug 和技术问题\n- 设计技术方案和架构\n- 代码重构和性能优化\n请认真完成 Leader 分配的任务，给出可直接使用的代码或完整技术方案。代码要有必要注释。"},
	{Name: "reviewer", Label: "审核者", Color: "#a855f7",
		Specialty: "你是团队中的审核者（Reviewer）。职责：\n- 审查代码质量、逻辑正确性\n- 发现潜在 bug、安全问题、性能隐患\n- 提出改进建议和最佳实践\n- 验证方案的可行性和完整性\n请认真完成 Leader 分配的任务，给出客观、专业的审查意见。"},
	{Name: "architect", Label: "架构师", Color: "#ec4899",
		Specialty: "你是团队中的架构师（Architect）。职责：\n- 设计系统整体架构和模块划分\n- 评估技术方案的可行性与扩展性\n- 编写设计文档（包含架构图、模块职责、接口定义、数据流）\n- 制定实施计划和分步方案\n请认真完成 Leader 分配的任务。设计文档要结构清晰、可直接指导编码。"},
	{Name: "secretary", Label: "书记员", Color: "#f59e0b",
		Specialty: "你是团队中的书记员（Secretary）。职责：\n- 分析团队对话，判断哪些信息值得记录到公共记忆池\n- 提取项目事实、用户偏好、架构决策等有价值的知识\n- 你是唯一有权写入公共记忆池的角色\n- 宁缺毋滥，只记录确定有价值的持久性知识"},
}

func Init() {
	os.MkdirAll(Dir, 0755)
	S.load()
	log.Printf("LLM Endpoint: %s", llm.ApiURL)
}

func (s *MetaStore) load() {
	data, err := os.ReadFile(MetaFile)
	if err != nil {
		s.NextID = 1
		s.Teams = []Info{}
		return
	}
	if err := json.Unmarshal(data, s); err != nil {
		s.NextID = 1
		s.Teams = []Info{}
	}
}

func (s *MetaStore) save() {
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(MetaFile, data, 0644)
}

func (s *MetaStore) Create(name, model string) Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		name = fmt.Sprintf("Team-%d", s.NextID)
	}
	t := Info{
		ID: s.NextID, Name: name, Model: model,
		Workers: defaultWorkers, CreatedAt: time.Now().Format("2006-01-02 15:04"),
	}
	s.NextID++
	s.Teams = append(s.Teams, t)
	s.save()

	d := teamDir(t.ID)
	os.MkdirAll(filepath.Join(d, "leader"), 0755)
	for _, w := range t.Workers {
		os.MkdirAll(filepath.Join(d, "workers", w.Name), 0755)
	}
	os.WriteFile(filepath.Join(d, "leader", "history.json"), []byte("[]"), 0644)
	os.WriteFile(filepath.Join(d, "messages.json"), []byte("[]"), 0644)

	log.Printf("Team #%d [%s] 创建完成", t.ID, t.Name)
	return t
}

func (s *MetaStore) Delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, t := range s.Teams {
		if t.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return false
	}
	s.Teams = append(s.Teams[:idx], s.Teams[idx+1:]...)
	s.save()
	os.RemoveAll(teamDir(id))
	log.Printf("Team #%d 已删除", id)
	return true
}

func (s *MetaStore) Get(id int) *Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.Teams {
		if t.ID == id {
			cp := t
			return &cp
		}
	}
	return nil
}

func (s *MetaStore) Rename(id int, newName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.Teams {
		if t.ID == id {
			s.Teams[i].Name = newName
			s.save()
			log.Printf("Team #%d 重命名为: %s", id, newName)
			return true
		}
	}
	return false
}

func (s *MetaStore) List() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Info, len(s.Teams))
	copy(out, s.Teams)
	return out
}

func teamDir(id int) string {
	return filepath.Join(Dir, strconv.Itoa(id))
}

// ── Message log ──

var msgMu sync.Mutex

func loadMessages(teamID int) []Message {
	data, err := os.ReadFile(filepath.Join(teamDir(teamID), "messages.json"))
	if err != nil {
		return []Message{}
	}
	var msgs []Message
	json.Unmarshal(data, &msgs)
	return msgs
}

func appendMessage(teamID int, msg Message) {
	msgMu.Lock()
	defer msgMu.Unlock()
	msgs := loadMessages(teamID)
	msgs = append(msgs, msg)
	data, _ := json.MarshalIndent(msgs, "", "  ")
	os.WriteFile(filepath.Join(teamDir(teamID), "messages.json"), data, 0644)
}

func newMsg(from, to, typ, content string) Message {
	return Message{
		ID: fmt.Sprintf("msg_%d", time.Now().UnixNano()), From: from, To: to,
		Type: typ, Content: content, Timestamp: time.Now().Format(time.RFC3339),
	}
}

// ── SSE ──

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	f.Flush()
	return &sseWriter{w: w, flusher: f}, true
}

func (s *sseWriter) send(event string, data interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, _ := json.Marshal(data)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, string(j))
	s.flusher.Flush()
}

// ── Worker session history persistence ──

func workerSessionPath(teamID int, workerName string) string {
	return filepath.Join(teamDir(teamID), "workers", workerName, "session_history.json")
}

func loadWorkerSessionHistory(teamID int, workerName string) []llm.Message {
	data, err := os.ReadFile(workerSessionPath(teamID, workerName))
	if err != nil {
		return nil
	}
	var msgs []llm.Message
	json.Unmarshal(data, &msgs)
	return msgs
}

func saveWorkerSessionHistory(teamID int, workerName string, history []llm.Message) {
	dir := filepath.Join(teamDir(teamID), "workers", workerName)
	os.MkdirAll(dir, 0755)
	data, _ := json.MarshalIndent(history, "", "  ")
	os.WriteFile(workerSessionPath(teamID, workerName), data, 0644)
}

func clearWorkerSessionHistories(teamID int) {
	workersDir := filepath.Join(teamDir(teamID), "workers")
	entries, err := os.ReadDir(workersDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			os.Remove(filepath.Join(workersDir, e.Name(), "session_history.json"))
		}
	}
}

// ── Leader prompt & parsing ──

func leaderSystemPrompt(workers []WorkerSpec) string {
	var sb strings.Builder
	sb.WriteString("你是一个团队的 Leader（编排者）。你的任务是在多轮对话中指挥团队完成用户需求。\n\n")
	sb.WriteString("## 团队成员\n")
	for _, w := range workers {
		if w.Name == "secretary" {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s（%s）：%s\n", w.Name, w.Label, firstLine(w.Specialty)))
	}
	sb.WriteString(`
## 工作流程

你的核心职责是判断任务规模，选择正确的工作流。

### 路由判断：大功能 vs 小功能

收到用户需求时，先判断规模：

**小功能**（bugfix、单文件修改、简单查询、配置调整）：
→ 直接 dispatch 给 coder 实现（或 researcher 调研后你综合回答）
→ 流程：[researcher] → coder → reviewer

**大功能**（涉及多模块、新项目、架构变更、系统设计）：
→ 必须经过架构师设计
→ 流程：researcher → 你理解后转给 architect → architect 输出设计文档 → coder 按文档实现 → reviewer 审核

**纯调研/分析类**（项目分析、技术对比、文档总结）：
→ dispatch 给 researcher，你综合回答即可

### 大功能标准流程（五阶段）

1. **Research**：dispatch researcher 调研问题背景、现有代码、依赖关系
2. **Synthesis**：你理解调研结果，明确需求边界和约束
3. **Design**：dispatch architect，把调研结果和你的理解一起发给它，让它输出设计文档
4. **Implementation**：dispatch coder，把架构师的设计文档作为实施依据
5. **Verification**：dispatch reviewer 审核代码和设计一致性

### 小功能标准流程（三阶段）

1. **调研（可选）**：简单问题跳过；需要了解现有代码时 dispatch researcher
2. **编码**：dispatch coder 实现
3. **审核**：dispatch reviewer 审核

## 可用 action

| action | 用途 | 何时使用 |
|--------|------|---------|
| direct_reply | 直接回答 | 简单问题，不需要 Worker |
| dispatch | 分派新任务 | 需要 Worker 调研/设计/执行 |
| continue_worker | 追加指令 | Worker 已有上下文，需要继续 |
| verify | 验证结果 | 重要修改完成后，需要独立验证 |
| synthesize | 综合回答 | 所有工作完成，给用户最终回答 |

## 关键原则

1. **先判断规模再行动**——大功能必须经过架构师，不要跳过设计直接编码
2. Synthesis 阶段你必须自己理解 Worker 的结果，展示你的分析和判断
3. 给 architect 的任务必须包含充足的背景信息（调研结果、需求边界、约束条件）
4. 给 coder 的任务必须附上架构师的设计文档作为实施依据
5. 不要过度分派——简单问题直接回答
6. 让 Worker 修改时用 continue_worker（保留上下文），验证时用 verify（新鲜视角）
7. 每轮只输出一个 JSON 对象，不加其他文字

## JSON 格式

{"action":"xxx","plan":"分析计划","tasks":[{"worker":"xxx","task":"任务描述","mode":"spawn|continue"}],"content":"回答内容","reason":"决策理由"}

字段说明：
- action（必填）：上表中的 action 之一
- content（direct_reply/synthesize 时必填）：回答内容，支持 \n 换行
- plan（dispatch 时可选）：你的分析计划，大功能时需说明判断依据
- tasks（dispatch/continue_worker/verify 时必填）：任务分配列表
- tasks[].worker：worker 名称（researcher/coder/reviewer/architect）
- tasks[].task：具体任务描述
- tasks[].mode：spawn（默认，全新执行）或 continue（复用上下文）
- reason（可选）：你的决策理由，大功能请注明"大功能，需经过架构师设计"

规则：只输出 JSON，绝对不要其他内容。`)

	if memPrompt := TeamMemoryPromptForLeader(); memPrompt != "" {
		sb.WriteString("\n\n## 项目记忆\n")
		sb.WriteString("以下是团队积累的项目知识，请在分派任务和综合回答时参考：\n")
		sb.WriteString(memPrompt)
		sb.WriteString("\n注意：超过 7 天的记忆可能已过时，使用前请验证。\n")
	}

	return sb.String()
}

func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func parseLeaderCommand(raw string) *leaderCommand {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		var lines []string
		for _, l := range strings.Split(raw, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "```") {
				lines = append(lines, l)
			}
		}
		raw = strings.Join(lines, "\n")
	}
	start := strings.Index(raw, "{")
	if start < 0 {
		return &leaderCommand{Action: "direct_reply", Content: raw}
	}
	// json.Decoder reads exactly one JSON value, handles multiple objects correctly
	decoder := json.NewDecoder(strings.NewReader(raw[start:]))
	var cmd leaderCommand
	if err := decoder.Decode(&cmd); err != nil {
		return &leaderCommand{Action: "direct_reply", Content: raw}
	}
	if cmd.Action == "" {
		cmd.Action = "direct_reply"
		if cmd.Content == "" {
			cmd.Content = raw
		}
	}
	return &cmd
}

// ── Orchestration engine (multi-turn) ──

func runOrchestration(teamID int, userMessage string, sse *sseWriter) {
	t := S.Get(teamID)
	if t == nil {
		sse.send("error", map[string]string{"error": "团队不存在"})
		return
	}

	session := newSessionContext(teamID)
	defer session.Cleanup()

	var finalReply string
	var finalReplyType string // "reply" or "synthesis"
	var allWorkerResults []workerResult

	appendMessage(teamID, newMsg("user", "leader", "chat", userMessage))
	sse.send("user_message", map[string]string{"content": userMessage})

	hist := loadLeaderHistory(teamID)
	var leaderMsgs []llm.Message
	leaderMsgs = append(leaderMsgs, llm.Message{Role: "system", Content: leaderSystemPrompt(t.Workers)})
	if len(hist) > 10 {
		hist = hist[len(hist)-10:]
	}
	leaderMsgs = append(leaderMsgs, hist...)
	leaderMsgs = append(leaderMsgs, llm.Message{Role: "user", Content: userMessage})

orchLoop:
	for turnCount := 1; turnCount <= maxLeaderTurns; turnCount++ {
		sse.send("leader_start", map[string]string{
			"status": fmt.Sprintf("Leader 第 %d 轮分析...", turnCount),
			"turn":   strconv.Itoa(turnCount),
		})

		leaderRaw, err := llm.CallWithRetry(leaderMsgs, t.Model, 1)
		if err != nil {
			log.Printf("Team #%d Leader LLM 失败 (turn %d): %v", teamID, turnCount, err)
			sse.send("error", map[string]string{"error": fmt.Sprintf("Leader 调用失败: %v", err)})
			return
		}

		cmd := parseLeaderCommand(leaderRaw)
		if cmd.Reason != "" {
			log.Printf("Team #%d Leader turn %d: action=%s reason=%s", teamID, turnCount, cmd.Action, cmd.Reason)
		}

		switch cmd.Action {
		case "direct_reply":
			content := cmd.Content
			if content == "" {
				content = leaderRaw
			}
			saveLeaderTurn(teamID, userMessage, content)
			finalReply = content
			finalReplyType = "reply"
			break orchLoop

		case "dispatch":
			leaderMsgs = append(leaderMsgs, llm.Message{Role: "assistant", Content: leaderRaw})
			if cmd.Plan != "" {
				appendMessage(teamID, newMsg("leader", "*", "plan", cmd.Plan))
				sse.send("leader_plan", map[string]string{"plan": cmd.Plan})
			}
			results := dispatchAndExecute(teamID, t, session, cmd, sse)
			allWorkerResults = append(allWorkerResults, results...)
			leaderMsgs = appendWorkerResults(leaderMsgs, results)

		case "continue_worker":
			leaderMsgs = append(leaderMsgs, llm.Message{Role: "assistant", Content: leaderRaw})
			results := doContinueWorkers(teamID, session, t, cmd, sse)
			allWorkerResults = append(allWorkerResults, results...)
			leaderMsgs = appendWorkerResults(leaderMsgs, results)

		case "verify":
			leaderMsgs = append(leaderMsgs, llm.Message{Role: "assistant", Content: leaderRaw})
			sse.send("verify_start", map[string]string{"status": "开始验证阶段"})
			appendMessage(teamID, newMsg("leader", "*", "phase", "🔎 验证阶段"))
			results := doVerification(teamID, t, session, cmd, sse)
			allWorkerResults = append(allWorkerResults, results...)
			leaderMsgs = appendWorkerResults(leaderMsgs, results)

		case "synthesize":
			content := cmd.Content
			if content == "" {
				content = leaderRaw
			}
			saveLeaderTurn(teamID, userMessage, content)
			finalReply = content
			finalReplyType = "synthesis"
			break orchLoop

		default:
			content := cmd.Content
			if content == "" {
				content = leaderRaw
			}
			saveLeaderTurn(teamID, userMessage, content)
			finalReply = content
			finalReplyType = "reply"
			break orchLoop
		}
	}

	// 最大轮数后强制综合
	if finalReply == "" {
		log.Printf("Team #%d Leader 达到最大轮数 %d，强制综合", teamID, maxLeaderTurns)
		sse.send("max_turns_reached", map[string]string{"turns": strconv.Itoa(maxLeaderTurns)})
		finalReply = forceSynthesizeReturn(teamID, t, userMessage, leaderMsgs, sse)
		finalReplyType = "synthesis"
	}

	// ── 书记员拦截：Leader 最终回复前，先交给书记员记录 ──
	invokeSecretary(teamID, t, userMessage, allWorkerResults, finalReply, sse)

	// ── 发送最终回复给用户 ──
	appendMessage(teamID, newMsg("leader", "user", finalReplyType, finalReply))
	sse.send("final_reply", map[string]string{"from": "leader", "content": finalReply})

	// Worker 角色经验提取（异步，不阻塞最终回复）
	if len(allWorkerResults) > 0 {
		go ExtractWorkerMemories(teamID, allWorkerResults)
	}
}

// ── Dispatch & Execution ──

func dispatchAndExecute(teamID int, t *Info, session *SessionContext, cmd *leaderCommand, sse *sseWriter) []workerResult {
	researcher, architect, coder, reviewer, others := classifyTasks(cmd.Tasks)

	// 大功能 Pipeline：researcher → architect → coder → reviewer
	if architect != nil && coder != nil {
		sse.send("pipeline_mode", map[string]string{"status": "检测到大功能（架构师+编码），启用设计驱动 Pipeline"})
		appendMessage(teamID, newMsg("leader", "*", "phase", "大功能 Pipeline：调研 → 架构设计 → 编码 → 审核"))
		return executeArchPipeline(teamID, t, session, researcher, architect, coder, reviewer, others, sse)
	}

	// 小功能 Pipeline：coder → reviewer（含返工循环）
	if coder != nil && reviewer != nil {
		sse.send("pipeline_mode", map[string]string{"status": "检测到 编码+审核，启用 Pipeline 模式"})
		appendMessage(teamID, newMsg("leader", "*", "phase", "Pipeline 模式：调研 → 编码 → 审核（含返工循环）"))
		return executePipeline(teamID, t, session, researcher, coder, reviewer, others, sse)
	}

	reviewerDispatched := false
	for _, task := range cmd.Tasks {
		appendMessage(teamID, newMsg("leader", task.Worker, "task", task.Task))
		spec := findWorker(t.Workers, task.Worker)
		label := task.Worker
		if spec != nil {
			label = spec.Label
		}
		sse.send("task_dispatch", map[string]string{"worker": task.Worker, "label": label, "task": task.Task})
		if task.Worker == "reviewer" {
			reviewerDispatched = true
		}
	}
	results := executeTasksParallel(teamID, t, session, cmd.Tasks, sse)

	nextWorker := "leader"
	nextLabel := "Leader"
	if !reviewerDispatched && findWorker(t.Workers, "reviewer") != nil {
		nextWorker = "reviewer"
		nextLabel = "审核者"
	}
	for _, r := range results {
		if r.Error == "" && r.Worker != "reviewer" {
			sendHandoff(teamID, r, nextWorker, nextLabel, sse)
		}
	}

	if !reviewerDispatched {
		results = autoReviewResults(teamID, t, session, results, sse)
	}
	return results
}

func doContinueWorkers(teamID int, session *SessionContext, t *Info, cmd *leaderCommand, sse *sseWriter) []workerResult {
	for i := range cmd.Tasks {
		cmd.Tasks[i].Mode = "continue"
	}
	for _, task := range cmd.Tasks {
		appendMessage(teamID, newMsg("leader", task.Worker, "task", task.Task))
		spec := findWorker(t.Workers, task.Worker)
		label := task.Worker
		if spec != nil {
			label = spec.Label
		}
		state := session.GetOrCreateWorker(task.Worker)
		sse.send("worker_continue", map[string]string{
			"worker": task.Worker,
			"label":  label,
			"task":   task.Task,
			"turn":   strconv.Itoa(state.TurnCount + 1),
		})
	}
	return executeTasksParallel(teamID, t, session, cmd.Tasks, sse)
}

func doVerification(teamID int, t *Info, session *SessionContext, cmd *leaderCommand, sse *sseWriter) []workerResult {
	for _, task := range cmd.Tasks {
		appendMessage(teamID, newMsg("leader", task.Worker, "task", task.Task))
		spec := findWorker(t.Workers, task.Worker)
		label := task.Worker
		if spec != nil {
			label = spec.Label
		}
		sse.send("task_dispatch", map[string]string{"worker": task.Worker, "label": label, "task": task.Task})
	}
	return executeTasksParallel(teamID, t, session, cmd.Tasks, sse)
}

func appendWorkerResults(leaderMsgs []llm.Message, results []workerResult) []llm.Message {
	var sb strings.Builder
	sb.WriteString("[Worker 执行结果]\n\n")
	var reworkFailedHints []string
	for _, r := range results {
		if r.Error != "" {
			sb.WriteString(fmt.Sprintf("### %s（%s）：❌ 失败\n%s\n\n", r.Worker, r.Label, r.Error))
		} else if r.ReworkFailed {
			sb.WriteString(fmt.Sprintf("### %s（%s）：⚠️ 完成（但审核返工失败：%s）\n%s\n\n",
				r.Worker, r.Label, r.ReworkError, r.Result))
			reworkFailedHints = append(reworkFailedHints, r.Label)
		} else {
			sb.WriteString(fmt.Sprintf("### %s（%s）：✅ 完成\n%s\n\n", r.Worker, r.Label, r.Result))
		}
	}
	sb.WriteString("请根据以上结果，决定下一步行动。你可以：\n")
	sb.WriteString("- dispatch：分派新任务\n")
	sb.WriteString("- continue_worker：给已完成的 Worker 追加指令\n")
	sb.WriteString("- verify：启动验证\n")
	sb.WriteString("- synthesize/direct_reply：给出最终回答\n")
	if len(reworkFailedHints) > 0 {
		sb.WriteString(fmt.Sprintf("\n⚠️ 注意：%s 上次返工超时/失败，再次分派相同任务大概率仍会失败。"+
			"建议直接基于已有成果综合回答（synthesize），或缩小任务范围后重试。\n",
			strings.Join(reworkFailedHints, "、")))
	}

	leaderMsgs = append(leaderMsgs, llm.Message{
		Role: "user", Content: sb.String(),
	})
	return leaderMsgs
}

// ── Secretary: sole writer to public memory pool ──

func invokeSecretary(teamID int, team *Info, userMessage string, workerResults []workerResult, finalReply string, sse *sseWriter) {
	sse.send("secretary_start", map[string]string{"status": "书记员正在分析对话，记录有价值的知识..."})

	var ctxBuf strings.Builder
	ctxBuf.WriteString("## 用户提问\n")
	ctxBuf.WriteString(userMessage)
	ctxBuf.WriteString("\n\n")

	if len(workerResults) > 0 {
		ctxBuf.WriteString("## 团队工作成果\n")
		for _, r := range workerResults {
			if r.Error != "" || r.Result == "" {
				continue
			}
			result := r.Result
			if len([]rune(result)) > 1500 {
				result = string([]rune(result)[:1500]) + "\n...(已截断)"
			}
			ctxBuf.WriteString(fmt.Sprintf("### %s（%s）\n%s\n\n", r.Worker, r.Label, result))
		}
	}

	ctxBuf.WriteString("## Leader 最终回复\n")
	reply := finalReply
	if len([]rune(reply)) > 2000 {
		reply = string([]rune(reply)[:2000]) + "\n...(已截断)"
	}
	ctxBuf.WriteString(reply)

	prompt := `你是团队的书记员，负责维护公共记忆池。请分析以下团队对话，判断哪些信息值得长期记录。

## 记忆类型
1. **project** — 项目事实：技术栈、架构、文件结构、依赖、模块职责、部署方式
2. **user** — 用户偏好：偏好、角色、技术栈、工作习惯、沟通风格
3. **reference** — 外部引用：文档链接、API 文档、参考资料
4. **feedback** — 行为反馈：用户纠正、约束（如"不要使用X"、"我更喜欢Y"）

## 记录原则
- 只记录对未来任务有价值的持久性知识
- 项目架构和技术选型是最重要的记录内容
- 用户明确表达的偏好和约束必须记录
- 不记录一次性问答、临时代码、推测性内容
- 不记录通过读取文件即可获取的信息
- 宁缺毋滥，只记录确定的事实

## 输出格式
严格输出一个 JSON 数组，每个元素包含 type、name、content 字段。
无需记录则输出空数组 []。不要包含任何其他文字。

示例：
[
  {"type":"project","name":"后端技术栈","content":"chat_server 使用 Go + net/http，端口 8088"},
  {"type":"user","name":"代码风格","content":"用户偏好简洁风格，不要过多注释"}
]

## 团队对话内容

` + ctxBuf.String()

	msgs := []llm.Message{{Role: "user", Content: prompt}}
	raw, err := llm.Call(msgs, team.Model)
	if err != nil {
		log.Printf("Team #%d 书记员调用失败: %v", teamID, err)
		sse.send("secretary_done", map[string]string{"status": "书记员记录失败", "count": "0"})
		return
	}

	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "["); start >= 0 {
		if end := strings.LastIndex(raw, "]"); end >= start {
			raw = raw[start : end+1]
		}
	}

	var entries []agent.MemoryEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		limit := len(raw)
		if limit > 200 {
			limit = 200
		}
		log.Printf("Team #%d 书记员 JSON 解析失败: %v, raw: %s", teamID, err, raw[:limit])
		sse.send("secretary_done", map[string]string{"status": "书记员记录完成", "count": "0"})
		return
	}

	now := time.Now().Format(time.RFC3339)
	for i := range entries {
		entries[i].CreatedAt = now
		if entries[i].Type == "" {
			entries[i].Type = "project"
		}
	}

	if len(entries) > 0 {
		addTeamMemory(entries)
		counts := map[string]int{}
		for _, e := range entries {
			counts[e.Type]++
		}
		log.Printf("Team #%d 书记员记录了 %d 条记忆: %v", teamID, len(entries), counts)
	}

	sse.send("secretary_done", map[string]string{
		"status": fmt.Sprintf("书记员记录了 %d 条记忆", len(entries)),
		"count":  strconv.Itoa(len(entries)),
	})
}

func forceSynthesizeReturn(teamID int, t *Info, userMessage string, leaderMsgs []llm.Message, sse *sseWriter) string {
	sse.send("leader_synthesize", map[string]string{"status": "Leader 达到最大轮数，正在强制综合..."})

	leaderMsgs = append(leaderMsgs, llm.Message{
		Role: "user",
		Content: "你已经达到最大分析轮数。请立即根据以上所有信息给出最终综合回答。\n" +
			"只输出 JSON：{\"action\":\"synthesize\",\"content\":\"你的综合回答\"}",
	})

	synthRaw, err := llm.CallWithRetry(leaderMsgs, t.Model, 1)
	if err != nil {
		return "（达到最大分析轮数，综合失败：" + err.Error() + "）"
	}

	cmd := parseLeaderCommand(synthRaw)
	content := cmd.Content
	if content == "" {
		content = synthRaw
	}

	saveLeaderTurn(teamID, userMessage, content)
	return content
}

// ── Worker execution with session context ──

func executeTasksParallel(teamID int, t *Info, session *SessionContext, tasks []taskAssign, sse *sseWriter) []workerResult {
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make([]workerResult, len(tasks))

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, ta taskAssign) {
			defer wg.Done()
			r := executeWorkerWithContext(teamID, session, t, ta, sse)
			mu.Lock()
			results[idx] = r
			mu.Unlock()
		}(i, task)
	}
	wg.Wait()
	return results
}

func workerAgenticLoop(msgs []llm.Message, model string, worker string, sse *sseWriter) (string, error) {
	toolDefs := tools.GetToolDefs()
	cache := tools.NewReadCache()
	cache.Model = model
	var toolsUsed []string
	subAgentCount := 0

	for round := 0; round < maxWorkerToolRounds; round++ {
		if round >= 2 {
			tools.CleanOldToolResults(msgs)
		}

		msg, err := llm.CallWithToolsRetry(msgs, toolDefs, model, 1)
		if err != nil {
			return "", err
		}

		if len(msg.ToolCalls) == 0 {
			return msg.Content, nil
		}

		msgs = append(msgs, *msg)

		var subAgentTasks []tools.ParallelSubAgentTask
		var normalCalls []llm.ToolCall

		for _, tc := range msg.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Function.Name)
			if tools.IsSubAgentTool(tc.Function.Name) && subAgentCount < tools.MaxSubAgentsPerRun {
				subAgentTasks = append(subAgentTasks, tools.ParallelSubAgentTask{
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Args:       json.RawMessage(tc.Function.Arguments),
				})
				subAgentCount++
			} else {
				normalCalls = append(normalCalls, tc)
			}
		}

		if len(subAgentTasks) > 0 {
			for _, t := range subAgentTasks {
				sse.send("subagent_start", map[string]string{
					"worker": worker, "tool": t.Name, "id": t.ToolCallID,
				})
			}

			saResults := tools.ExecuteSubAgentsParallel(subAgentTasks, cache)

			for _, r := range saResults {
				if r.IsError {
					sse.send("subagent_error", map[string]string{
						"worker": worker, "id": r.ToolCallID, "error": r.Content,
					})
				} else {
					sse.send("subagent_done", map[string]string{
						"worker": worker, "id": r.ToolCallID,
					})
				}
				msgs = append(msgs, llm.Message{
					Role:       "tool",
					ToolCallID: r.ToolCallID,
					Content:    r.Content,
				})
			}
		}

		for _, tc := range normalCalls {
			sse.send("worker_tool_call", map[string]string{
				"worker": worker,
				"tool":   tc.Function.Name,
				"args":   tc.Function.Arguments,
			})
		}
		normalResults := tools.ExecutePartitioned(normalCalls, cache)
		for _, r := range normalResults {
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				ToolCallID: r.ToolCallID,
				Content:    r.Content,
			})
		}
	}

	log.Printf("Worker [%s] 达到 %d 轮工具上限，强制生成总结（已用工具: %s）", worker, maxWorkerToolRounds, strings.Join(toolsUsed, ", "))

	tools.CleanOldToolResults(msgs)
	msgs = append(msgs, llm.Message{
		Role:    "user",
		Content: "你已达到工具调用轮数上限。请立即根据以上所有工具返回的信息，给出完整的分析结论。不要再调用任何工具。",
	})

	finalMsg, err := llm.CallWithToolsRetry(msgs, nil, model, 1)
	if err != nil {
		return fmt.Sprintf("（工具调用达到 %d 轮上限，总结生成失败: %v）", maxWorkerToolRounds, err), nil
	}
	return finalMsg.Content, nil
}

func executeWorkerWithContext(teamID int, session *SessionContext, t *Info, task taskAssign, sse *sseWriter) workerResult {
	state := session.GetOrCreateWorker(task.Worker)
	spec := findWorker(t.Workers, task.Worker)
	label := task.Worker
	sysPrompt := "你是一个AI助手，请完成以下任务。"
	if spec != nil {
		label = spec.Label
		sysPrompt = spec.Specialty
	}

	sysPrompt += fmt.Sprintf(`

你可以访问本地项目文件。项目根目录: %s

## 工具使用原则
- 涉及代码或文件时，用工具读取实际内容，不要猜测
- 大文件必须用 offset+limit 分段读（如前 50 行），禁止读全文
- 收集到足够信息后立即停止工具调用，输出结论

## 搜索工具
- grep_search：按内容搜索（正则匹配文件内容）
- glob_search：按文件名搜索（glob 模式匹配文件路径，如 **/*.go）
- list_files：目录结构概览（树形列表）
搜索策略：知道文件名 → glob_search；知道代码内容 → grep_search；了解目录结构 → list_files

## 文件编辑工具
- edit_file：精确字符串替换（修改已有文件，先 read_file 确认内容）
- write_file：创建新文件或完全覆盖（优先用 edit_file 修改已有文件）

## 子 Agent 工具（推荐）
- explore：轻量级只读搜索，独立上下文，适合查目录、搜函数、读配置。可同时调用多个并行搜索。
- delegate_task：通用子任务，比 explore 慢，仅在 explore 不够时使用。
决策：能用 1 次 grep/read 解决的直接用工具；需要 3+ 次搜索的用 explore；可分解为多个独立子问题的用多个 explore 并行。

## 代码修改行为准则
1. 先搜后读再改：修改前先用 grep_search/glob_search 定位，再用 read_file 确认，最后用 edit_file 修改。
2. 工具优先于命令：
   - 读文件用 read_file，不要用 run_command("cat ...")
   - 改文件用 edit_file，不要用 run_command("sed ...")
   - 搜索用 grep_search，不要用 run_command("grep ...")
   - 查文件名用 glob_search，不要用 run_command("find ...")
3. 修改后验证：edit_file 或 write_file 后，主动运行 run_command("go build ./...") 或相关测试验证。
4. 如实报告：测试失败就附上输出说明，不要声称"已通过"。
5. 失败后诊断：修复失败先分析原因再换策略，不要盲目重试同样的操作。`, tools.WorkspacePath)

	if projectMem := TeamMemoryPromptForWorker(); projectMem != "" {
		sysPrompt += "\n\n## 项目背景\n" + projectMem
	}
	if workerExp := WorkerMemoryPrompt(task.Worker); workerExp != "" {
		sysPrompt += "\n\n## 你的经验积累\n" + workerExp
	}

	state.Status = "running"
	sse.send("worker_start", map[string]string{"worker": task.Worker, "label": label})

	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout)
	defer cancel()

	var msgs []llm.Message
	msgs = append(msgs, llm.Message{Role: "system", Content: sysPrompt})

	if task.Mode == "continue" {
		history := state.History
		if len(history) > maxWorkerHistory {
			history = history[len(history)-maxWorkerHistory:]
		}
		for i := range history {
			if history[i].Role == "tool" && len([]rune(history[i].Content)) > 300 {
				history[i].Content = string([]rune(history[i].Content)[:300]) + "\n...(历史工具结果已压缩)"
			}
		}
		msgs = append(msgs, history...)
	}

	msgs = append(msgs, llm.Message{Role: "user", Content: fmt.Sprintf("Leader 分配的任务：\n%s", task.Task)})

	replyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		reply, err := workerAgenticLoop(msgs, t.Model, task.Worker, sse)
		if err != nil {
			errCh <- err
		} else {
			replyCh <- reply
		}
	}()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()
	startTime := time.Now()

	var r workerResult
	r.Worker = task.Worker
	r.Label = label

	for {
		select {
		case reply := <-replyCh:
			truncated := reply
			if len([]rune(reply)) > maxWorkerResult {
				truncated = string([]rune(reply)[:maxWorkerResult]) + "\n...(已截断)"
			}
			r.Result = truncated

			state.History = append(state.History,
				llm.Message{Role: "user", Content: task.Task},
				llm.Message{Role: "assistant", Content: reply},
			)
			state.Status = "idle"
			state.LastResult = reply
			state.TurnCount++
			saveWorkerSessionHistory(teamID, task.Worker, state.History)

			appendMessage(teamID, newMsg(task.Worker, "leader", "result", truncated))
			sse.send("worker_done", map[string]string{"worker": task.Worker, "label": label, "result": truncated})
			return r

		case err := <-errCh:
			r.Error = err.Error()
			state.Status = "idle"
			appendMessage(teamID, newMsg(task.Worker, "leader", "error", err.Error()))
			sse.send("worker_error", map[string]string{"worker": task.Worker, "label": label, "error": err.Error()})
			return r

		case <-ctx.Done():
			r.Error = "执行超时"
			state.Status = "idle"
			appendMessage(teamID, newMsg(task.Worker, "leader", "error", "执行超时"))
			sse.send("worker_error", map[string]string{"worker": task.Worker, "label": label, "error": "执行超时"})
			return r

		case <-heartbeatTicker.C:
			elapsed := time.Since(startTime).Round(time.Second)
			sse.send("worker_heartbeat", map[string]string{
				"worker":  task.Worker,
				"label":   label,
				"status":  "仍在执行中...",
				"elapsed": elapsed.String(),
			})
		}
	}
}

// ── Auto-review: all worker results go through reviewer before Leader ──

func autoReviewResults(teamID int, team *Info, session *SessionContext, results []workerResult, sse *sseWriter) []workerResult {
	reviewerSpec := findWorker(team.Workers, "reviewer")
	if reviewerSpec == nil {
		return results
	}

	var reviewContent strings.Builder
	reviewContent.WriteString("请对以下团队成员的工作成果进行质量审核：\n\n")
	var reviewableWorkers []string
	for _, r := range results {
		if r.Error != "" || r.Worker == "reviewer" {
			continue
		}
		reviewableWorkers = append(reviewableWorkers, r.Worker)
		reviewContent.WriteString(fmt.Sprintf("---\n## [%s]（%s）的输出\n\n%s\n\n", r.Worker, r.Label, r.Result))
	}
	if len(reviewableWorkers) == 0 {
		return results
	}

	reviewContent.WriteString("---\n\n审核要求：\n")
	reviewContent.WriteString("1. 评估每位成员输出的准确性、完整性和质量\n")
	reviewContent.WriteString("2. 对每位成员的评估，用以下格式给出结论：\n")
	reviewContent.WriteString("   [成员名] APPROVED — 简要评价\n")
	reviewContent.WriteString("   [成员名] NEEDS_REWORK — 需要改进的具体内容\n")
	reviewContent.WriteString("3. 最后一行总结：如果全部通过输出 LGTM，如果有任何需要修改的输出 NEEDS_FIX\n")

	sse.send("phase_start", map[string]string{"phase": "review", "name": "质量审核阶段"})
	appendMessage(teamID, newMsg("leader", "*", "phase", "质量审核阶段：Reviewer 审核所有 Worker 的成果"))

	reviewTask := taskAssign{Worker: "reviewer", Task: reviewContent.String()}
	appendMessage(teamID, newMsg("leader", "reviewer", "task", "审核团队成员工作成果"))
	sse.send("task_dispatch", map[string]string{
		"worker": "reviewer", "label": reviewerSpec.Label,
		"task": fmt.Sprintf("审核 %s 的工作成果", strings.Join(reviewableWorkers, "、")),
	})

	reviewResultSlice := executeTasksParallel(teamID, team, session, []taskAssign{reviewTask}, sse)
	reviewResult := reviewResultSlice[0]

	if reviewResult.Error != "" {
		log.Printf("Team #%d 自动审核失败: %s，跳过审核", teamID, reviewResult.Error)
		return results
	}

	if !needsRevision(reviewResult.Result) {
		log.Printf("Team #%d 审核全部通过", teamID)
		sse.send("review_complete", map[string]string{"rounds": "0", "status": "审核通过"})
		results = append(results, reviewResult)
		sendHandoff(teamID, reviewResult, "leader", "Leader", sse)
		return results
	}

	log.Printf("Team #%d 审核未通过，开始返工", teamID)

	reviewSummary := briefSummary(reviewResult.Result, 100)
	for _, r := range results {
		if r.Error != "" || r.Worker == "reviewer" {
			continue
		}
		if !workerNeedsRework(reviewResult.Result, r.Worker) {
			continue
		}
		spec := findWorker(team.Workers, r.Worker)
		label := r.Worker
		if spec != nil {
			label = spec.Label
		}
		appendMessage(teamID, newMsg("reviewer", r.Worker, "handoff",
			fmt.Sprintf("审核未通过：%s @%s 请修改", reviewSummary, label)))
		sse.send("worker_handoff", map[string]string{
			"from": "reviewer", "from_label": "审核者",
			"to": r.Worker, "to_label": label,
			"summary": fmt.Sprintf("审核未通过：%s", reviewSummary),
		})
	}

	sse.send("revision_start", map[string]string{"round": "1", "reason": "审核者发现问题，需要改进"})
	appendMessage(teamID, newMsg("leader", "*", "revision", "审核未通过，相关 Worker 正在改进"))

	reworkSucceeded := 0
	reworkFailed := 0
	var failedWorkers []string

	for i, r := range results {
		if r.Error != "" || r.Worker == "reviewer" {
			continue
		}
		if !workerNeedsRework(reviewResult.Result, r.Worker) {
			continue
		}
		log.Printf("Team #%d Worker [%s] 需要返工", teamID, r.Worker)
		reworkTask := taskAssign{
			Worker: r.Worker,
			Mode:   "continue",
			Task: fmt.Sprintf("审核者对你的工作提出了修改意见，请据此改进并输出完整的改进后结果：\n\n%s",
				reviewResult.Result),
		}
		spec := findWorker(team.Workers, r.Worker)
		label := r.Worker
		if spec != nil {
			label = spec.Label
		}
		sse.send("task_dispatch", map[string]string{
			"worker": r.Worker, "label": label, "task": "根据审核意见改进",
		})
		reworkResults := executeTasksParallel(teamID, team, session, []taskAssign{reworkTask}, sse)
		if reworkResults[0].Error == "" {
			results[i] = reworkResults[0]
			reworkSucceeded++
			sendHandoff(teamID, reworkResults[0], "reviewer", "审核者", sse)
		} else {
			reworkFailed++
			failedWorkers = append(failedWorkers, label)
			log.Printf("Team #%d Worker [%s] 返工失败: %s", teamID, r.Worker, reworkResults[0].Error)
			results[i].ReworkFailed = true
			results[i].ReworkError = reworkResults[0].Error
			sse.send("worker_handoff", map[string]string{
				"from": r.Worker, "from_label": label,
				"to": "leader", "to_label": "Leader",
				"summary": fmt.Sprintf("返工失败（%s），将原始成果提交给 Leader", reworkResults[0].Error),
			})
		}
	}

	var statusMsg string
	if reworkFailed > 0 && reworkSucceeded == 0 {
		statusMsg = fmt.Sprintf("返工失败（%s 超时/出错），使用原始成果", strings.Join(failedWorkers, "、"))
	} else if reworkFailed > 0 {
		statusMsg = fmt.Sprintf("部分返工成功，%s 返工失败", strings.Join(failedWorkers, "、"))
	} else {
		statusMsg = "审核完成（含 1 轮返工）"
	}
	sse.send("review_complete", map[string]string{"rounds": "1", "status": statusMsg})

	if reworkFailed == 0 {
		// 返工全部成功：用"审核通过"替换原始 NEEDS_REWORK 内容，避免 Leader 误判
		results = append(results, workerResult{
			Worker: "reviewer",
			Label:  "审核者",
			Result: fmt.Sprintf("审核完成（含 1 轮返工）：所有 Worker 已根据审核意见完成修改，成果质量达标。请直接综合回答。"),
		})
	} else {
		// 有返工失败的情况，保留原始审核内容供 Leader 参考
		results = append(results, reviewResult)
	}

	sendHandoff(teamID, workerResult{Worker: "reviewer", Label: "审核者", Result: statusMsg}, "leader", "Leader", sse)

	return results
}

func workerNeedsRework(reviewContent string, workerName string) bool {
	lower := strings.ToLower(reviewContent)
	workerLower := strings.ToLower(workerName)

	idx := strings.Index(lower, workerLower)
	if idx < 0 {
		return needsRevision(reviewContent)
	}

	end := idx + len(workerLower) + 300
	if end > len(lower) {
		end = len(lower)
	}
	nearby := lower[idx:end]

	for _, kw := range []string{"needs_rework", "needs_fix", "需要修改", "需要改进", "建议修改", "存在问题", "有以下问题", "需要返工"} {
		if strings.Contains(nearby, kw) {
			return true
		}
	}
	return false
}

// ── Worker handoff: brief summary + @next worker ──

func briefSummary(result string, maxRunes int) string {
	runes := []rune(result)
	if len(runes) <= maxRunes {
		return strings.TrimSpace(result)
	}
	truncated := string(runes[:maxRunes])
	for _, sep := range []string{"。", "；", "\n", "，", ".", ";", ","} {
		if idx := strings.LastIndex(truncated, sep); idx > len(truncated)/2 {
			truncated = truncated[:idx+len(sep)]
			break
		}
	}
	return strings.TrimSpace(truncated)
}

func sendHandoff(teamID int, from workerResult, nextWorker string, nextLabel string, sse *sseWriter) {
	summary := briefSummary(from.Result, 100)
	msg := fmt.Sprintf("已完成工作：%s @%s", summary, nextLabel)

	appendMessage(teamID, newMsg(from.Worker, nextWorker, "handoff", msg))
	sse.send("worker_handoff", map[string]string{
		"from":       from.Worker,
		"from_label": from.Label,
		"to":         nextWorker,
		"to_label":   nextLabel,
		"summary":    summary,
	})
}

// ── Phase-level review: worker output → reviewer → rework loop ──

type reviewOutcome struct {
	WorkerResult workerResult // final (possibly revised) worker output
	ReviewResult workerResult // reviewer's verdict
	Revised      bool         // whether rework occurred
}

func reviewAndRework(teamID int, team *Info, session *SessionContext,
	result workerResult, reviewFocus string, phaseName string, sse *sseWriter) reviewOutcome {

	reviewerSpec := findWorker(team.Workers, "reviewer")
	if reviewerSpec == nil {
		return reviewOutcome{WorkerResult: result}
	}

	sse.send("phase_start", map[string]string{"phase": "review", "name": fmt.Sprintf("%s — 审核", phaseName)})
	appendMessage(teamID, newMsg("leader", "*", "phase", fmt.Sprintf("%s — 审核者介入", phaseName)))

	reviewPrompt := fmt.Sprintf("请审核以下 %s（%s）的工作成果。\n\n## 审核重点\n%s\n\n## 待审内容\n%s\n\n"+
		"审核结论格式：\n- 通过：输出 LGTM + 简要评价\n- 需修改：输出 NEEDS_FIX + 具体修改意见",
		result.Worker, result.Label, reviewFocus, result.Result)

	reviewTask := taskAssign{Worker: "reviewer", Task: reviewPrompt}
	appendMessage(teamID, newMsg("leader", "reviewer", "task", fmt.Sprintf("审核%s的产出", phaseName)))
	sse.send("task_dispatch", map[string]string{
		"worker": "reviewer", "label": "审核者",
		"task": fmt.Sprintf("审核%s — %s", phaseName, result.Label),
	})

	current := result
	var reviewResult workerResult
	revised := false

	for round := 0; round <= maxRevisionRounds; round++ {
		reviewResults := executeTasksParallel(teamID, team, session, []taskAssign{reviewTask}, sse)
		reviewResult = reviewResults[0]

		if reviewResult.Error != "" || !needsRevision(reviewResult.Result) {
			break
		}

		if round >= maxRevisionRounds {
			break
		}

		revised = true
		log.Printf("Team #%d %s 审核未通过，第 %d 轮返工", teamID, phaseName, round+1)

		revSummary := briefSummary(reviewResult.Result, 100)
		appendMessage(teamID, newMsg("reviewer", current.Worker, "handoff",
			fmt.Sprintf("审核未通过：%s @%s 请修改", revSummary, current.Label)))
		sse.send("worker_handoff", map[string]string{
			"from": "reviewer", "from_label": "审核者",
			"to": current.Worker, "to_label": current.Label,
			"summary": fmt.Sprintf("审核未通过：%s", revSummary),
		})

		appendMessage(teamID, newMsg("leader", "*", "revision",
			fmt.Sprintf("%s：审核未通过，%s 正在修改（第%d轮）", phaseName, current.Label, round+1)))

		reworkTask := taskAssign{
			Worker: current.Worker,
			Mode:   "continue",
			Task:   fmt.Sprintf("审核者对你的工作提出了修改意见，请据此改进并输出完整的改进后结果：\n\n%s", reviewResult.Result),
		}
		sse.send("task_dispatch", map[string]string{
			"worker": current.Worker, "label": current.Label,
			"task": fmt.Sprintf("根据审核意见修改（第%d轮）", round+1),
		})
		reworkResults := executeTasksParallel(teamID, team, session, []taskAssign{reworkTask}, sse)
		if reworkResults[0].Error == "" {
			current = reworkResults[0]
			sendHandoff(teamID, current, "reviewer", "审核者", sse)
		}

		reviewTask = taskAssign{
			Worker: "reviewer",
			Mode:   "continue",
			Task:   fmt.Sprintf("请重新审查修改后的成果（第%d版）：\n\n%s", round+2, current.Result),
		}
		sse.send("task_dispatch", map[string]string{
			"worker": "reviewer", "label": "审核者",
			"task": fmt.Sprintf("重新审查%s（第%d版）", phaseName, round+2),
		})
	}

	status := "审核通过"
	if revised {
		status = "审核通过（含返工修改）"
	}
	sse.send("review_complete", map[string]string{"phase": phaseName, "status": status})

	if reviewResult.Error == "" {
		reviewSummary := fmt.Sprintf("%s审核%s。", status, phaseName)
		appendMessage(teamID, newMsg("reviewer", current.Worker, "handoff",
			fmt.Sprintf("%s @%s", reviewSummary, current.Label)))
		sse.send("worker_handoff", map[string]string{
			"from":       "reviewer",
			"from_label": "审核者",
			"to":         current.Worker,
			"to_label":   current.Label,
			"summary":    reviewSummary,
		})
	}

	return reviewOutcome{WorkerResult: current, ReviewResult: reviewResult, Revised: revised}
}

// ── Architecture Pipeline: researcher → architect → coder (每阶段审核介入) ──

func executeArchPipeline(teamID int, team *Info, session *SessionContext,
	researcher, architect, coder, reviewer *taskAssign, others []taskAssign, sse *sseWriter) []workerResult {

	var allResults []workerResult
	phaseNum := 1

	// ── Phase 1: 调研阶段 + 审核 ──
	var researchOutput string
	if researcher != nil || len(others) > 0 {
		phaseName := "调研阶段"
		sse.send("phase_start", map[string]string{"phase": strconv.Itoa(phaseNum), "name": phaseName})
		appendMessage(teamID, newMsg("leader", "*", "phase", fmt.Sprintf("Phase %d: %s", phaseNum, phaseName)))

		var phase1 []taskAssign
		if researcher != nil {
			appendMessage(teamID, newMsg("leader", "researcher", "task", researcher.Task))
			sse.send("task_dispatch", map[string]string{"worker": "researcher", "label": "研究员", "task": researcher.Task})
			phase1 = append(phase1, *researcher)
		}
		for _, o := range others {
			appendMessage(teamID, newMsg("leader", o.Worker, "task", o.Task))
			spec := findWorker(team.Workers, o.Worker)
			lbl := o.Worker
			if spec != nil {
				lbl = spec.Label
			}
			sse.send("task_dispatch", map[string]string{"worker": o.Worker, "label": lbl, "task": o.Task})
			phase1 = append(phase1, o)
		}

		p1Results := executeTasksParallel(teamID, team, session, phase1, sse)

		// 审核调研结果
		for i, r := range p1Results {
			if r.Error == "" && r.Worker != "reviewer" && reviewer != nil {
				reviewFocus := "调研结果的准确性、完整性和信息覆盖面。是否遗漏关键信息？是否有事实错误？"
				outcome := reviewAndRework(teamID, team, session, r, reviewFocus, "调研审核", sse)
				p1Results[i] = outcome.WorkerResult
				allResults = append(allResults, outcome.ReviewResult)
			}
		}

		allResults = append(allResults, p1Results...)

		for _, r := range p1Results {
			if r.Worker == "researcher" && r.Error == "" {
				researchOutput = r.Result
				architect.Task += "\n\n---\n以下是经审核通过的调研结果，请基于此进行架构设计：\n" + r.Result
				sendHandoff(teamID, r, "architect", "架构师", sse)
				break
			}
		}
		phaseNum++
	}

	// ── Phase 2: 架构设计阶段 + 审核 ──
	phaseName := "架构设计阶段"
	sse.send("phase_start", map[string]string{"phase": strconv.Itoa(phaseNum), "name": phaseName})
	appendMessage(teamID, newMsg("leader", "*", "phase", fmt.Sprintf("Phase %d: %s", phaseNum, phaseName)))
	appendMessage(teamID, newMsg("leader", "architect", "task", architect.Task))
	sse.send("task_dispatch", map[string]string{"worker": "architect", "label": "架构师", "task": architect.Task})

	archResults := executeTasksParallel(teamID, team, session, []taskAssign{*architect}, sse)
	archResult := archResults[0]

	if archResult.Error != "" {
		log.Printf("Team #%d 架构师执行失败: %s", teamID, archResult.Error)
		allResults = append(allResults, archResult)
		return allResults
	}

	// 审核架构设计
	if reviewer != nil {
		reviewFocus := "架构设计的合理性、可行性和完整性。包括：\n" +
			"- 模块划分是否清晰\n- 接口定义是否完整\n- 数据流是否合理\n" +
			"- 是否考虑了扩展性和边界条件\n- 与调研结果是否一致"
		outcome := reviewAndRework(teamID, team, session, archResult, reviewFocus, "设计审核", sse)
		archResult = outcome.WorkerResult
		allResults = append(allResults, outcome.ReviewResult)
	}
	allResults = append(allResults, archResult)
	phaseNum++

	sendHandoff(teamID, archResult, "coder", "编码者", sse)

	// 注入设计文档到编码者任务
	coder.Task += "\n\n---\n以下是经审核通过的架构设计文档，请严格按照设计实现：\n" + archResult.Result
	if researchOutput != "" {
		coder.Task += "\n\n---\n调研背景（供参考）：\n" + researchOutput
	}

	// ── Phase 3: 编码实现阶段 + 审核 ──
	phaseName = "编码实现阶段"
	sse.send("phase_start", map[string]string{"phase": strconv.Itoa(phaseNum), "name": phaseName})
	appendMessage(teamID, newMsg("leader", "*", "phase", fmt.Sprintf("Phase %d: %s", phaseNum, phaseName)))
	appendMessage(teamID, newMsg("leader", "coder", "task", coder.Task))
	sse.send("task_dispatch", map[string]string{"worker": "coder", "label": "编码者", "task": coder.Task})

	coderResults := executeTasksParallel(teamID, team, session, []taskAssign{*coder}, sse)
	coderResult := coderResults[0]

	if coderResult.Error != "" {
		allResults = append(allResults, coderResult)
		return allResults
	}

	// 审核编码实现（重点：与架构设计的一致性）
	if reviewer != nil {
		reviewFocus := "代码质量和与架构设计的一致性。重点关注：\n" +
			"- 实现是否严格遵循架构设计\n- 代码质量、逻辑正确性\n" +
			"- 是否有遗漏的模块或接口\n- 潜在 bug 和性能隐患\n\n" +
			"参考架构设计文档：\n" + archResult.Result
		outcome := reviewAndRework(teamID, team, session, coderResult, reviewFocus, "编码审核", sse)
		coderResult = outcome.WorkerResult
		allResults = append(allResults, outcome.ReviewResult)
	}
	allResults = append(allResults, coderResult)

	sendHandoff(teamID, coderResult, "leader", "Leader", sse)

	return allResults
}

// ── Pipeline mode (small feature: coder+reviewer dispatch) ──

func classifyTasks(tasks []taskAssign) (researcher, architect, coder, reviewer *taskAssign, others []taskAssign) {
	for i := range tasks {
		switch tasks[i].Worker {
		case "researcher":
			cp := tasks[i]
			researcher = &cp
		case "architect":
			cp := tasks[i]
			architect = &cp
		case "coder":
			cp := tasks[i]
			coder = &cp
		case "reviewer":
			cp := tasks[i]
			reviewer = &cp
		default:
			others = append(others, tasks[i])
		}
	}
	return
}

func needsRevision(content string) bool {
	upper := strings.ToUpper(content)
	if strings.Contains(upper, "LGTM") {
		return false
	}
	if strings.Contains(upper, "NEEDS_FIX") {
		return true
	}
	lower := strings.ToLower(content)
	for _, w := range []string{"审查通过", "没有发现问题", "代码质量良好", "没有问题", "可以接受", "总体良好"} {
		if strings.Contains(lower, w) {
			return false
		}
	}
	for _, w := range []string{"需要修改", "建议修改", "存在问题", "必须修复", "需要改进", "有以下问题", "严重问题"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func executePipeline(teamID int, team *Info, session *SessionContext,
	researcher, coder, reviewer *taskAssign, others []taskAssign, sse *sseWriter) []workerResult {

	var allResults []workerResult
	phaseNum := 1

	// ── Phase: Researcher + others (parallel) ──
	if researcher != nil || len(others) > 0 {
		phaseName := "调研阶段"
		sse.send("phase_start", map[string]string{"phase": strconv.Itoa(phaseNum), "name": phaseName})
		appendMessage(teamID, newMsg("leader", "*", "phase", fmt.Sprintf("Phase %d: %s", phaseNum, phaseName)))

		var phase1 []taskAssign
		if researcher != nil {
			appendMessage(teamID, newMsg("leader", "researcher", "task", researcher.Task))
			sse.send("task_dispatch", map[string]string{"worker": "researcher", "label": "研究员", "task": researcher.Task})
			phase1 = append(phase1, *researcher)
		}
		for _, o := range others {
			appendMessage(teamID, newMsg("leader", o.Worker, "task", o.Task))
			spec := findWorker(team.Workers, o.Worker)
			lbl := o.Worker
			if spec != nil {
				lbl = spec.Label
			}
			sse.send("task_dispatch", map[string]string{"worker": o.Worker, "label": lbl, "task": o.Task})
			phase1 = append(phase1, o)
		}

		p1Results := executeTasksParallel(teamID, team, session, phase1, sse)
		allResults = append(allResults, p1Results...)

		for _, r := range p1Results {
			if r.Worker == "researcher" && r.Error == "" {
				coder.Task += "\n\n---\n以下是研究员的调研结果，请参考：\n" + r.Result
				sendHandoff(teamID, r, "coder", "编码者", sse)
				break
			}
		}
		phaseNum++
	}

	// ── Phase: Coder ──
	phaseName := "编码阶段"
	sse.send("phase_start", map[string]string{"phase": strconv.Itoa(phaseNum), "name": phaseName})
	appendMessage(teamID, newMsg("leader", "*", "phase", fmt.Sprintf("Phase %d: %s", phaseNum, phaseName)))
	appendMessage(teamID, newMsg("leader", "coder", "task", coder.Task))
	sse.send("task_dispatch", map[string]string{"worker": "coder", "label": "编码者", "task": coder.Task})

	coderResults := executeTasksParallel(teamID, team, session, []taskAssign{*coder}, sse)
	latestCoderResult := coderResults[0]

	if latestCoderResult.Error == "" {
		sendHandoff(teamID, latestCoderResult, "reviewer", "审核者", sse)
	}
	phaseNum++

	// ── Phase: Review loop ──
	phaseName = "审核阶段"
	sse.send("phase_start", map[string]string{"phase": strconv.Itoa(phaseNum), "name": phaseName})
	appendMessage(teamID, newMsg("leader", "*", "phase", fmt.Sprintf("Phase %d: %s", phaseNum, phaseName)))

	var reviewTaskContent strings.Builder
	reviewTaskContent.WriteString(reviewer.Task)
	reviewTaskContent.WriteString("\n\n---\n请审查以下团队成员的输出：\n")
	for _, r := range allResults {
		if r.Error == "" && r.Worker != "reviewer" {
			reviewTaskContent.WriteString(fmt.Sprintf("\n## [%s]（%s）的输出\n%s\n", r.Worker, r.Label, r.Result))
		}
	}
	reviewTaskContent.WriteString(fmt.Sprintf("\n## [coder]（编码者）的输出\n%s\n", latestCoderResult.Result))
	reviewTaskContent.WriteString("\n---\n对每位成员用 [成员名] APPROVED 或 [成员名] NEEDS_REWORK 标注。\n")
	reviewTaskContent.WriteString("最后一行：全部通过输出 LGTM，有需要修改的输出 NEEDS_FIX\n")

	reviewTask := taskAssign{
		Worker: "reviewer",
		Mode:   "continue",
		Task:   reviewTaskContent.String(),
	}
	appendMessage(teamID, newMsg("leader", "reviewer", "task", "审查所有成员的输出"))
	sse.send("task_dispatch", map[string]string{"worker": "reviewer", "label": "审核者", "task": "审查所有成员的输出"})

	var latestReviewResult workerResult
	revisionCount := 0

	for round := 0; round <= maxRevisionRounds; round++ {
		reviewResults := executeTasksParallel(teamID, team, session, []taskAssign{reviewTask}, sse)
		latestReviewResult = reviewResults[0]

		if latestReviewResult.Error != "" || !needsRevision(latestReviewResult.Result) {
			if latestReviewResult.Error == "" {
				log.Printf("Team #%d 审核通过 (round %d)", teamID, round)
			}
			break
		}

		if round >= maxRevisionRounds {
			log.Printf("Team #%d 达到最大修改轮数 %d，停止循环", teamID, maxRevisionRounds)
			break
		}

		revisionCount++
		log.Printf("Team #%d 审核未通过，开始第 %d 轮修改", teamID, revisionCount)

		revSummary := briefSummary(latestReviewResult.Result, 100)
		appendMessage(teamID, newMsg("reviewer", "coder", "handoff",
			fmt.Sprintf("审核未通过：%s @编码者 请修改", revSummary)))
		sse.send("worker_handoff", map[string]string{
			"from": "reviewer", "from_label": "审核者",
			"to": "coder", "to_label": "编码者",
			"summary": fmt.Sprintf("审核未通过：%s", revSummary),
		})

		sse.send("revision_start", map[string]string{
			"round":  strconv.Itoa(revisionCount),
			"reason": "审核者认为代码需要修改",
		})
		appendMessage(teamID, newMsg("leader", "*", "revision",
			fmt.Sprintf("第%d轮修改：审核者提出修改意见，编码者正在修改", revisionCount)))

		revisionTask := taskAssign{
			Worker: "coder",
			Mode:   "continue",
			Task: fmt.Sprintf(
				"审核者对你的代码提出了修改意见，请据此修改并输出完整的改进后代码。\n\n"+
					"## 审核意见\n%s\n\n## 你之前的代码\n%s\n\n"+
					"请输出修改后的完整代码，并简要说明改了什么。",
				latestReviewResult.Result, latestCoderResult.Result),
		}

		appendMessage(teamID, newMsg("leader", "coder", "task", fmt.Sprintf("根据审核意见修改代码（第%d轮）", revisionCount)))
		sse.send("task_dispatch", map[string]string{
			"worker": "coder", "label": "编码者",
			"task": fmt.Sprintf("根据审核意见修改代码（第%d轮）", revisionCount),
		})

		revResults := executeTasksParallel(teamID, team, session, []taskAssign{revisionTask}, sse)
		latestCoderResult = revResults[0]

		if latestCoderResult.Error == "" {
			sendHandoff(teamID, latestCoderResult, "reviewer", "审核者", sse)
		}

		reviewTask = taskAssign{
			Worker: "reviewer",
			Mode:   "continue",
			Task: fmt.Sprintf(
				"这是编码者根据你的反馈修改后的代码（第%d版），请重新审查：\n\n%s\n\n"+
					"请在审查意见的最后一行明确给出结论：如果代码可以接受输出 LGTM，如果需要修改输出 NEEDS_FIX",
				revisionCount+1, latestCoderResult.Result),
		}
		appendMessage(teamID, newMsg("leader", "reviewer", "task", fmt.Sprintf("重新审查修改后的代码（第%d版）", revisionCount+1)))
		sse.send("task_dispatch", map[string]string{
			"worker": "reviewer", "label": "审核者",
			"task": fmt.Sprintf("重新审查（第%d版）", revisionCount+1),
		})
	}

	allResults = append(allResults, latestCoderResult, latestReviewResult)

	if revisionCount > 0 {
		appendMessage(teamID, newMsg("leader", "*", "phase",
			fmt.Sprintf("审核完成，共经历 %d 轮修改", revisionCount)))
		sse.send("review_complete", map[string]string{
			"rounds": strconv.Itoa(revisionCount),
			"status": fmt.Sprintf("审核完成，共 %d 轮修改", revisionCount),
		})
	}

	if latestReviewResult.Error == "" {
		sendHandoff(teamID, latestReviewResult, "leader", "Leader", sse)
	}

	return allResults
}

func findWorker(workers []WorkerSpec, name string) *WorkerSpec {
	for _, w := range workers {
		if w.Name == name {
			cp := w
			return &cp
		}
	}
	return nil
}

// ── Leader history ──

func loadLeaderHistory(teamID int) []llm.Message {
	data, err := os.ReadFile(filepath.Join(teamDir(teamID), "leader", "history.json"))
	if err != nil {
		return []llm.Message{}
	}
	var msgs []llm.Message
	json.Unmarshal(data, &msgs)
	return msgs
}

func saveLeaderTurn(teamID int, user, assistant string) {
	h := loadLeaderHistory(teamID)
	h = append(h, llm.Message{Role: "user", Content: user}, llm.Message{Role: "assistant", Content: assistant})
	data, _ := json.MarshalIndent(h, "", "  ")
	os.WriteFile(filepath.Join(teamDir(teamID), "leader", "history.json"), data, 0644)
}

// ── HTTP Handlers ──

func HandleTeams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		llm.WriteJSON(w, 200, map[string]interface{}{"teams": S.List()})
	case http.MethodPost:
		var body struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model == "" {
			body.Model = "gpt-5.4"
		}
		t := S.Create(body.Name, body.Model)
		llm.WriteJSON(w, 201, t)
	default:
		http.Error(w, "不支持的方法", 405)
	}
}

func HandleTeam(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/team/")
	parts := strings.SplitN(path, "/", 2)

	id, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "无效的 Team ID", 400)
		return
	}
	if S.Get(id) == nil {
		llm.WriteJSON(w, 404, map[string]string{"error": "Team 不存在"})
		return
	}

	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}

	switch sub {
	case "":
		handleTeamRoot(w, r, id)
	case "chat":
		handleTeamChat(w, r, id)
	case "messages":
		handleTeamMessages(w, r, id)
	case "memory":
		HandleTeamMemory(w, r)
	default:
		http.NotFound(w, r)
	}
}

func handleTeamRoot(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		llm.WriteJSON(w, 200, map[string]interface{}{"team": S.Get(id), "messages": loadMessages(id)})
	case http.MethodPut:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			llm.WriteJSON(w, 400, map[string]string{"error": "name 不能为空"})
			return
		}
		if !S.Rename(id, body.Name) {
			llm.WriteJSON(w, 404, map[string]string{"error": "团队不存在"})
			return
		}
		llm.WriteJSON(w, 200, S.Get(id))
	case http.MethodDelete:
		S.Delete(id)
		llm.WriteJSON(w, 200, map[string]string{"status": "deleted"})
	default:
		http.Error(w, "不支持的方法", 405)
	}
}

func handleTeamChat(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", 405)
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
		llm.WriteJSON(w, 400, map[string]string{"error": "消息不能为空"})
		return
	}
	sse, ok := newSSEWriter(w)
	if !ok {
		llm.WriteJSON(w, 500, map[string]string{"error": "SSE 不支持"})
		return
	}
	runOrchestration(id, body.Message, sse)
	sse.send("done", map[string]string{"status": "complete"})
}

func handleTeamMessages(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		llm.WriteJSON(w, 200, map[string]interface{}{"messages": loadMessages(id)})
	case http.MethodDelete:
		os.WriteFile(filepath.Join(teamDir(id), "messages.json"), []byte("[]"), 0644)
		os.WriteFile(filepath.Join(teamDir(id), "leader", "history.json"), []byte("[]"), 0644)
		clearWorkerSessionHistories(id)
		llm.WriteJSON(w, 200, map[string]string{"status": "cleared"})
	default:
		http.Error(w, "不支持的方法", 405)
	}
}
