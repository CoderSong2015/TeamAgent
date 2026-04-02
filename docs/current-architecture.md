# 当前架构分析（v5 — 编辑增强 + 安全加固 + Bug 修复后）

> 基于 `chat_server/` 源码的完整梳理，反映编辑工作流增强、per-session 状态修复后的最新状态。
>
> 更新日期：2026-04-02（v5）

---

## 1. 系统概览

chat_server 是一个基于 Go 标准库的轻量多 Agent 对话服务，提供两种独立的交互模式：

| 模式 | 入口 | 本质 |
|------|------|------|
| **Agent 模式** | `/api/agent/<id>/chat` | 单 Agent Agentic Loop + 工具调用（含子 Agent） + 自动记忆提取 |
| **Team 模式** | `/api/team/<id>/chat` | Leader 多轮编排 + Worker 带工具/子 Agent 的 Agentic Loop + 书记员记忆 + Pipeline |

两种模式共享 `llm` 包（LLM 调用层）和 `tools` 包（工具注册/执行/子 Agent/上下文管理层）。

---

## 2. 源码文件索引

```
chat_server/
├── main.go                        # HTTP 路由入口 + 工具/工作区初始化
├── llm/
│   └── client.go                  # LLM API 调用（支持 Function Calling）
├── agent/
│   └── agent.go                   # Agent CRUD、Agentic Loop 对话、记忆系统
├── team/
│   ├── team.go                    # Team CRUD、Leader 多轮编排、Worker Agentic Loop、Pipeline
│   └── memory.go                  # Team 公共记忆池 + Worker 角色经验 + HTTP API
├── tools/                         # 工具系统（9 个工具）
│   ├── tools.go                   # Tool 接口 + 全局注册表 + Execute 分发 + 分级截断 + 读写分流并行
│   ├── workspace.go               # 工作目录管理、路径安全、写入安全控制、命令安全控制
│   ├── context.go                 # CleanOldToolResults — 旧工具结果清理（含子 Agent 结果保护）
│   ├── read_cache.go              # ReadCache — per-session 上下文（读缓存 + Model + 子Agent降级 + HasRead/GetReadTime）
│   ├── read_file.go               # read_file 工具（200 行限制、offset/limit、ReadCache 去重）
│   ├── list_files.go              # list_files 工具（树形、深度限制、条目限制）
│   ├── grep_search.go             # grep_search 工具（正则搜索、100 行结果限制）
│   ├── glob_search.go             # glob_search 工具（文件名模式搜索、** 递归匹配、mtime 排序）
│   ├── edit_file.go               # edit_file 工具（先读后改 + mtime竞态 + 引号归一化 + diff输出 + 备份）
│   ├── write_file.go              # write_file 工具（创建/覆盖文件、自动建目录）
│   ├── run_command.go             # run_command 工具（Shell 命令、黑名单、超时 + 命令语义映射 + 大输出持久化）
│   └── subagent.go                # 子 Agent 引擎 + explore/delegate_task 工具注册 + 并行执行
├── web/
│   ├── template.go                # 模板工具
│   ├── index_html.go              # 嵌入式前端 HTML（含 SSE 事件处理）
│   └── docs.go                    # 设计文档浏览 API
└── data/
    ├── meta.json                  # Agent 注册表
    ├── teams_meta.json            # Team 注册表
    ├── team_memory.json           # Team 公共记忆池
    ├── worker_memory/             # Worker 角色经验记忆
    │   └── {role}.json            # 如 researcher.json
    ├── agents/<id>/
    │   ├── history.json           # 对话历史（只存 user + final assistant）
    │   └── memory.json            # 结构化长期记忆
    └── teams/<id>/
        ├── messages.json          # 协作消息日志
        ├── leader/history.json
        └── workers/{role}/
            └── session_history.json
```

---

## 3. LLM 调用层（`llm/client.go`）

### 3.1 核心数据结构

```go
type Message struct {
    Role       string     `json:"role"`
    Content    string     `json:"content"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
    ID       string       `json:"id"`
    Type     string       `json:"type"`
    Function FunctionCall `json:"function"`
}

type ToolDef struct {
    Type     string      `json:"type"`
    Function FunctionDef `json:"function"`
}
```

### 3.2 调用函数

| 函数 | 用途 | 调用方 |
|------|------|--------|
| `Call()` | 纯文本对话（不带工具） | Leader、记忆提取、书记员 |
| `CallWithRetry()` | 带重试的纯文本对话 | Leader、强制综合 |
| `CallWithTools()` | 带 Function Calling 的对话 | Agent/Worker/SubAgent Agentic Loop |
| `CallWithToolsRetry()` | 带重试的 Function Calling | Agent/Worker/SubAgent Agentic Loop |

所有函数共享 120s HTTP 超时和指数退避重试（429/500/502/503 + 网络错误）。

---

## 4. 工具系统（`tools/` 包）

### 4.1 架构

```
                        ┌───────────────────────────────────────────┐
                        │          tools.GetToolDefs()              │
                        │  → 9 个工具的 JSON Schema 发给 LLM API    │
                        └────────────────┬──────────────────────────┘
                                         │
                                         ▼
