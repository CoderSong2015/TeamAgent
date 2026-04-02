# Team 模式记忆系统设计文档

> 基于 Claude Code 记忆架构分析，结合 chat_server 当前实现现状的差距分析与改进方案

---

## 第 1 部分：Claude Code 记忆架构全景

### 1.1 记忆存储的 7 个层次

Claude Code 的记忆系统是一个**多层次、多粒度**的体系，从全局到会话逐层覆盖：

```
层次            存储位置                          生命周期       作用域
────           ──────                           ──────        ─────
1. Managed     /etc/claude-code/CLAUDE.md        管理员设定     全局（所有用户）
2. User        ~/.claude/CLAUDE.md               跨项目持久     当前用户
3. Project     <cwd>/CLAUDE.md + .claude/rules/  跨会话持久     当前项目（可 git 追踪）
4. Local       <cwd>/CLAUDE.local.md             跨会话持久     当前项目（不进 VCS）
5. Auto Memory ~/.claude/projects/<hash>/memory/  自动积累       当前项目 × 当前用户
   └─ Team Mem .../memory/team/                  团队共享       当前项目 × 所有用户
6. Agent Mem   ~/.claude/agent-memory/<type>/     按角色隔离     特定 Agent 类型
7. Session Mem .claude/sessions/<id>/summary.md   会话级         当前会话
```

### 1.2 记忆注入的 5 条通道

| 通道 | 载体 | 注入方式 | 可见角色 |
|------|------|---------|---------|
| CLAUDE.md | 磁盘文件 | `getUserContext()` → `<system-reminder>` | 所有角色（通过 userContext） |
| Auto Memory | memdir 目录 | `loadMemoryPrompt()` → system prompt | 普通模式 ✅ / Coordinator ❌ / Teammate ✅ |
| Agent Memory | 按类型目录 | `loadAgentMemoryPrompt()` → agent prompt | 配置了 memory 字段的 Agent |
| Relevant Mem | 语义搜索 | 附件（attachment）预取 | 主线程 |
| Session Mem | summary.md | compact 时摘要源 | 当前会话 |

### 1.3 关键设计决策

1. **Coordinator 的 system prompt 整段替换**：`getCoordinatorSystemPrompt()` 不含 `loadMemoryPrompt()`，导致 Leader 看不到 Auto Memory
2. **Worker 不触发记忆提取**：`!toolUseContext.agentId` 条件使子 Agent 跳过 `extractMemories`
3. **Agent Memory 按类型隔离**：`researcher/MEMORY.md` 和 `coder/MEMORY.md` 相互独立
4. **Scratchpad 作为运行时共享**：Coordinator 模式专用的跨 Worker 文件共享目录
5. **Team Memory 需要 feature flag**：通过 `TEAMMEM` 控制，是 Auto Memory 的子目录

### 1.4 Coordinator 模式的记忆不对称问题

Claude Code 自身也存在这个架构缺陷：

```
正常模式:
  getSystemPrompt()
    ├── loadMemoryPrompt()     ← Auto Memory ✅
    ├── session_guidance        ← 会话引导 ✅
    └── tools                   ← 工具说明 ✅

Coordinator 模式:
  getCoordinatorSystemPrompt()  ← 整段替换
    ├── 角色说明                 ← 编排指令 ✅
    ├── 工具使用                 ← Agent/SendMessage ✅
    └── 无 memory 段             ← Auto Memory ❌
```

**影响**：
- Coordinator 不知道 Auto Memory 目录存在 → 不会主动读写 MEMORY.md
- Worker 不知道项目记忆存在 → 每个 Worker 都是"无背景"的
- 记忆提取仍在主线程工作，但提取到的记忆 Coordinator prompt 中看不到

---

## 第 2 部分：chat_server 当前记忆系统现状

### 2.1 Agent 模式 — 有完整记忆

Agent 模式拥有一套可用的持久化记忆系统：

```
data/agents/{id}/
  ├── history.json    ← 对话历史
  └── memory.json     ← 结构化记忆（持久）
```

**记忆结构** (`MemoryEntry`)：

| 字段 | 说明 |
|------|------|
| Type | user / feedback / project / reference |
| Name | 记忆名称 |
| Description | 简短描述 |
| Content | 记忆内容 |
| CreatedAt | 创建时间（RFC3339） |

