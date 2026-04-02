# 多轮编排改进设计

> 参考 Claude Code Team Mode 的编排架构，为 chat_server 设计可落地的多轮编排方案。
>
> 前置阅读：[当前架构分析](./current-architecture.md)

---

## 目录

1. [设计目标](#1-设计目标)
2. [核心概念模型](#2-核心概念模型)
3. [多轮编排循环](#3-多轮编排循环)
4. [Worker 上下文持久化](#4-worker-上下文持久化)
5. [Leader 指令协议扩展](#5-leader-指令协议扩展)
6. [异步命令队列](#6-异步命令队列)
7. [Worker 进度感知](#7-worker-进度感知)
8. [智能结果摘要](#8-智能结果摘要)
9. [Agent-Team 记忆打通](#9-agent-team-记忆打通)
10. [错误处理与重试](#10-错误处理与重试)

---

## 1. 设计目标

### 1.1 核心原则

- **渐进式改造**：不破坏现有功能，在当前 Go + 标准库架构上迭代
- **实用优先**：不照搬 Claude Code 的全部复杂度（如 tmux/iTerm 后端、UDS 跨会话通信），只取其编排精髓
- **可观测性**：每个改进都要在 SSE 事件流和前端 UI 中有所体现

### 1.2 目标能力

| 能力 | 当前 | 目标 |
|------|------|------|
| Leader 决策轮数 | 固定 2 轮 | 开放式 N 轮（Leader 自主决定结束时机） |
| Worker 上下文 | 无（每次全新调用） | 同一 Team 会话内保持连续对话 |
| 结果消费 | 同步阻塞全部完成 | 支持"先到先处理" |
| 进度感知 | 无 | Worker 执行时实时推送状态 |
| 结果截断 | 硬截断 1500 rune | 智能摘要 + 完整存储 |
| 记忆利用 | 仅 Agent 模式 | Team Worker 可读取项目上下文记忆 |
| 错误恢复 | 失败即终止 | 自动重试 + Leader 可重新分配 |

---

## 2. 核心概念模型

### 2.1 Worker 状态机

当前 Worker 无状态。改进后引入三态模型：

```
                    ┌─────────┐
          spawn     │         │  finish / error
     ───────────────► running ├──────────────────┐
                    │         │                  │
                    └────┬────┘                  ▼
                         │                ┌──────────┐
                         │   idle_notify  │          │  continue
                         └────────────────►  idle    ├──────────┐
                                          │          │          │
                                          └────┬─────┘          │
                                               │                │
                                               │ shutdown       │
                                               ▼           ┌────┘
                                          ┌──────────┐     │
                                          │ stopped  ◄─────┘
                                          └──────────┘
                                               ▲ timeout / max_turns
                                               │
                                          (auto cleanup)
```

**状态定义**：

| 状态 | 含义 | 可接收操作 |
|------|------|-----------|
| `running` | 正在执行 LLM 调用 | 等待完成 / 超时中断 |
| `idle` | 执行完毕，等待后续指令 | continue（新任务） / shutdown |
| `stopped` | 已终止 | 无（可被 Leader 重新 spawn） |

### 2.2 数据模型扩展

```go
// Worker 运行时状态（内存中）
type WorkerState struct {
    Name      string           // researcher / coder / reviewer
    Status    string           // running / idle / stopped
    History   []llm.Message    // 对话历史（session 级别）
    LastResult string          // 最近一次执行结果
    StartedAt time.Time
    TurnCount int              // 当前会话已执行轮数
}

// Team 运行时上下文（内存中，per-team-session）
type SessionContext struct {
    TeamID     int
    Workers    map[string]*WorkerState
    CommandQ   chan Command     // 异步命令队列
    Done       chan struct{}    // 会话结束信号
}
```

### 2.3 持久化文件扩展

```
data/teams/<id>/
├── messages.json              # 不变：协作消息日志
├── leader/
│   └── history.json           # 不变：Leader 对话转录
└── workers/
    ├── researcher/
    │   └── session_history.json   # [新增] Worker 会话级对话历史
    ├── coder/
    │   └── session_history.json
    └── reviewer/
        └── session_history.json
```

---

## 3. 多轮编排循环

### 3.1 从"两轮制"到"Leader 自主循环"

这是最核心的改进。当前 `runOrchestration` 的流程：

```
Leader dispatch → Workers execute → Leader synthesize → END
```

改进后：

```
while (!done) {
    Leader 分析（初始 / 中间结果 / 最终综合）
        → direct_reply    → 输出 + END
        → dispatch        → 执行 Workers → 收集结果 → continue
        → continue_worker → 给已有 Worker 追加指令 → 收集结果 → continue
        → verify          → 启动验证 Worker → 收集结果 → continue
        → synthesize      → 输出综合结果 + END
}
```

### 3.2 核心函数重构

```go
func runOrchestration(teamID int, userMessage string, sse *sseWriter) {
    t := S.Get(teamID)
    session := newSessionContext(teamID, t)
    defer session.Cleanup()

    appendMessage(teamID, newMsg("user", "leader", "chat", userMessage))
    sse.send("user_message", map[string]string{"content": userMessage})

    // Leader 上下文初始化
    leaderMsgs := buildLeaderContext(teamID, t, userMessage)
    
    maxLeaderTurns := 6  // 安全阀：Leader 最多决策 6 轮
    turnCount := 0

    for turnCount < maxLeaderTurns {
        turnCount++
        sse.send("leader_start", map[string]string{
            "status": fmt.Sprintf("Leader 第 %d 轮分析...", turnCount),
            "turn":   strconv.Itoa(turnCount),
        })

        // Leader LLM 调用
        leaderRaw, err := llm.Call(leaderMsgs, t.Model)
        if err != nil {
            sse.send("error", ...)
            return
        }

        cmd := parseLeaderCommand(leaderRaw)

        switch cmd.Action {
        case "direct_reply":
            // 直接回复，结束循环
            outputFinalReply(teamID, cmd.Content, sse)
            return

        case "dispatch":
            // 分派新任务
            results := dispatchAndExecute(teamID, t, session, cmd, sse)
            // 将结果注入 Leader 上下文，继续循环
            leaderMsgs = appendWorkerResults(leaderMsgs, cmd, results)

        case "continue_worker":
            // 向已有 Worker 发送后续指令
            results := continueWorkers(teamID, session, cmd, sse)
            leaderMsgs = appendWorkerResults(leaderMsgs, cmd, results)

        case "verify":
            // 启动独立验证
            results := runVerification(teamID, t, session, cmd, sse)
            leaderMsgs = appendWorkerResults(leaderMsgs, cmd, results)

        case "synthesize":
            // 综合回答并结束
            outputFinalReply(teamID, cmd.Content, sse)
            return
        }
    }

    // 安全阀触发：强制综合
    forceSynthesize(teamID, t, session, leaderMsgs, sse)
}
```

### 3.3 Leader 上下文累积

每轮 Leader 调用后，将 Worker 结果以结构化格式追加到 Leader 的消息列表中：

```go
func appendWorkerResults(leaderMsgs []llm.Message, cmd *leaderCommand, results []workerResult) []llm.Message {
    // 追加 Leader 的指令作为 assistant 消息
    leaderMsgs = append(leaderMsgs, llm.Message{
        Role: "assistant", Content: marshalCommand(cmd),
    })
    
    // 追加 Worker 结果作为 user 消息（模拟 Claude Code 的 <task-notification>）
    var sb strings.Builder
    sb.WriteString("[Worker 执行结果]\n\n")
    for _, r := range results {
        if r.Error != "" {
            sb.WriteString(fmt.Sprintf("### %s（%s）：❌ 失败\n%s\n\n", r.Worker, r.Label, r.Error))
        } else {
            sb.WriteString(fmt.Sprintf("### %s（%s）：✅ 完成\n%s\n\n", r.Worker, r.Label, r.Result))
        }
    }
    sb.WriteString("请根据以上结果，决定下一步行动。你可以：\n")
    sb.WriteString("- dispatch：分派新任务\n")
    sb.WriteString("- continue_worker：给已完成的 Worker 追加指令\n")
    sb.WriteString("- verify：启动验证\n")
    sb.WriteString("- synthesize/direct_reply：给出最终回答\n")
    
    leaderMsgs = append(leaderMsgs, llm.Message{
        Role: "user", Content: sb.String(),
    })
    
    return leaderMsgs
}
```

### 3.4 四阶段工作流（参考 Claude Code Coordinator）

改进后 Leader 的 system prompt 应指导其遵循四阶段工作流：

| 阶段 | 执行者 | 目的 |
|------|--------|------|
| **Research** | Workers（并行） | 调查问题，收集信息 |
| **Synthesis** | **Leader 自己** | 理解研究结果，制定实施方案 |
| **Implementation** | Workers | 按方案执行具体任务 |
| **Verification** | Workers（新视角） | 验证结果正确性 |

关键约束：**Leader 在 Synthesis 阶段必须自己理解 Worker 的研究结果，而不能偷懒写"根据研究员的结论"直接转述。**

---

## 4. Worker 上下文持久化

### 4.1 Session 级对话历史

```go
// Worker 执行时，复用已有上下文
func executeWorkerWithContext(teamID int, session *SessionContext, 
    t *Info, task taskAssign, sse *sseWriter) workerResult {
    
    state := session.GetOrCreateWorker(task.Worker)
    spec := findWorker(t.Workers, task.Worker)
    
    // 构建消息：system + 历史 + 新任务
    var msgs []llm.Message
    msgs = append(msgs, llm.Message{Role: "system", Content: spec.Specialty})
    
    // 截断历史到合理长度（保留最近 10 轮对话）
    history := state.History
    if len(history) > 20 {
        history = history[len(history)-20:]
    }
    msgs = append(msgs, history...)
    msgs = append(msgs, llm.Message{
        Role: "user", Content: fmt.Sprintf("Leader 分配的任务：\n%s", task.Task),
    })

    reply, err := llm.Call(msgs, t.Model)
    // ...
    
    // 追加到 Worker 历史
    state.History = append(state.History, 
        llm.Message{Role: "user", Content: task.Task},
        llm.Message{Role: "assistant", Content: reply},
    )
    state.Status = "idle"
    state.LastResult = reply
    state.TurnCount++
    
    // 持久化
    saveWorkerSessionHistory(teamID, task.Worker, state.History)
    
    return workerResult{Worker: task.Worker, Label: spec.Label, Result: reply}
}
```

### 4.2 Continue 机制

当 Leader 发出 `continue_worker` 指令时，复用 Worker 的已有上下文：

```go
func continueWorkers(teamID int, session *SessionContext, 
    cmd *leaderCommand, sse *sseWriter) []workerResult {
    
    var results []workerResult
    for _, task := range cmd.Tasks {
        state := session.Workers[task.Worker]
        if state == nil || state.Status != "idle" {
            // Worker 不存在或不在 idle 状态，回退到 spawn
            results = append(results, executeWorkerWithContext(...))
            continue
        }
        
        sse.send("worker_continue", map[string]string{
            "worker": task.Worker,
            "label":  state.Label,
            "task":   task.Task,
            "turn":   strconv.Itoa(state.TurnCount + 1),
        })
        
        // 继续执行，Worker 保有之前的全部上下文
        result := executeWorkerWithContext(teamID, session, ...)
        results = append(results, result)
    }
    return results
}
```

### 4.3 与 Claude Code 的 Continue vs. Spawn 对应

| 场景 | 操作 | 原因 |
|------|------|------|
| Researcher 调研后让 Coder 实现 | Spawn（新 Worker） | Coder 不需要 Researcher 的探索过程 |
| Reviewer 提出修改意见后让 Coder 修改 | Continue（同一 Worker） | Coder 需要之前的代码上下文 |
| Coder 修复后启动验证 | Spawn（新 Worker） | 验证者需要新鲜视角 |
| 补充研究员遗漏的信息 | Continue（同一 Worker） | 复用已有调研上下文 |

---

## 5. Leader 指令协议扩展

### 5.1 完整 JSON 协议

```json
{
  "action": "dispatch | direct_reply | continue_worker | verify | synthesize",
  "content": "回答内容（direct_reply/synthesize 时使用）",
  "plan": "分析计划（dispatch 时使用）",
  "tasks": [
    {
      "worker": "researcher | coder | reviewer",
      "task": "任务描述",
      "mode": "spawn | continue",
      "depends_on": ["researcher"]
    }
  ],
  "reason": "决策理由（可选，用于日志和调试）"
}
```

### 5.2 新增字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `action` | string | 扩展了 `continue_worker` 和 `verify` |
| `tasks[].mode` | string | `spawn`=全新执行，`continue`=复用上下文（默认 spawn） |
| `tasks[].depends_on` | []string | 依赖关系，被依赖的 Worker 先执行（可选） |
| `reason` | string | Leader 的决策理由，用于日志审计 |

### 5.3 Leader System Prompt 改进

```
你是一个团队的 Leader（编排者）。你的任务是在多轮对话中指挥团队完成用户需求。

## 团队成员
- researcher（研究员）：调研、分析、整理信息
- coder（编码者）：编写代码、修复 bug、设计方案
- reviewer（审核者）：代码审查、质量验证

## 工作流程

建议遵循四阶段工作流：
1. Research：让 Worker 调研问题
2. Synthesis：你自己理解调研结果，制定具体方案（不要偷懒让 Worker 替你理解）
3. Implementation：让 Worker 按方案执行
4. Verification：让新的 Worker 验证结果

## 决策规则

每轮你会看到 Worker 的执行结果，然后决定下一步。可用 action：

| action | 用途 | 何时使用 |
|--------|------|---------|
| direct_reply | 直接回答 | 简单问题 / 不需要 Worker |
| dispatch | 分派新任务 | 需要 Worker 调研或执行 |
| continue_worker | 追加指令 | Worker 已有上下文，需要继续 |
| verify | 验证结果 | 重要修改完成后，需要独立验证 |
| synthesize | 综合回答 | 所有工作完成，给用户最终回答 |

## 关键原则

1. Synthesis 阶段你必须自己理解 Worker 的结果，展示你的分析和判断
2. 不要过度分派——简单问题直接回答
3. 让 Coder 修改代码时用 continue（保留上下文），验证时用新 Worker（新鲜视角）
4. 每轮只输出一个 JSON 对象，不加其他文字

## JSON 格式

{"action":"xxx","plan":"...","tasks":[{"worker":"...","task":"...","mode":"spawn|continue"}],"content":"...","reason":"决策理由"}
```

---

## 6. 异步命令队列

### 6.1 设计思路

用 Go channel 实现简化版的"全局命令队列"，支持 Worker 先到先处理：

```go
type CommandType string

const (
    CmdWorkerDone  CommandType = "worker_done"
    CmdWorkerError CommandType = "worker_error"
    CmdUserAbort   CommandType = "user_abort"
)

type Command struct {
    Type    CommandType
    Worker  string
    Result  workerResult
}

type SessionContext struct {
    TeamID   int
    Workers  map[string]*WorkerState
    CommandQ chan Command
    Done     chan struct{}
}
```

### 6.2 异步 Worker 执行

```go
func executeWorkersAsync(teamID int, session *SessionContext, 
    t *Info, tasks []taskAssign, sse *sseWriter) []workerResult {
    
    // 启动所有 Worker（goroutine）
    for _, task := range tasks {
        go func(ta taskAssign) {
            result := executeWorkerWithContext(teamID, session, t, ta, sse)
            session.CommandQ <- Command{
                Type:   CmdWorkerDone,
                Worker: ta.Worker,
                Result: result,
            }
        }(task)
    }

    // 先到先处理：每个 Worker 完成时立即通知
    var results []workerResult
    pending := len(tasks)
    
    for pending > 0 {
        select {
        case cmd := <-session.CommandQ:
            switch cmd.Type {
            case CmdWorkerDone:
                results = append(results, cmd.Result)
                pending--
                // 实时通知：可以让 Leader 提前看到部分结果
                sse.send("worker_result_received", map[string]string{
                    "worker":    cmd.Worker,
                    "remaining": strconv.Itoa(pending),
                })
            case CmdUserAbort:
                // 用户中断
                return results
            }
        case <-time.After(workerTimeout):
            // 超时处理
            break
        }
    }
    
    return results
}
```

### 6.3 未来扩展：部分结果提前处理

当 Worker A 先完成时，Leader 可以提前处理 A 的结果并给 A 分配新任务，而 B 继续执行。这需要更复杂的调度逻辑，作为后续迭代目标。

---

## 7. Worker 进度感知

### 7.1 方案：LLM Streaming + SSE 转发

如果 LLM API 支持 streaming，可以将流式 token 实时转发给前端：

```go
// llm/client.go 扩展
func CallStream(messages []Message, model string, onChunk func(string)) (string, error) {
    // 使用 stream=true 调用 API
    // 每收到一个 chunk 回调 onChunk
}
```

```go
// team.go 中使用
llm.CallStream(msgs, t.Model, func(chunk string) {
    sse.send("worker_progress", map[string]string{
        "worker": task.Worker,
        "chunk":  chunk,
    })
})
```

### 7.2 备选方案：定时摘要（参考 Claude Code 30s Fork）

如果不支持 streaming，可以在 Worker 执行超过 N 秒后异步请求进度描述：

```go
func executeWorkerWithProgress(teamID int, task taskAssign, sse *sseWriter) workerResult {
    resultCh := make(chan workerResult, 1)
    
    go func() {
        result := executeWorker(...)
        resultCh <- result
    }()
    
    progressTicker := time.NewTicker(15 * time.Second)
    defer progressTicker.Stop()
    
    for {
        select {
        case result := <-resultCh:
            return result
        case <-progressTicker.C:
            sse.send("worker_heartbeat", map[string]string{
                "worker": task.Worker,
                "status": "仍在执行中...",
                "elapsed": "...",
            })
        }
    }
}
```

---

## 8. 智能结果摘要

### 8.1 双层存储：完整结果 + 摘要

```go
const (
    maxSummaryLength = 1500  // 给 Leader 的摘要长度
    maxFullResult    = 10000 // 完整结果存储长度
)

func processWorkerResult(reply string, worker string, teamID int) (summary, full string) {
    full = reply
    if len([]rune(full)) > maxFullResult {
        full = string([]rune(full)[:maxFullResult]) + "\n...(完整结果已截断)"
    }
    
    if len([]rune(reply)) <= maxSummaryLength {
        summary = reply  // 不需要摘要
    } else {
        summary = generateSummary(reply, worker)  // LLM 摘要
    }
    
    // 完整结果存入 Worker session history
    // 摘要传给 Leader 上下文
    return summary, full
}

func generateSummary(content, workerName string) string {
    msgs := []llm.Message{{
        Role: "user",
        Content: fmt.Sprintf(
            "请将以下 %s 的输出压缩为不超过 500 字的摘要，保留关键结论和代码片段：\n\n%s",
            workerName, content),
    }}
    summary, err := llm.Call(msgs, "gpt-5.4")
    if err != nil {
        // 降级：硬截断
        return string([]rune(content)[:maxSummaryLength]) + "\n...(已截断)"
    }
    return summary
}
```

---

## 9. Agent-Team 记忆打通

### 9.1 共享项目上下文

将 Agent 的 `project` 类型记忆作为 Team Worker 的可用上下文：

```go
func buildWorkerSystemPrompt(spec *WorkerSpec, teamID int) string {
    base := spec.Specialty
    
    // 查找同用户的 Agent 中的 project 类型记忆
    projectMemories := loadProjectMemories()
    if len(projectMemories) > 0 {
        base += "\n\n## 项目上下文（来自历史对话的记忆）\n"
        for _, m := range projectMemories {
            base += fmt.Sprintf("- %s\n", m.Content)
        }
    }
    
    return base
}

func loadProjectMemories() []agent.MemoryEntry {
    // 遍历所有 Agent 的 memory.json，提取 type="project" 的条目
    var results []agent.MemoryEntry
    agents := agent.S.List()
    for _, a := range agents {
        entries := agent.LoadMemory(a.ID)
        for _, e := range entries {
            if e.Type == "project" {
                results = append(results, e)
            }
        }
    }
    return results
}
```

### 9.2 团队级 Scratchpad

为每个 Team 会话创建一个共享笔记区，Worker 之间可以通过它传递大段内容：

```
data/teams/<id>/scratchpad/
├── researcher_findings.md
├── coder_implementation.md
└── review_notes.md
```

Worker 的 system prompt 中告知 Scratchpad 路径，Worker 可以引用其他成员的笔记。

---

## 10. 错误处理与重试

### 10.1 LLM 调用重试

```go
func CallWithRetry(messages []Message, model string, maxRetries int) (string, error) {
    var lastErr error
    for i := 0; i <= maxRetries; i++ {
        reply, err := Call(messages, model)
        if err == nil {
            return reply, nil
        }
        lastErr = err
        if i < maxRetries {
            backoff := time.Duration(1<<uint(i)) * time.Second  // 1s, 2s, 4s
            time.Sleep(backoff)
        }
    }
    return "", fmt.Errorf("重试 %d 次后仍然失败: %w", maxRetries, lastErr)
}
```

### 10.2 Leader 错误感知与重新分配

在多轮循环中，Worker 失败后 Leader 可以看到错误信息并决定重新分配：

```json
// Worker 失败后 Leader 收到的上下文
{
    "worker": "coder",
    "status": "❌ 失败",
    "error": "API 返回 429: Rate limit exceeded",
    "suggestion": "可以：1) 重新 dispatch 给同一 Worker，2) 换一个 Worker，3) 降级为 direct_reply"
}
```

Leader 可以输出：
```json
{"action": "dispatch", "tasks": [{"worker": "coder", "task": "（与之前相同的任务）", "mode": "spawn"}], "reason": "coder 调用失败，重试一次"}
```

---

## 附录 A：SSE 事件流扩展

### 新增事件

| 事件 | 数据 | 说明 |
|------|------|------|
| `leader_turn` | `{turn, status}` | Leader 第 N 轮决策开始 |
| `worker_continue` | `{worker, label, task, turn}` | Worker 接收后续指令 |
| `worker_progress` | `{worker, chunk}` | Worker 流式输出（如支持） |
| `worker_heartbeat` | `{worker, status, elapsed}` | Worker 执行心跳 |
| `worker_result_received` | `{worker, remaining}` | 异步模式下 Worker 完成通知 |
| `verify_start` | `{worker, target}` | 验证阶段开始 |
| `max_turns_reached` | `{turns}` | Leader 达到最大轮数，强制综合 |

### 完整事件时序示例

```
→ user_message          "实现一个 JWT 认证中间件"
→ leader_turn           {turn: 1}
→ leader_plan           {plan: "1. 调研 JWT 库 2. 实现中间件 3. 代码审查"}
→ task_dispatch         {worker: "researcher", task: "调研 Go JWT 库"}
→ worker_start          {worker: "researcher"}
→ worker_heartbeat      {worker: "researcher", elapsed: "15s"}  ← 新增
→ worker_done           {worker: "researcher", result: "..."}
→ worker_result_received {worker: "researcher", remaining: 0}   ← 新增
→ leader_turn           {turn: 2}                                ← 新增
→ task_dispatch         {worker: "coder", task: "基于调研结果实现..."}
→ worker_start          {worker: "coder"}
→ worker_done           {worker: "coder", result: "..."}
→ leader_turn           {turn: 3}                                ← 新增
→ verify_start          {worker: "reviewer", target: "coder"}    ← 新增
→ worker_start          {worker: "reviewer"}
→ worker_done           {worker: "reviewer", result: "LGTM..."}
→ leader_turn           {turn: 4}                                ← 新增
→ final_reply           {from: "leader", content: "综合回答..."}
→ done                  {status: "complete"}
```

---

## 附录 B：与 Claude Code 的对应关系

| Claude Code 概念 | 本方案对应实现 |
|-------------------|---------------|
| Coordinator queryLoop `while(true)` | `runOrchestration` 中的 `for turnCount < maxLeaderTurns` |
| `<task-notification>` XML | Worker 结果注入 Leader 上下文的结构化文本 |
| Sleep 工具 + 通知队列 drain | 异步 CommandQ channel + select |
| SendMessage（Continue） | `continue_worker` action + Worker session history |
| Agent（Spawn） | `dispatch` action + `mode: "spawn"` |
| TaskStop | `maxLeaderTurns` 安全阀 + 超时控制 |
| 30s 进度摘要 fork | 心跳事件 / Streaming 转发 |
| Scratchpad 目录 | Team scratchpad 目录 |
| Worker 对话转录 | `workers/<name>/session_history.json` |
| 上下文压缩 | Worker history 截断 + 智能摘要 |