┌─ init() 自动注册 ──→ registry map[string]*Tool ──→ tools.Execute(name, args, cache)
│                                                           │
│  read_file     (只读, SubAgent可用, 3000)                  ├─ tool.Execute(args, cache)
│  list_files    (只读, SubAgent可用, 2000)                  ├─ 分级截断: MaxResultChars / DefaultMaxResultChars(1500)
│  grep_search   (只读, SubAgent可用, 2000)                  └─ 返回结果
│  glob_search   (只读, SubAgent可用, 2000)
│  edit_file     (写入, 先读后改+diff+备份)     ┌──────────────────────────────────┐
│  write_file    (写入)                        │  tools.ExecutePartitioned(calls) │
│  run_command   (写入, 默认禁用, 8000)         │  连续只读工具 → goroutine 并行    │
│  explore       (子Agent, 只读搜索)            │  写入工具 → 串行执行              │
│  delegate_task (子Agent, 只读多步)            └──────────────────────────────────┘
│
│  RegisterSubAgentTools() ← main.go 调用
└──────────────────────────
```

### 4.2 Tool 接口

```go
type Tool struct {
    Name            string
    Description     string
    Parameters      json.RawMessage
    Execute         func(args json.RawMessage, cache *ReadCache) (string, error)
    SubAgentAllowed bool   // explore/delegate_task 子 Agent 可调用此工具
    IsSubAgentTool  bool   // 此工具本身是子 Agent 启动器（explore, delegate_task）
    IsReadOnly      bool   // 只读工具可并行执行
    MaxResultChars  int    // >0: 截断到此长度; 0: 使用 DefaultMaxResultChars(1500)
}
```

### 4.3 已注册工具（9 个）

#### 只读工具

| 工具 | 文件 | 功能 | SubAgent 可用 | 安全限制 |
|------|------|------|:---:|---------|
| `read_file` | `read_file.go` | 读取文件（带行号），支持 offset/limit | ✅ | 路径白名单 + 100KB + **200 行限制**（无范围时报错引导） |
| `list_files` | `list_files.go` | 树形目录结构 | ✅ | 路径白名单 + 200 条目 + 6 层深度 + 跳过 .git 等 |
| `grep_search` | `grep_search.go` | 正则搜索文件内容（grep -rn -E） | ✅ | 路径白名单 + 100 行结果 + 跳过 .git 等 |
| `glob_search` | `glob_search.go` | 按文件名 glob 模式搜索（支持 `**`），结果按 mtime 排序 | ✅ | 路径白名单 + 100 个结果 + 跳过 .git 等 |

#### 写入工具

| 工具 | 文件 | 功能 | SubAgent 可用 | 安全限制 |
|------|------|------|:---:|---------|
| `edit_file` | `edit_file.go` | 精确字符串替换（16 步校验流程） | ❌ | 路径白名单 + 写入安全 + **先读后改(HasRead)** + **mtime竞态检测** + **引号归一化** + **删除尾部换行** + **行尾空白清理(非.md)** + **备份(/tmp/chat_server_backups/)** + **diff输出** + 修改后清除 ReadCache |
| `write_file` | `write_file.go` | 创建新文件或覆盖，自动创建父目录 | ❌ | 路径白名单 + 写入安全 + 200KB 大小限制 |
| `run_command` | `run_command.go` | 执行 Shell 命令 | ❌ | **默认禁用** + 黑名单拦截 + 超时(30s/120s) + 输出截断(8000字符) + **命令语义映射**(grep exit 1≠错误) + **大输出持久化**(/tmp/chat_server_outputs/) |

#### 子 Agent 工具

| 工具 | 文件 | 功能 | 安全限制 |
|------|------|------|---------|
| `explore` | `subagent.go` | 启动只读搜索子 Agent（独立上下文）| 3 轮上限 + 60s 超时 + 800 字符结果 + 只读工具集(4个) |
| `delegate_task` | `subagent.go` | 只读多步子 Agent（比 explore 超时更长） | 3 轮上限 + 120s 超时 + 1200 字符结果 + 只读工具集(4个) |

### 4.4 安全与控制

**路径安全**（`workspace.go`）：
```go
var WorkspacePath = "/root/stockAnalysis"  // 启动时通过 WORKSPACE 环境变量配置
const MaxFileSize = 100 * 1024             // 100KB — read_file 单文件字节限制
```

**写入安全**（`workspace.go`）：
```go
var WriteEnabled   = true                  // TOOL_WRITE_ENABLED=false 关闭
var WriteDenyPaths = []string{".git/", "go.sum"}  // "/" 结尾=目录前缀匹配; 否则精确文件名匹配
var MaxWriteSize   = 200 * 1024            // 200KB — write_file 内容限制
```

**命令安全**（`workspace.go` + `run_command.go`）：
```go
var CommandEnabled    = false               // TOOL_COMMAND_ENABLED=true 开启
var CommandTimeout    = 30                  // 默认超时秒数
var MaxCommandTimeout = 120                // 最大超时秒数
// 黑名单: rm -rf /, mkfs, dd if=, chmod -R 777 /, ...
```

**分级截断**（`tools.go`）：
```go
const DefaultMaxResultChars = 1500  // 工具未设置 MaxResultChars 时的兜底截断