**记忆生命周期**：

```
用户对话 → handleChat() → 返回回复
                ↓
         go ExtractMemory()        ← 异步 LLM 调用提取记忆
                ↓
         addMemory() → memory.json ← 去重后持久化
                ↓
         下次对话 → SystemPrompt()  ← 注入 system prompt（带时间衰减标记）
```

**已实现的能力**：
- ✅ 自动记忆提取（LLM 分析对话 → 提取结构化记忆）
- ✅ 4 种记忆类型分类
- ✅ 去重机制（Name + Type 匹配）
- ✅ 时间衰减标记（今天 / 昨天 / N天前 / ⚠️ 可能已过时）
- ✅ 注入 system prompt
- ✅ 前端展示与清空
- ✅ 记忆使用规则指导

### 2.2 Team 模式 — 无记忆系统

Team 模式**完全没有记忆系统**，各角色只有会话历史：

```
data/teams/{id}/
  ├── leader/
  │   └── history.json              ← Leader 对话历史（保留最近 10 轮）
  ├── workers/
  │   ├── researcher/
  │   │   └── session_history.json  ← 会话级对话记录
  │   ├── coder/
  │   │   └── session_history.json
  │   └── reviewer/
  │       └── session_history.json
  └── messages.json                 ← 全局消息流
```

**各角色记忆能力对比**：

| 能力 | Agent 模式 | Leader | Researcher | Coder | Reviewer |
|------|-----------|--------|------------|-------|----------|
| 对话历史 | ✅ 完整 | ✅ 最近10轮 | ✅ 会话级 | ✅ 会话级 | ✅ 会话级 |
| 持久记忆 | ✅ memory.json | ❌ | ❌ | ❌ | ❌ |
| 记忆提取 | ✅ ExtractMemory | ❌ | ❌ | ❌ | ❌ |
| 项目上下文 | ✅ 自动注入 | ❌ | ❌ | ❌ | ❌ |
| 用户偏好 | ✅ 自动注入 | ❌ | ❌ | ❌ | ❌ |
| 跨角色共享 | N/A | ❌ | ❌ | ❌ | ❌ |

### 2.3 四个核心问题

#### 问题 1：Leader 没有记忆

Leader 的 system prompt 由 `leaderSystemPrompt()` 硬编码生成，不含任何记忆注入：

```go
func leaderSystemPrompt(workers []WorkerSpec) string {
    var sb strings.Builder
    sb.WriteString("你是一个团队的 Leader...")  // 纯编排指令
    // 无 memory 段、无项目上下文、无用户偏好
}
```

**影响**：Leader 每次对话都是"零背景"——不知道用户偏好、项目技术栈、上次讨论的架构决策。

#### 问题 2：Worker 没有记忆

Worker 的 system prompt 来自 `spec.Specialty`（角色说明）+ 工具使用指南，无记忆注入：

```go
sysPrompt = spec.Specialty  // "你是团队中的研究员..."
sysPrompt += "你可以访问本地项目文件..."  // 工具说明
// 无项目记忆、无历史经验
```

**影响**：
- Researcher 不记得之前调研过的项目结构、技术栈
- Coder 不记得用户偏好的编码风格、架构约定
- Reviewer 不记得之前审查中发现的常见问题模式

#### 问题 3：无公共记忆池

没有任何跨角色共享的知识存储。即使 Researcher 发现了"这个项目用 Go + net/http"，下次 Coder 接到任务时也完全不知道。

#### 问题 4：Agent 模式记忆孤岛

Agent 模式的 `memory.json` 与 Team 模式完全隔离。Agent #1 积累的丰富项目记忆，Team 模式无法使用。

```
data/agents/1/memory.json    ← Agent 模式独占
data/agents/3/memory.json    ← Agent 模式独占
data/teams/10/               ← 无 memory.json
```

### 2.4 与 Claude Code 的差距矩阵