// 各工具实际限制（MaxResultChars 字段）：
// read_file    → 3000（需要看更多代码）
// list_files   → 2000（目录结构较紧凑）
// grep_search  → 2000（搜索结果较短）
// glob_search  → 2000（文件列表）
// run_command  → 8000（命令输出空间更大）+ 超限时持久化到磁盘
// edit_file    → 0 → 默认 1500（实际返回 diff 摘要）
// write_file   → 0 → 默认 1500（实际返回一行摘要）
// explore      → 0 → 默认 1500（子 Agent 内部已截断到 800）
// delegate_task→ 0 → 默认 1500（子 Agent 内部已截断到 1200）
```

### 4.5 工具过滤函数

| 函数 | 返回的工具集 | 调用方 |
|------|-------------|--------|
| `GetToolDefs()` | 全部 9 个 | Agent handleChat、Worker workerAgenticLoop |
| `GetToolDefsNoSubAgent()` | 排除 explore/delegate_task（7 个） | 未使用（为未来子 Agent 内部准备） |
| `GetSubAgentAllowedToolDefs()` | 仅 SubAgentAllowed=true（4 个只读） | delegate_task 子 Agent |
| `GetReadOnlyToolDefs()` | 仅 IsReadOnly=true 且非子 Agent（4 个） | explore 子 Agent |

### 4.6 上下文管理机制

| 机制 | 文件 | 说明 |
|------|------|------|
| `ReadCache` | `read_cache.go` | **per-session 上下文**：文件读取去重(path+offset+limit+ModTime) + `Model`(子Agent模型) + `subAgentFails`(降级计数) + `HasRead()`(先读后改) + `GetReadTime()`(mtime竞态) + `Invalidate()`(edit/write后清除) |
| `CleanOldToolResults` | `context.go` | 保留最近 3 个 tool result，更早的替换为占位文本。跳过 `[SubAgent]` 前缀的结果 |
| `DefaultMaxResultChars` | `tools.go` | 单个工具结果默认 **1500** 字符截断（per-tool 可覆盖：read_file:3000, grep/list/glob:2000, run_command:8000） |
| `MaxReadLines` | `read_file.go` | 大文件（>200 行）无范围读取时报错引导用 offset/limit |

`ReadCache` 生命周期：
- Agent 模式：每次 `handleChat` 创建，请求结束释放
- Team 模式：每次 `workerAgenticLoop` 创建，Worker 执行结束释放
- 子 Agent：每次 `runSubAgent` 创建独立 ReadCache，子 Agent 结束释放

`CleanOldToolResults` 触发时机：
- Agent 模式：round >= 3 时触发
- Team 模式：round >= 2 时触发
- 子 Agent：round >= 1 时触发

### 4.7 子 Agent 引擎（`subagent.go`）

```
Worker 的 Agentic Loop
  │
  ├─ LLM 返回 tool_calls 中包含 explore / delegate_task
  │
  ├─ explore("查找所有配置文件")
  │   ├─ 创建独立 messages: [system(只读搜索专家) + user(task)]
  │   ├─ 独立 ReadCache
  │   ├─ 工具集: GetReadOnlyToolDefs()（read_file, list_files, grep_search, glob_search）
  │   ├─ 最多 3 轮 × 60s 超时
  │   ├─ 结果截断到 800 字符
  │   └─ 返回带 [SubAgent] 前缀的结果 → 不被 CleanOldToolResults 清理
  │
  ├─ delegate_task("分析并总结 API 设计模式")
  │   ├─ 同上但工具集: GetSubAgentAllowedToolDefs()（4 个只读工具）
  │   ├─ 最多 3 轮 × 120s 超时
  │   └─ 结果截断到 1200 字符
  │
  └─ 多个子 Agent 可并行（ExecuteSubAgentsParallel，最多 3 个 goroutine）
```

降级策略（per-session）：连续 3 次子 Agent 失败后，`cache.ShouldDegradeSubAgent()` 返回 true，explore/delegate_task 拒绝执行并提示直接使用基础工具。失败计数存储在 `ReadCache.subAgentFails`（atomic int32），仅影响当前 session，不跨 Worker/Agent 共享。子 Agent 模型从 `cache.GetModel()` 读取（不再使用全局变量）。

---

## 5. Agent 模式详解

### 5.1 Agentic Loop 对话流程

```
用户 POST /api/agent/<id>/chat { message: "..." }
  │
  ├─ 加载 history.json
  ├─ 构建 system prompt（工作区信息 + Agent 自定义提示词 + 记忆注入）
  ├─ 截断历史到最近 20 条（MaxHistory）
  ├─ 拼接 [system + history + user_message]
  │
  ├─ ═══ Agentic Loop（最多 10 轮）═══
  │  │
  │  ├─ round >= 3 时调用 CleanOldToolResults 清理旧结果
  │  ├─ llm.CallWithToolsRetry(messages, toolDefs, model, 1)
  │  │   toolDefs = GetToolDefs() → 全部 9 个工具
  │  │
  │  ├─ 无 tool_calls → finalReply = msg.Content → 退出循环
  │  │
  │  └─ 有 tool_calls →
  │     ├─ tools.ExecutePartitioned(tool_calls, cache)
  │     │   ├─ 连续只读工具（read_file, grep_search, glob_search, list_files）→ 并行
  │     │   ├─ 写入工具（edit_file, write_file, run_command）→ 串行
  │     │   └─ 子 Agent 工具（explore, delegate_task）→ 走 Execute → 内部独立循环
  │     ├─ 每个结果按 tool.MaxResultChars 或默认 1500 截断
  │     ├─ 追加 role:"tool" 消息到 messages
  │     └─ 继续下一轮
  │
  ├─ 若 10 轮后仍无最终回复 → CleanOldToolResults + 强制生成总结
  │  └─ llm.CallWithToolsRetry(msgs, nil, model, 1)  ← tools=nil 禁止再调用工具
  │
  ├─ 保存历史（user + finalReply，不保存工具中间过程）
  ├─ 首次对话自动设置标题（消息前 20 字）
  ├─ go ExtractMemory()  ← 异步后台提取记忆
  └─ 返回 { "reply": "...", "model": "..." }
```

### 5.2 关键常量

| 常量 | 值 | 说明 |
|------|-----|------|
| `MaxHistory` | 20 | 历史消息硬截断条数 |
| `MaxToolRounds` | 10 | Agentic Loop 最大轮数 |

### 5.3 系统提示词

```go
func workspaceSystemPrompt(agentID int) string {
    // "你是一个能够访问本地项目文件的 AI 助手。"
    // "项目根目录: {WorkspacePath}"
    // "当用户提到文件或代码时，请主动使用工具读取实际内容..."
    //
    // == 搜索工具 ==  (grep_search / glob_search / list_files 使用策略)
    // == 文件编辑工具 ==  (edit_file / write_file 使用说明)
    // == 子 Agent 工具 ==  (explore / delegate_task 使用决策)
    // == 代码修改行为准则 ==
    //   1. 先搜后读再改
    //   2. 工具优先于命令（read_file > cat, edit_file > sed, grep_search > grep）
    //   3. 修改后验证（go build / 测试）
    //   4. 如实报告（测试失败附输出）
    //   5. 失败后诊断（分析原因再换策略）
    //
    // + Agent 记忆（SystemPrompt(agentID)）
}
```

### 5.4 记忆系统

记忆分四种类型，通过独立 LLM 调用（`ExtractMemory`）异步提取：

| 类型 | 含义 | 示例 |
|------|------|------|
| `user` | 用户画像 | "用户叫小明，后端工程师" |
| `feedback` | 行为反馈 | "用户不喜欢 var，偏好 const/let" |
| `project` | 项目上下文 | "后端使用 Go + net/http，端口 8088" |
| `reference` | 外部引用 | "参考文档：https://..." |

特点：
- 按 `type + name` 去重
- 注入 system prompt 时按类型分组，超 2 天标注 ⚠️ 可能过时
- 异步提取不阻塞主对话流程

---

## 6. Team 模式详解

### 6.1 角色定义

| 角色 | 实例数 | 有工具 | 有记忆 | 说明 |
|------|--------|--------|--------|------|
| Leader | 1 | ❌（纯文本调用） | ❌ | 编排层，分析任务并分配 |
| Researcher（研究员） | 1 | ✅ Agentic Loop | ✅ 公共记忆 + 角色经验 | 调研分析，提供背景知识 |
| Coder（编码者） | 1 | ✅ Agentic Loop | ✅ 公共记忆 | 编写代码，修复问题 |
| Reviewer（审核者） | 1 | ✅ Agentic Loop | ✅ 公共记忆 | 审查质量，发现问题 |
| Architect（架构师） | 1 | ✅ Agentic Loop | ✅ 公共记忆 | 设计架构，编写设计文档 |
| Secretary（书记员） | 1 | ❌（纯文本调用） | ✅ 写入公共记忆池 | 分析对话提取持久知识 |

Worker 的 system prompt 中会注入：
- `TeamMemoryPromptForWorker()` → 公共记忆池中的项目背景
- `WorkerMemoryPrompt(role)` → 该角色的经验积累
- 工具使用指导（搜索策略、文件编辑、子 Agent 使用建议）

### 6.2 Leader 指令协议

Leader 严格输出 JSON，支持 5 种 action：

```json
{"action": "direct_reply", "content": "回答内容"}
{"action": "dispatch", "plan": "分析计划", "tasks": [...]}
{"action": "continue_worker", "tasks": [...]}
{"action": "verify", "tasks": [...]}
{"action": "synthesize", "content": "综合回答"}
```

`parseLeaderCommand` 使用 `json.NewDecoder` 解析首个 JSON 对象，有容错逻辑。

### 6.3 多轮编排流程

```
用户发送消息（SSE 流式响应）
  │
  ├─ [Leader Turn 1] 分析并输出指令 JSON
  │   ├─ direct_reply → 直接回复，结束
  │   └─ dispatch → dispatchAndExecute
  │       ├─ 检测 architect + coder → 设计驱动 Pipeline（executeArchPipeline）
  │       ├─ 检测 coder + reviewer → 小功能 Pipeline（executePipeline）
  │       └─ 其他 → 并行执行 + 可选 auto-review
  │
  ├─ Worker 执行（带工具 Agentic Loop）
  │   ├─ workerAgenticLoop() — 最多 4 轮工具调用
  │   ├─ 工具调用通过 SSE 实时推送（worker_tool_call / subagent_start/done）
  │   └─ 并行执行（executeTasksParallel）
  │
  ├─ [Leader Turn 2-N] 评估 Worker 结果，可选：
  │   ├─ continue_worker → 给 Worker 追加指令（保留上下文）
  │   ├─ verify → 启动验证
  │   ├─ dispatch → 分派新任务
  │   └─ synthesize → 综合回答，结束
  │
  ├─ （最多 maxLeaderTurns=6 轮，超限强制综合）
  │
  ├─ 书记员拦截：invokeSecretary 分析对话记录有价值知识
  ├─ 异步提取 Worker 角色经验：go ExtractWorkerMemories()
  └─ sse "done"