| Claude Code 能力 | chat_server 状态 | 差距等级 |
|-----------------|-----------------|---------|
| CLAUDE.md（多层静态配置） | ❌ 不存在 | 🟡 中等 — 可用 system prompt 配置替代 |
| Auto Memory（自动积累） | ⚠️ Agent 模式有，Team 模式无 | 🔴 严重 — Team 模式核心缺失 |
| Agent Memory（按角色隔离） | ❌ 不存在 | 🟡 中等 — 角色特化记忆 |
| Team Memory（公共共享） | ❌ 不存在 | 🔴 严重 — 跨角色知识无法传递 |
| Session Memory（会话摘要） | ❌ 不存在 | 🟡 中等 — 影响 compact/resume |
| Scratchpad（运行时共享） | ❌ 不存在 | 🟢 低 — 当前通过 Leader 合成传递 |
| 记忆提取（主线程自动） | ⚠️ Agent 模式有，Team 模式无 | 🔴 严重 — Team 对话不积累知识 |
| 语义搜索预取 | ❌ 不存在 | 🟡 中等 — 需要向量化基础设施 |
| Compact 时记忆保留 | ❌ 不存在（无 compact 机制） | 🟢 低 — 当前上下文管理已有其他方案 |

---

## 第 3 部分：改进方案设计

### 3.1 设计原则

1. **最小侵入**：复用 Agent 模式已有的 `MemoryEntry` 体系，不新建存储格式
2. **渐进式**：按优先级分阶段实施，每阶段可独立验证
3. **Token 敏感**：记忆注入必须控制大小，避免加剧 context bloat
4. **写入安全**：多 Worker 并行时避免竞争写入
5. **向后兼容**：不影响现有 Agent 模式和 Team 模式的正常功能

### 3.2 分层记忆架构（目标状态）

```
chat_server 记忆层次（目标）:

  1. Team Memory Pool     data/team_memory.json       跨 Team 共享（公共记忆池）
     ├── project 类：项目事实（技术栈、架构、文件结构）
     ├── user 类：用户偏好（编码风格、沟通偏好）
     └── reference 类：常用文档、API 引用

  2. Team-level Memory    data/teams/{id}/memory.json  Team 级记忆
     └── 该 Team 讨论中积累的项目知识

  3. Worker History        data/teams/{id}/workers/*/   Worker 会话历史（已有）
     └── session_history.json

  4. Agent Memory          data/agents/{id}/memory.json Agent 记忆（已有）
     └── 可被 Team 模式引用
```

### 3.3 P0：Team 模式接入项目记忆（最小可行方案）

**目标**：让 Leader 和 Worker 看到项目上下文，打破"零背景"问题。

#### 3.3.1 引入公共记忆池

复用 Agent 模式的 `MemoryEntry` 结构，新增一个全局共享的记忆文件：

```
data/team_memory.json  ← 所有 Team 共享的项目级记忆
```

数据结构完全复用 `agent.MemoryEntry`，无需新类型。

#### 3.3.2 Leader System Prompt 注入记忆

修改 `leaderSystemPrompt()`，追加项目记忆段：

```go
func leaderSystemPrompt(workers []WorkerSpec) string {
    var sb strings.Builder
    sb.WriteString("你是一个团队的 Leader...")
    // ... 现有编排指令 ...

    // 注入公共记忆
    if memPrompt := loadTeamMemoryPrompt(); memPrompt != "" {
        sb.WriteString("\n\n## 项目记忆\n")
        sb.WriteString("以下是团队积累的项目知识，请在分派任务时参考：\n")
        sb.WriteString(memPrompt)
    }

    return sb.String()
}
```

**Token 预算控制**：记忆摘要限制在 1500 字符以内（约 500-700 tokens）。

#### 3.3.3 Worker System Prompt 注入项目上下文

修改 `executeWorkerWithContext()`，在 Worker 的 system prompt 中追加项目记忆：

```go
// 只注入 project 和 reference 类记忆（与角色无关的客观事实）
if projectMem := loadTeamMemoryForWorker(); projectMem != "" {
    sysPrompt += "\n\n## 项目背景\n" + projectMem
}
```

**过滤策略**：Worker 只看 `project` + `reference` 类记忆，不看 `user` + `feedback`（由 Leader 视角传递）。

#### 3.3.4 Team 对话后触发记忆提取

在 `runOrchestration()` 完成后，异步提取记忆：

```go
// orchestration 完成后
go extractTeamMemory(teamID, userMessage, finalContent)
```

**提取策略**：
- 只在主线程（orchestration 结束后）触发，不在 Worker 中触发
- 复用 `ExtractMemory` 的 prompt 模板，但写入 `team_memory.json`
- 去重逻辑与 Agent 模式一致（Name + Type 匹配）

#### 3.3.5 P0 文件变更清单

| 文件 | 变更 | 说明 |
|------|------|------|
| `team/team.go` | 修改 | `leaderSystemPrompt` 注入记忆、`executeWorkerWithContext` 注入项目上下文、`runOrchestration` 末尾触发提取 |
| `team/memory.go` | **新增** | `loadTeamMemory`、`saveTeamMemory`、`addTeamMemory`、`loadTeamMemoryPrompt`、`loadTeamMemoryForWorker`、`extractTeamMemory` |
| `agent/agent.go` | 无变更 | 复用 `MemoryEntry` 类型定义 |

#### 3.3.6 P0 数据流

```
用户提问 → runOrchestration()
              ├── leaderSystemPrompt()
              │     └── loadTeamMemoryPrompt() → [project + user + reference 记忆]
              │
              ├── Leader 分派 → Worker
              │     └── executeWorkerWithContext()
              │           └── loadTeamMemoryForWorker() → [project + reference 记忆]
              │
              ├── Worker 返回 → Leader 综合 → 最终回复
              │
              └── go extractTeamMemory(userMsg, finalReply)
                    ├── LLM 分析对话 → 提取 MemoryEntry[]
                    └── addTeamMemory() → data/team_memory.json
```

### 3.4 P1：Agent 记忆桥接（打破孤岛）

**目标**：让 Team 模式可以读取 Agent 模式积累的 `project` 类记忆。

#### 3.4.1 合并读取

```go
func loadTeamMemoryPrompt() string {
    // 1. 读取公共记忆池
    teamMem := loadTeamMemory()

    // 2. 从所有 Agent 的 memory.json 中提取 project 类记忆
    agentProjectMem := loadAgentProjectMemories()

    // 3. 合并去重
    merged := mergeMemories(teamMem, agentProjectMem)

    // 4. 格式化为 prompt 段
    return formatMemoryPrompt(merged)
}
```

**只读桥接**：Team 模式只**读取** Agent 的 `project` 记忆，不写入。避免双向耦合。

#### 3.4.2 P1 文件变更清单

| 文件 | 变更 | 说明 |
|------|------|------|
| `team/memory.go` | 修改 | 增加 `loadAgentProjectMemories()`、`mergeMemories()` |

### 3.5 P2：角色级记忆（Worker 经验积累）

**目标**：让每种角色积累专属经验。

#### 3.5.1 角色记忆存储

```
data/worker_memory/
  ├── researcher.json    ← 研究员经验（有效的搜索策略、项目结构发现等）
  ├── coder.json         ← 编码者经验（代码风格偏好、常见坑点等）
  └── reviewer.json      ← 审核者经验（常见问题模式、审查标准等）
```

#### 3.5.2 Worker 记忆注入

```go
if workerMem := loadWorkerMemory(task.Worker); workerMem != "" {
    sysPrompt += "\n\n## 你的经验积累\n" + workerMem
}
```

#### 3.5.3 Worker 记忆提取

**不在 Worker 执行期间提取**（与 Claude Code 一致，避免并行写入冲突）。

改为在 Leader 综合阶段，由主线程异步分析各 Worker 的工作成果，提取角色级记忆：

```go
// orchestration 完成后
go extractWorkerMemories(results)  // results 是所有 Worker 的工作成果
```

#### 3.5.4 P2 文件变更清单

| 文件 | 变更 | 说明 |
|------|------|------|
| `team/memory.go` | 修改 | 增加 `loadWorkerMemory()`、`saveWorkerMemory()`、`extractWorkerMemories()` |
| `team/team.go` | 修改 | Worker prompt 注入、orchestration 后触发提取 |

### 3.6 P3：前端记忆管理

**目标**：在 Team 模式前端展示和管理记忆。

- 查看公共记忆池内容
- 查看各角色记忆
- 手动添加/删除记忆条目
- 记忆导入/导出

（此阶段暂不详细设计，待 P0-P2 验证后再展开）

---