```

### 6.4 Pipeline 模式

**设计驱动 Pipeline（architect + coder）**：

```
Phase 1: Researcher + Others 并行 → handoff → Architect
Phase 2: Architect 设计 → 设计文档审核（reviewer 介入）→ handoff → Coder
Phase 3: Coder 实现（基于审核通过的设计）→ 代码审核 → handoff → Leader
```

**小功能 Pipeline（coder + reviewer）**：

```
Phase 1: Researcher + Others 并行 → handoff → Coder
Phase 2: Coder 实现 → handoff → Reviewer
Phase 3: Reviewer 审查
  ├─ LGTM → handoff → Leader，结束
  └─ 需改进 → Coder 修改 → Reviewer 重审（最多 maxRevisionRounds 轮）
```

每个 Worker 完成时发送交接消息（`sendHandoff`）：100 字内摘要 + @下一流程。

### 6.5 Worker Agentic Loop

```
workerAgenticLoop(msgs, model, worker, sse)
  │
  ├─ toolDefs = GetToolDefs()  ← 全部 9 个工具
  ├─ cache = NewReadCache()   ← cache.Model = model（per-session 传递）
  │
  ├─ ═══ Worker Loop（最多 4 轮）═══
  │  │
  │  ├─ round >= 2 → CleanOldToolResults（保留最近 3 个，跳过 [SubAgent] 结果）
  │  ├─ llm.CallWithToolsRetry(msgs, toolDefs, model, 1)
  │  │
  │  ├─ 无 tool_calls → 返回 msg.Content
  │  │
  │  └─ 有 tool_calls → 三类分流：
  │     │
  │     ├─ ① 子 Agent 工具（explore / delegate_task，且 subAgentCount < 10）
  │     │   ├─ SSE: subagent_start（每个子 Agent 一条）
  │     │   ├─ ExecuteSubAgentsParallel()
  │     │   │   ├─ semaphore 限制最多 3 个并行 goroutine
  │     │   │   ├─ 每个子 Agent 有独立上下文 + 独立 ReadCache + 独立 agentic loop
  │     │   │   └─ 部分失败不影响其他子 Agent
  │     │   ├─ SSE: subagent_done / subagent_error
  │     │   └─ 结果带 [SubAgent] 前缀 → CleanOldToolResults 跳过
  │     │
  │     └─ ② 普通工具（read_file, edit_file, run_command...）
  │         ├─ SSE: worker_tool_call（每个工具一条）
  │         ├─ ExecutePartitioned(normalCalls, cache)
  │         │   ├─ 连续只读工具 → 并行（goroutine + WaitGroup）
  │         │   └─ 写入工具 → 串行
  │         └─ 追加 role:"tool" 消息到 msgs
  │
  ├─ 4 轮未结束 → CleanOldToolResults → 强制总结（tools=nil）
  └─ 返回结果给 Leader / 进入审核流程