## 第 4 部分：Token 预算与风控

### 4.1 记忆注入的 Token 开销估算

| 注入位置 | 最大字符数 | 估算 Token | 频率 |
|---------|-----------|-----------|------|
| Leader system prompt | 1500 | ~500-700 | 每轮 Leader 调用 |
| Worker system prompt（项目上下文） | 800 | ~300-400 | 每个 Worker |
| Worker system prompt（角色经验） | 500 | ~200-300 | 每个 Worker |

**最坏情况**：1 次 Leader + 3 个 Worker = 700 + 3 × 700 = **~2800 tokens/轮**

**对比**：当前 Leader prompt 约 1200 tokens、Worker prompt 约 400 tokens。增幅约 50-70%。

### 4.2 记忆大小控制策略

```go
const (
    maxTeamMemoryEntries    = 30     // 公共记忆池最多 30 条
    maxWorkerMemoryEntries  = 15     // 每个角色记忆最多 15 条
    maxMemoryPromptChars    = 1500   // Leader 记忆注入最大字符数
    maxWorkerMemPromptChars = 800    // Worker 项目上下文最大字符数
    maxWorkerExpPromptChars = 500    // Worker 经验记忆最大字符数
)
```

### 4.3 记忆淘汰策略

参考 Agent 模式的时间衰减机制，增加主动淘汰：

1. **容量淘汰**：超过 `maxEntries` 时，删除最旧的同类型条目
2. **时间衰减**：超过 7 天的记忆标记为"可能已过时"
3. **过期清理**：超过 30 天且未被引用的记忆自动删除
4. **手动清除**：提供 API 手动删除特定记忆

### 4.4 并发写入安全

```go
var teamMemMu sync.Mutex  // 公共记忆池写入锁

func addTeamMemory(entries []agent.MemoryEntry) {
    teamMemMu.Lock()
    defer teamMemMu.Unlock()
    // ... 读取 → 去重 → 追加 → 写入
}
```

**设计决策**：与 Claude Code 一致，只在主线程（orchestration 结束后）写入记忆，Worker 不写入。消除并行写入风险。

---

## 第 5 部分：Claude Code 的设计权衡及对我们的启示

### 5.1 为什么 Coordinator 不含 memory？

Claude Code 的设计理由：

| 理由 | 是否适用于 chat_server | 我们的决策 |
|------|---------------------|-----------|
| Prompt Cache 效率 | ❌ 我们不使用 prompt cache | **加入记忆** |
| Token 预算紧张 | ⚠️ 部分适用 | **限制大小（1500字符）** |
| 职责分离（Leader 只编排） | ⚠️ 部分适用 | **Leader 需要项目背景来做更好的编排决策** |

**我们的结论**：Leader 应该有记忆。一个不知道项目用什么技术栈的 Leader，无法做出好的任务分派决策。

### 5.2 为什么 Worker 不触发记忆提取？

Claude Code 的设计理由：

| 理由 | 是否适用于 chat_server | 我们的决策 |
|------|---------------------|-----------|
| 避免竞争写入 | ✅ 完全适用 | **只在主线程提取** |
| Token 成本（fork 对话给摘要 Agent） | ✅ 适用 | **异步、限频** |
| 质量控制（主线程看到完整交互） | ✅ 适用 | **由 orchestration 结果提取** |

**我们的结论**：完全采纳。Worker 不触发记忆提取，由 `runOrchestration` 完成后统一提取。

### 5.3 为什么 Agent Memory 按类型隔离？

Claude Code 的设计理由：

| 理由 | 是否适用于 chat_server | 我们的决策 |
|------|---------------------|-----------|
| 角色特化 | ✅ 适用 | **P2 实现角色级记忆** |
| 避免噪声 | ✅ 适用 | **Worker 只看与自己相关的经验** |
| 项目事实需要共享 | ✅ 适用 | **公共记忆池承载 project 类记忆** |

**我们的结论**：两层结构 — 公共记忆池（project/reference）+ 角色记忆（角色经验）。

### 5.4 Scratchpad 是否需要？

Claude Code 的 Scratchpad 解决的是**会话内**跨 Worker 知识共享。chat_server 当前通过 Leader 的 `Synthesis` 职责来传递：