```

Worker 系统提示词包含工具使用指导：
- 搜索策略：知道文件名 → glob_search；知道代码内容 → grep_search；了解目录结构 → list_files
- 文件编辑：edit_file 精确替换（先 read_file）；write_file 创建新文件
- 子 Agent：能用 1 次 grep/read 解决的直接用工具；需要 3+ 次搜索的用 explore；可分解为多个独立子问题的用多个 explore 并行
- **代码修改行为准则**（5 条）：先搜后读再改、工具优先于命令、修改后验证、如实报告、失败后诊断

Worker `continue` 模式下，保留历史但对旧 tool result 压缩（超过 300 rune 截断）。

### 6.6 SSE 事件流

| 事件 | 含义 | 触发时机 |
|------|------|---------|
| `user_message` | 用户消息 | 收到请求时 |
| `leader_start` | Leader 开始分析 | Leader LLM 调用前（含 turn 编号） |
| `leader_plan` | Leader 计划 | dispatch 且有 plan 时 |
| `pipeline_mode` | 启用 Pipeline | 检测到 architect+coder 或 coder+reviewer 时 |
| `phase_start` | Pipeline 阶段开始 | 各 Pipeline 阶段切换时 |
| `task_dispatch` | 任务分派 | Leader 分配任务给 Worker |
| `worker_start` | Worker 开始 | Worker 执行前 |
| `worker_tool_call` | Worker 工具调用 | Worker 调用普通工具时 |
| `subagent_start` | 子 Agent 启动 | Worker 调用 explore/delegate_task 时 |
| `subagent_done` | 子 Agent 完成 | 子 Agent 成功返回结果 |
| `subagent_error` | 子 Agent 失败 | 子 Agent 执行出错/超时 |
| `worker_done` | Worker 完成 | Worker 返回结果 |
| `worker_error` | Worker 失败 | Worker 调用失败/超时 |
| `worker_heartbeat` | Worker 心跳 | 每 15 秒，显示已执行时间 |
| `worker_continue` | Worker 继续执行 | continue_worker 时 |
| `worker_handoff` | Worker 交接 | Pipeline 中一个 Worker 完成传给下一个 |
| `verify_start` | 验证开始 | Leader 发出 verify 指令时 |
| `revision_start` | 修订开始 | Reviewer 认为需修改 |
| `review_complete` | 审核完成 | 修订循环结束 |
| `max_turns_reached` | Leader 达到最大轮数 | 强制综合前 |
| `leader_synthesize` | Leader 综合中 | 强制综合阶段开始 |
| `secretary_start` | 书记员开始 | 书记员分析对话 |
| `secretary_done` | 书记员完成 | 书记员记录结果（含 count） |
| `final_reply` | 最终回复 | 综合完成 |
| `error` | 错误 | 任何阶段出错 |
| `done` | 流程结束 | 全部完成 |

### 6.7 关键常量

| 常量 | 值 | 说明 |
|------|-----|------|
| `workerTimeout` | 600s | Worker 整体执行超时（安全网） |
| `maxWorkerResult` | 3000 rune | Worker 输出截断阈值 |
| `maxWorkerToolRounds` | 4 | Worker Agentic Loop 最大轮数 |
| `maxRevisionRounds` | 2 | Coder↔Reviewer 最大修订轮数 |
| `maxLeaderTurns` | 6 | Leader 多轮编排最大轮数 |
| `maxWorkerHistory` | 8 | Worker continue 模式下历史保留条数 |
| `ExploreMaxRounds` | 3 | explore 子 Agent 最大轮数 |
| `ExploreTimeout` | 60s | explore 子 Agent 超时 |
| `ExploreMaxResult` | 800 字符 | explore 结果截断 |
| `DelegateMaxRounds` | 3 | delegate_task 子 Agent 最大轮数 |
| `DelegateTimeout` | 120s | delegate_task 子 Agent 超时 |
| `DelegateMaxResult` | 1200 字符 | delegate_task 结果截断 |
| `MaxParallelAgents` | 3 | 同时运行子 Agent 数上限 |
| `MaxSubAgentsPerRun` | 10 | 单次 Worker 执行子 Agent 总数上限 |

### 6.8 Team 记忆系统（`team/memory.go`）

Team 模式拥有独立的两层记忆体系：

**公共记忆池**（`data/team_memory.json`）：
- 由书记员（Secretary）在每次对话结束时写入
- 所有 Worker 的 system prompt 中通过 `TeamMemoryPromptForWorker()` 注入
- 上限 30 条，超限按时间淘汰
- 30 天过期自动清理
- 记忆类型同 Agent：project / user / feedback / reference

**Worker 角色经验**（`data/worker_memory/{role}.json`）：
- 对话结束后由 `ExtractWorkerMemories()` 异步提取
- 各角色独立存储（如 `researcher.json`）
- 通过 `WorkerMemoryPrompt(role)` 注入到对应 Worker
- 上限每角色 15 条

**HTTP API**：
- `GET /api/team/<id>/memory` — 查看公共记忆
- `DELETE /api/team/<id>/memory` — 清空公共记忆

---

## 7. 通信机制

当前"通信"本质上是**函数调用内的数据传递**，不是真正的消息系统：

| 通信路径 | 实现方式 |
|----------|---------|
| Leader → Worker | 字符串拼接到 LLM user message |
| Worker → Leader | goroutine 返回值（channel + WaitGroup） |
| Researcher → Coder/Architect | Pipeline 中结果字符串追加到下游 Worker 的 task |
| Coder ↔ Reviewer | Pipeline 循环内字符串传递 |
| Worker → 子 Agent | 独立 messages 构建（完全隔离上下文） |
| 子 Agent → Worker | 返回摘要字符串（截断后带 [SubAgent] 前缀） |
| Worker → UI | SSE 事件推送（工具调用 + 子 Agent 进度 + 心跳） |
| Secretary → 公共记忆池 | `addTeamMemory()` 直接写文件 |
| Worker 经验 → 记忆文件 | `ExtractWorkerMemories()` 异步写文件 |
| 公共记忆 → Worker prompt | `TeamMemoryPromptForWorker()` 读文件注入 |
| 角色经验 → Worker prompt | `WorkerMemoryPrompt(role)` 读文件注入 |
| 全部 → 持久化 | `appendMessage` → messages.json |

---

## 8. 架构关系图

```
                    ┌──────────────┐
                    │   main.go    │
                    │  路由 + 初始化│
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
        ┌─────▼─────┐ ┌───▼────────┐ ┌─▼──────────────────────┐
        │  agent/    │ │   team/    │ │       tools/            │
        │  agent.go  │ │  team.go   │ │  tools.go              │
        │            │ │  memory.go │ │  ├ Execute + Partitioned│
        │ Agentic    │ │            │ │  ├ context.go (Clean)   │
        │ Loop ──────┤ │ Leader Loop│ │  ├ read_cache.go        │
        │ + Memory   │ │ Worker Loop│ │  ├ workspace.go (安全)  │
        │            │ │ Pipeline   │ │  ├ subagent.go (子Agent)│
        │            │ │ Secretary  │ │  ├── 只读 ─────────────│
        │            │ │ SubAgent   │ │  │  read_file           │
        │            │ │ Dispatch   │ │  │  list_files          │
        └─────┬──────┘ └───┬───────┘ │  │  grep_search         │
              │            │         │  │  glob_search          │
              │   ┌────────▼─────┐   │  ├── 写入 ─────────────│
              └──►│    llm/      │◄──┤  │  edit_file            │
                  │CallWithTools │   │  │  write_file           │
                  │CallWithRetry │   │  │  run_command          │
                  │Call          │   │  ├── 子Agent ───────────│
                  └──────────────┘   │  │  explore              │
                                     │  │  delegate_task        │
                  ┌──────────────┐   │  └──────────────────────│
                  │     web/     │   └─────────────────────────┘
                  │ index_html.go│
                  │ docs.go      │
                  │ template.go  │
                  └──────────────┘
```

---

## 9. 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WORKSPACE` | `/root/stockAnalysis` | 工具系统工作目录 |
| `LLM_API_URL` | （必填，无默认值） | LLM API 地址（OpenAI 兼容格式） |
| `LLM_API_KEY` | — | LLM API 密钥 |
| `TOOL_WRITE_ENABLED` | `true` | 设为 `false` 禁用 edit_file/write_file |
| `TOOL_COMMAND_ENABLED` | `false` | 设为 `true` 启用 run_command |

---

## 10. 已知问题与改进方向

### 10.1 已完成的治理措施

| 措施 | 文件 | 效果 |
|------|------|------|
| read_file 200 行限制 + 报错引导 | `read_file.go` | 大文件不再全量进入上下文 |
| ReadCache 读取去重 + Invalidate | `read_cache.go` | 同文件重复读取仅返回 stub；edit/write 后清除缓存 |
| CleanOldToolResults 旧结果清理 | `context.go` | 保留最近 3 个，其余替换为占位；[SubAgent] 结果豁免 |
| 分级截断 DefaultMaxResultChars=1500 | `tools.go` | per-tool 可覆盖（read:3000, grep/list/glob:2000, cmd:8000） |
| ExecutePartitioned 读写分流 | `tools.go` | 只读工具并行，写工具串行 |
| 子 Agent 上下文隔离 | `subagent.go` | explore/delegate_task 有独立上下文，不膨胀父 Worker |
| 子 Agent 结果保护 | `context.go` | [SubAgent] 前缀不被 CleanOldToolResults 清理 |
| 子 Agent per-session 降级 | `read_cache.go` | 连续 3 次失败后降级（计数器 per-session，不跨 Worker） |
| 子 Agent model per-session | `read_cache.go` | 从 ReadCache.Model 读取，不再使用全局变量 |
| 写入安全控制 | `workspace.go` | WriteDenyPaths 精确匹配：`/`结尾=目录前缀，否则=精确文件名 |
| 命令执行安全 | `run_command.go` | 默认禁用 + 黑名单 + 超时 + 命令语义映射 |
| edit_file 编辑增强 | `edit_file.go` | 先读后改(HasRead) + mtime竞态 + 引号归一化 + 尾部换行 + 行尾空白 + 备份 + diff输出 |
| run_command 输出增强 | `run_command.go` | 命令语义(grep exit 1≠错误) + 大输出持久化(/tmp) |
| 代码修改行为准则 | `agent.go` `team.go` | Agent/Worker prompt 注入 5 条行为准则 |