```
Worker A 输出 → Leader 理解合成 → 传递给 Worker B
```

**我们的结论**：暂不需要 Scratchpad。当前 Leader 合成机制 + `continue_worker` 已能满足会话内知识传递。如果后续发现 Leader 合成不够精确（丢失细节），再考虑引入。

---

## 第 6 部分：实施路线图

### Phase 0（P0）：公共记忆池 + Leader/Worker 注入 + 对话后提取

**预计工作量**：0.5-1 天

| 步骤 | 内容 | 文件 |
|------|------|------|
| 1 | 新建 `team/memory.go`：公共记忆池的 CRUD | 新文件 |
| 2 | `leaderSystemPrompt` 注入记忆 | team.go |
| 3 | `executeWorkerWithContext` 注入项目上下文 | team.go |
| 4 | `runOrchestration` 末尾触发记忆提取 | team.go |
| 5 | 编译验证 + 功能测试 | — |

### Phase 1（P1）：Agent 记忆桥接

**预计工作量**：0.5 天

| 步骤 | 内容 | 文件 |
|------|------|------|
| 1 | `loadAgentProjectMemories` 遍历 Agent 的 project 记忆 | team/memory.go |
| 2 | `mergeMemories` 合并去重 | team/memory.go |
| 3 | 集成测试 | — |

### Phase 2（P2）：角色级记忆

**预计工作量**：1 天

| 步骤 | 内容 | 文件 |
|------|------|------|
| 1 | Worker 记忆存储与读取 | team/memory.go |
| 2 | Worker prompt 注入角色经验 | team.go |
| 3 | orchestration 后提取角色记忆 | team/memory.go |
| 4 | 集成测试 | — |

### Phase 3（P3）：前端记忆管理

**预计工作量**：1 天

| 步骤 | 内容 | 文件 |
|------|------|------|
| 1 | Team Memory 查看/管理 API | team.go |
| 2 | 前端记忆面板 | web/index_html.go |
| 3 | 记忆导入/导出 | team.go |

---

## 附录 A：关键代码位置速查

### chat_server 现有代码

| 文件 | 作用 |
|------|------|
| `agent/agent.go` L40-46 | `MemoryEntry` 结构定义 |
| `agent/agent.go` L166-210 | `LoadMemory`、`SaveMemory`、`addMemory` |
| `agent/agent.go` L212-255 | `SystemPrompt` — 记忆注入 system prompt |
| `agent/agent.go` L279-360 | `ExtractMemory` — LLM 提取记忆 |
| `agent/agent.go` L572 | `go ExtractMemory()` — 异步触发点 |
| `team/team.go` L349-402 | `leaderSystemPrompt` — 需要修改 |
| `team/team.go` L741-760 | Worker system prompt 构建 — 需要修改 |
| `team/team.go` L444-536 | `runOrchestration` — 需要在末尾增加记忆提取 |

### Claude Code 参考代码

| 文件 | 作用 |
|------|------|
| `utils/systemPrompt.ts` L59-74 | Coordinator prompt 替换逻辑 |
| `memdir/memdir.ts` L448-472 | `loadMemoryPrompt()` |
| `memdir/teamMemPaths.ts` L84-86 | Team Memory 路径 |
| `tools/AgentTool/agentMemory.ts` | Agent Memory 加载 |
| `query/stopHooks.ts` L141-152 | 记忆提取触发条件 |
| `coordinator/coordinatorMode.ts` L104-106 | Scratchpad 注入 |

## 附录 B：Claude Code 的"公共记忆池"对照

| Claude Code 机制 | chat_server 对应方案 |
|-----------------|-------------------|
| CLAUDE.md（静态配置） | P0 公共记忆池中的 project 类记忆 |
| Auto Memory（自动积累） | P0 Team 对话后自动提取 |
| Team Memory（跨实例共享） | P0 `data/team_memory.json`（单实例，暂不需跨实例同步） |
| Agent Memory（按角色） | P2 `data/worker_memory/{role}.json` |
| Scratchpad（运行时） | 暂不需要，Leader 合成机制替代 |
| relevant_memories（语义预取） | 暂不实现，需向量化基础设施 |
| Session Memory（compact 摘要） | 暂不需要，已有 context bloat 方案 |