### 10.2 待改进

| 维度 | 现状 | 改进方向 |
|------|------|---------|
| 前端工具展示 | Agent 模式无工具过程展示 | SSE 流式推送工具过程（复用 Team 的 sseWriter） |
| 角色工具权限 | 所有 Worker 获得全部 9 个工具 | 按角色过滤：研究员不需要 edit/write/run |
| LLM 流式响应 | 不支持 | 减少用户等待感 |
| Worker 间通信 | 无（仅 Pipeline 串联） | 共享 Scratchpad / TaskList |
| 记忆淘汰 | Agent 记忆只增不减；Team 有 30 天过期 | Agent 引入衰减/淘汰机制 |

---

## 11. 版本变更摘要

| 变更项 | v1（初始） | v2（工具系统） | v3（上下文治理） | v4（工具扩展+子Agent） | v5（当前） |
|--------|-----------|---------------|-----------------|-----------|-----------|
| LLM 调用 | 只有 `Call()` | +`CallWithTools/Retry` | 不变 | 不变 | 不变 |
| Agent 对话 | 单次 LLM 调用 | Agentic Loop（10 轮） | +CleanOld(≥3轮) +ReadCache | +ExecutePartitioned | +完整工具/子Agent prompt |
| Worker 执行 | 单次 LLM 调用 | Agentic Loop（6 轮） | **4 轮** +CleanOld(≥2轮) +ReadCache | +子 Agent 分流 +ExecutePartitioned | +代码修改行为准则 |
| 工具数量 | 0 | 3（read/list/grep） | 3 | **9**（+glob/edit/write/run/explore/delegate） | 9（edit/run 增强） |
| 工具结果限制 | — | MaxToolResultChars=4000 | **1500** | 分级截断（默认 3000） | **默认 1500** + per-tool(read:3000, grep/list/glob:2000, cmd:8000) |
| 工具执行 | — | 串行逐个 | 串行逐个 | **读写分流**（只读并行，写串行） | 不变 |
| 子 Agent | — | — | — | **explore + delegate_task**（独立上下文，并行执行） | **per-session Model/降级**（移除全局变量竞态） |
| edit_file | — | — | — | 精确替换 + 唯一性检查 | +**先读后改 + mtime + 引号归一化 + 尾部换行 + 备份 + diff** |
| run_command | — | — | — | 默认禁用 + 黑名单 + 超时 | +**命令语义映射 + 大输出持久化** |
| ReadCache | — | — | 读缓存+去重 | +Invalidate | **per-session 上下文**（+Model, +subAgentFails, +HasRead, +GetReadTime） |
| Worker 上下文 | 无状态 | SessionContext 跨 turn | +历史压缩(>300rune) | +子 Agent 结果保护 | 不变 |
| Team 角色 | 3 个 | 3 个 | **5 个** (+architect, secretary) | 不变 | 不变 |
| Pipeline | 无 | coder→reviewer | +architect→coder 设计驱动 | +handoff 交接消息 | 不变 |
| Team 记忆 | 无 | 无 | **公共记忆池 + 角色经验** | 不变 | 不变 |
| SSE 事件 | 基础 | +worker_tool_call | +phase/handoff/heartbeat/secretary | +subagent_start/done/error | 不变 |
