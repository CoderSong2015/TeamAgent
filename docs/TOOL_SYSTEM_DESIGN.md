# Agent 工具系统设计文档

> 基于 Claude Code 工具架构分析 + 当前 chat_server 现状的差距分析与实现方案
> 
> **实施状态**：P0 核心功能已完成（2026-03），上下文优化方案待实施
> 
> 相关文档：
> - [current-architecture.md](./current-architecture.md) — 实施后的架构现状
> - [CONTEXT_BLOAT_DESIGN.md](./CONTEXT_BLOAT_DESIGN.md) — 上下文膨胀治理专项设计

---

## 一、Claude Code 工具架构核心要点

### 1.1 三种让 Agent 访问本地资源的方案

| 方案 | 描述 | 复杂度 | 效果 |
|------|------|--------|------|
| **注入文件内容** | 前端读文件，拼入用户消息发给 LLM | 最低 | 被动，依赖人工选文件 |
| **Function Calling** | LLM 返回工具调用，服务端执行后回传结果 | 中等 | **主动**，LLM 自主决定读什么 |
| **完整工具系统** | 多工具协作 + 权限 + 编排 + 并发 | 高 | 生产级 Agent |

Claude Code 实现的是第三种。**我们的目标是先实现第二种（Function Calling），再逐步演进到第三种。**

### 1.2 核心架构：Agentic Loop

```
用户输入
  │
  ▼
┌────────────────────────────────────────────┐
│  while (true) {                            │
│                                            │
│    1. 构建请求                              │
│       system: [系统提示词]                  │
│       tools:  [工具的 JSON Schema]          │
│       messages: [对话历史]                  │
│                                            │
│    2. 调用 LLM API                          │
│                                            │
│    3. 解析响应                              │
│       ├─ 只有 text → 返回给用户，结束       │
│       └─ 有 tool_calls → 继续              │
│                                            │
│    4. 本地执行工具（读文件、搜索代码等）      │
│                                            │
│    5. 构建 tool result 消息                 │
│       拼入 messages → 继续下一轮循环        │
│  }                                         │
└────────────────────────────────────────────┘
```

**关键点**：LLM 不是一次调用就结束，而是循环调用。每次 LLM 想用工具，服务端就执行工具、把结果喂回去，直到 LLM 认为信息足够，输出最终文本回答。

### 1.3 Tool Use 协议（OpenAI 兼容格式）

**请求**：在 `chat/completions` 请求中附带 `tools` 数组

```json
{
  "model": "gpt-5.4",
  "messages": [...],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "read_file",
        "description": "读取指定路径的文件内容，返回带行号的文本",
        "parameters": {
          "type": "object",
          "properties": {
            "path": { "type": "string", "description": "文件路径" }
          },
          "required": ["path"]
        }
      }
    }
  ]
}
```

**响应**：LLM 可能返回 `tool_calls`

```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        {
          "id": "call_abc123",
          "type": "function",
          "function": {
            "name": "read_file",
            "arguments": "{\"path\": \"main.py\"}"
          }
        }
      ]
    },
    "finish_reason": "tool_calls"
  }]
}
```

**回传工具结果**：用 `role: "tool"` 消息

```json
{
  "role": "tool",
  "tool_call_id": "call_abc123",
  "content": "1|import akshare as ak\n2|import pandas as pd\n..."
}
```

### 1.4 工具接口设计要素

Claude Code 每个工具包含：

| 字段 | 作用 |
|------|------|
| `name` | 工具唯一标识 |
| `description` / `prompt` | 给 LLM 看的描述（引导模型正确使用） |
| `inputSchema` | 参数的 JSON Schema（API 层验证） |
| `call()` | 实际执行函数 |
| `isConcurrencySafe` | 是否可并发（读操作=true） |
| `isReadOnly` | 是否只读 |
| `checkPermissions` | 权限检查 |

---

## 二、当前 chat_server 架构现状

### 2.1 整体结构

```
chat_server/
├── main.go              # HTTP 服务入口，路由注册
├── llm/
│   └── client.go        # LLM API 调用（纯 chat completions）
├── agent/
│   └── agent.go         # 单 Agent：记忆、历史、对话
├── team/
│   └── team.go          # 多 Agent 编排（Leader-Worker）
└── web/
    ├── index_html.go    # 前端 HTML/CSS/JS（内嵌单文件）
    └── template.go      # 模板渲染
```

### 2.2 当前 LLM 调用方式

`llm/client.go` 的核心数据结构：

```go
// 请求 — 只有 model 和 messages
type request struct {
    Model    string    `json:"model"`
    Messages []Message `json:"messages"`
}

// 消息 — 只有 role 和 content
type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

// 响应 — 只读 content 文本
type response struct {
    Choices []struct {
        Message struct {
            Content string `json:"content"`
        } `json:"message"`
    } `json:"choices"`
}
```

### 2.3 当前对话流程

`agent/agent.go` 的 `handleChat`：

```
POST /api/agent/{id}/chat  →  { "message": "分析下 main.py" }
  │
  ├── 加载记忆 → 拼入 system 消息
  ├── 加载最近 20 条历史
  ├── 拼接当前 user 消息
  ├── llm.Call(messages, model)     ← 单次调用，无工具
  ├── 保存历史（user + assistant）
  ├── go ExtractMemory(...)         ← 后台提取记忆
  └── 返回 { "reply": "..." }
```

**问题**：当用户说"分析下 main.py"，Agent 无法读取文件，只能凭空回答或要求用户手动粘贴代码。

### 2.4 Team 模式的"伪工具调用"

Team 的 Leader-Worker 模式通过 **prompt 约束 + Go 侧 JSON 解析** 实现类似工具调用的效果：

```
Leader LLM → 输出 JSON: {"action":"dispatch","workers":[...]}
  → Go 解析 → 分发给 Worker（本质是再调一次 LLM）
  → Worker 结果拼回 Leader 的 messages
  → Leader 继续推理
```

但这不是 LLM 协议层的 Function Calling，Worker 也不能执行实际操作（读文件、运行代码等）。

---

## 三、差距分析：缺失清单

### 3.1 按层分解

```
┌─────────────────────────────────────────────────────────┐
│                    前端 UI 层                            │
│  ❌ 工具调用过程展示（正在读取文件...）                    │
│  ❌ 工具结果折叠/展开                                    │
│  ❌ 流式响应（当前是等全部完成才返回）                     │
├─────────────────────────────────────────────────────────┤
│                    HTTP API 层                           │
│  ❌ 响应需要支持多轮工具调用（SSE 或轮询）                │
├─────────────────────────────────────────────────────────┤
│                    Agent 对话层                          │
│  ❌ Agentic Loop（工具调用循环）                          │
│  ❌ 工具注册表                                           │
│  ❌ 工具执行分发器                                       │
│  ❌ 工作目录 / 项目上下文配置                             │
├─────────────────────────────────────────────────────────┤
│                    LLM 调用层                            │
│  ❌ 请求体 tools 字段                                    │
│  ❌ 响应体 tool_calls 解析                               │
│  ❌ Message 结构扩展（tool_calls / tool_call_id / role）  │
├─────────────────────────────────────────────────────────┤
│                    工具实现层                             │
│  ❌ read_file — 读取文件内容                             │
│  ❌ list_files — 列出目录/文件                           │
│  ❌ grep_search — 代码搜索                               │
│  ❌ write_file / edit_file — 文件写入/编辑（可选）        │
│  ❌ run_command — Shell 命令执行（可选）                  │
├─────────────────────────────────────────────────────────┤
│                    安全层                                │
│  ❌ 路径白名单（限制在工作目录下）                        │
│  ❌ 文件大小限制（防止 token 爆炸）                      │
│  ❌ 命令黑名单（如果实现 Bash 工具）                     │
│  ❌ 最大循环次数限制（防止无限循环）                      │
└─────────────────────────────────────────────────────────┘
```

### 3.2 核心缺失项优先级与实施状态

| # | 缺失项 | 重要程度 | 阶段 | 状态 |
|---|--------|---------|------|------|
| 1 | `llm.Message` 扩展 | 必须 | P0 | ✅ 已完成 |
| 2 | `llm.Call` 支持 tools 请求 + tool_calls 响应 | 必须 | P0 | ✅ 已完成 |
| 3 | 工具定义结构 + 注册表 | 必须 | P0 | ✅ 已完成 |
| 4 | `read_file` 工具实现 | 必须 | P0 | ✅ 已完成 |
| 5 | `list_files` 工具实现 | 必须 | P0 | ✅ 已完成 |
| 6 | Agent Agentic Loop | 必须 | P0 | ✅ 已完成 |
| 7 | 工作目录配置 + 路径安全 | 必须 | P0 | ✅ 已完成 |
| 8 | `grep_search` 工具实现 | 重要 | P0 | ✅ 已完成（提前） |
| 9 | Team Worker Agentic Loop | 重要 | P0 | ✅ 已完成（新增） |
| 10 | Team SSE 工具调用事件 | 重要 | P0 | ✅ 已完成（新增） |
| 11 | **上下文膨胀治理** | **高** | **P0+** | **⏳ 待实施** |
| 12 | 前端工具调用展示（Agent 模式） | 重要 | P1 | ⏳ 待实施 |
| 13 | `edit_file` / `write_file` 工具 | 可选 | P2 | — |
| 14 | `run_command` Shell 工具 | 可选 | P2 | — |
| 15 | 完整权限系统 | 可选 | P3 | — |

---

## 四、实现方案（P0 阶段详细设计）

### 4.1 扩展 `llm.Message`

**文件**：`llm/client.go`

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

type FunctionCall struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"`
}
```

**影响**：`Message` 被 agent 和 team 两个包引用，扩展字段带 `omitempty` 不影响现有序列化（历史 JSON 中没有这些字段，反序列化时为零值）。

### 4.2 扩展 LLM 请求/响应

**文件**：`llm/client.go`

```go
// 工具 Schema 定义（发给 API）
type ToolDef struct {
    Type     string       `json:"type"`
    Function FunctionDef  `json:"function"`
}

type FunctionDef struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Parameters  json.RawMessage `json:"parameters"`
}

// 扩展请求体
type request struct {
    Model    string    `json:"model"`
    Messages []Message `json:"messages"`
    Tools    []ToolDef `json:"tools,omitempty"`
}

// 扩展响应体
type response struct {
    Choices []struct {
        Message struct {
            Role      string     `json:"role"`
            Content   string     `json:"content"`
            ToolCalls []ToolCall `json:"tool_calls,omitempty"`
        } `json:"message"`
        FinishReason string `json:"finish_reason"`
    } `json:"choices"`
}

// 新增：带工具的调用（返回完整 Message 而非纯文本）
func CallWithTools(messages []Message, tools []ToolDef, model string) (*Message, error) {
    payload := request{Model: model, Messages: messages, Tools: tools}
    // ... HTTP 调用逻辑同 Call() ...
    // 返回包含 Content 和/或 ToolCalls 的完整 Message
}
```

**兼容性**：保留原 `Call()` 函数不变（`tools` 字段 `omitempty` 不发送），Team 模式和记忆提取仍用 `Call()`。

### 4.3 工具定义与注册

**新文件**：`tools/tools.go`

```go
package tools

import "encoding/json"

// Tool 接口：每个工具必须实现
type Tool struct {
    Name        string                           // 工具名称
    Description string                           // 给 LLM 看的描述
    Parameters  json.RawMessage                  // JSON Schema
    Execute     func(args json.RawMessage) (string, error)  // 执行函数
}

// Registry 全局工具注册表
var Registry = map[string]*Tool{}

func Register(t *Tool) {
    Registry[t.Name] = t
}

// ToToolDefs 转为 LLM API 格式
func ToToolDefs() []llm.ToolDef {
    var defs []llm.ToolDef
    for _, t := range Registry {
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
```

### 4.4 核心工具实现

#### read_file

```go
// tools/read_file.go
func init() {
    Register(&Tool{
        Name:        "read_file",
        Description: `读取指定路径的文件内容。返回带行号的文本。
使用场景：当你需要查看某个文件的代码或内容时。
限制：单个文件最大 100KB。路径相对于项目根目录。`,
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "path": {
                    "type": "string",
                    "description": "文件路径，相对于项目根目录"
                },
                "offset": {
                    "type": "integer",
                    "description": "起始行号（可选，从 1 开始）"
                },
                "limit": {
                    "type": "integer",
                    "description": "读取行数（可选，默认全部）"
                }
            },
            "required": ["path"]
        }`),
        Execute: executeReadFile,
    })
}

func executeReadFile(args json.RawMessage) (string, error) {
    var input struct {
        Path   string `json:"path"`
        Offset int    `json:"offset"`
        Limit  int    `json:"limit"`
    }
    json.Unmarshal(args, &input)

    // 1. 路径安全检查（必须在 workspace 下）
    absPath := resolvePath(input.Path)
    if !isUnderWorkspace(absPath) {
        return "", fmt.Errorf("路径不在允许范围内: %s", input.Path)
    }

    // 2. 文件大小检查
    info, err := os.Stat(absPath)
    if err != nil {
        return "", fmt.Errorf("文件不存在: %s", input.Path)
    }
    if info.Size() > MaxFileSize {
        return "", fmt.Errorf("文件过大 (%d bytes)，请用 offset + limit 分段读取", info.Size())
    }

    // 3. 读取并加行号
    content, _ := os.ReadFile(absPath)
    lines := strings.Split(string(content), "\n")
    // 应用 offset/limit ...
    var sb strings.Builder
    for i, line := range lines {
        fmt.Fprintf(&sb, "%4d|%s\n", i+1, line)
    }
    return sb.String(), nil
}
```

#### list_files

```go
// tools/list_files.go
func init() {
    Register(&Tool{
        Name:        "list_files",
        Description: `列出指定目录下的文件和子目录。
使用场景：当你需要了解项目结构或查找文件时。
返回格式为树形目录列表，最多显示 200 个条目。`,
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "path": {
                    "type": "string",
                    "description": "目录路径，相对于项目根目录，默认为根目录"
                },
                "max_depth": {
                    "type": "integer",
                    "description": "最大递归深度（默认 3）"
                }
            }
        }`),
        Execute: executeListFiles,
    })
}
```

#### grep_search（P1 阶段）

```go
// tools/grep_search.go
func init() {
    Register(&Tool{
        Name:        "grep_search",
        Description: `在项目文件中搜索匹配正则表达式的内容。
使用场景：当你需要查找某个函数定义、变量引用或特定代码模式时。
底层使用 grep/ripgrep，支持正则表达式。`,
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "pattern": {
                    "type": "string",
                    "description": "搜索的正则表达式"
                },
                "path": {
                    "type": "string",
                    "description": "搜索目录，默认项目根目录"
                },
                "include": {
                    "type": "string",
                    "description": "文件过滤（如 *.go, *.py）"
                }
            },
            "required": ["pattern"]
        }`),
        Execute: executeGrepSearch,
    })
}
```

### 4.5 Agentic Loop 实现

**文件**：`agent/agent.go` — 改造 `handleChat`

```go
const MaxToolRounds = 10  // 最大工具循环次数

func handleChat(w http.ResponseWriter, r *http.Request, id int) {
    // ... 解析请求、加载历史（不变）...

    // 获取工具定义
    toolDefs := tools.ToToolDefs()

    // ═══ Agentic Loop ═══
    for round := 0; round < MaxToolRounds; round++ {

        // 调用 LLM（带工具）
        reply, err := llm.CallWithTools(messages, toolDefs, model)
        if err != nil {
            // 错误处理...
            return
        }

        // 追加 assistant 消息
        messages = append(messages, *reply)

        // 如果没有 tool_calls → LLM 认为信息足够，返回最终回复
        if len(reply.ToolCalls) == 0 {
            // 保存历史、返回 reply.Content
            break
        }

        // 有 tool_calls → 逐个执行工具
        for _, tc := range reply.ToolCalls {
            result, err := tools.Execute(tc.Function.Name, []byte(tc.Function.Arguments))
            content := result
            if err != nil {
                content = fmt.Sprintf("工具执行错误: %v", err)
            }

            // 构建 tool result 消息
            messages = append(messages, llm.Message{
                Role:       "tool",
                ToolCallID: tc.ID,
                Content:    content,
            })
        }
        // 继续下一轮 → LLM 看到工具结果后决定下一步
    }

    // 保存历史、返回最终回复
}
```

**核心变化**：`handleChat` 从"单次调用"变成"循环调用"。

### 4.6 工作目录与路径安全

**新文件**：`tools/workspace.go`

```go
package tools

var WorkspacePath string  // 项目根目录，启动时配置

const MaxFileSize = 100 * 1024  // 100KB 单文件限制

func resolvePath(rel string) string {
    if filepath.IsAbs(rel) {
        return filepath.Clean(rel)
    }
    return filepath.Clean(filepath.Join(WorkspacePath, rel))
}

func isUnderWorkspace(abs string) bool {
    return strings.HasPrefix(abs, WorkspacePath)
}
```

**启动配置**（`main.go`）：

```go
func main() {
    tools.WorkspacePath = getEnvDefault("WORKSPACE", "/root/stockAnalysis")
    tools.Init()  // 注册所有工具
    // ...
}
```

### 4.7 系统提示词增强

在 Agent 的 system prompt 中注入工作区上下文：

```go
func SystemPromptWithWorkspace(agentID int) string {
    base := SystemPrompt(agentID)

    workspace := fmt.Sprintf(`
你是一个能够访问本地项目文件的 AI 助手。

项目根目录: %s

你可以使用以下工具来查看和分析项目代码：
- read_file: 读取文件内容
- list_files: 列出目录结构
- grep_search: 搜索代码

当用户提到某个文件或代码时，请主动使用工具读取实际内容，不要猜测。
`, tools.WorkspacePath)

    return workspace + "\n" + base
}
```

---

## 五、改造影响与兼容性

### 5.1 受影响文件

| 文件 | 改动类型 | 影响范围 |
|------|---------|---------|
| `llm/client.go` | **扩展** — 新增字段和函数 | 全局（但向后兼容） |
| `agent/agent.go` | **改造** — handleChat 加循环 | Agent 对话 |
| `tools/` (新建) | **新增** — 工具注册和实现 | Agent 对话 |
| `main.go` | **微调** — 初始化工具 | 启动流程 |

### 5.2 实际影响（实施后更新）

| 模块 | 实际改动 | 备注 |
|------|---------|------|
| `team/team.go` | ✅ **已改造** — Worker 使用 workerAgenticLoop | 原计划不改，实际需求驱动提前改造 |
| 记忆系统 | 未改动 | `ExtractMemory` 仍用 `llm.Call()` |
| 前端 | 未改动 | Agent 模式工具调用在服务端透明完成；Team 模式通过 SSE 推送 |
| 历史存储 | 未改动 | `Message` 新增字段带 `omitempty`，旧数据兼容 |

### 5.3 前端改造计划（P1 阶段）

P0 阶段前端不需要任何改动——工具调用在服务端循环内完成，前端只看到最终的文本回复。

P1 阶段改造前端以展示工具调用过程：

```
用户: "帮我分析 main.py"
  │
  ├── 🔧 read_file("main.py")           ← 展示工具调用
  │   └── 📄 165 行代码已读取            ← 展示执行结果
  ├── 🔧 read_file("requirements.txt")
  │   └── 📄 44 行代码已读取
  │
  └── 💬 "这个项目是一个股票分析工具..."   ← 最终回复
```

实现方式：把 `handleChat` 改为 SSE 推送，每轮工具调用都推送事件给前端。

---

## 六、文件目录规划

```
chat_server/
├── main.go                # + tools.Init(), workspace 配置
├── llm/
│   └── client.go          # + ToolCall, ToolDef, CallWithTools()
├── agent/
│   └── agent.go           # handleChat → Agentic Loop
├── tools/                 # 🆕 新建目录
│   ├── tools.go           # Tool 接口 + Registry + Execute()
│   ├── workspace.go       # 工作目录管理 + 路径安全
│   ├── read_file.go       # read_file 工具
│   ├── list_files.go      # list_files 工具
│   └── grep_search.go     # grep_search 工具（P1）
├── team/
│   └── team.go            # 不变
└── web/
    ├── index_html.go      # P1 阶段改造
    └── template.go        # 不变
```

---

## 七、验证方案

### 7.1 P0 阶段测试场景

| 场景 | 用户输入 | 期望行为 |
|------|---------|---------|
| 读文件 | "帮我看看 main.py 的代码" | Agent 调用 read_file → 返回代码分析 |
| 项目结构 | "这个项目的目录结构是什么" | Agent 调用 list_files → 返回项目概览 |
| 代码分析 | "get_data.py 里有哪些函数" | Agent 调用 read_file → 列出函数并解释 |
| 多文件 | "对比 main.py 和 requirements.txt" | Agent 调用两次 read_file → 综合分析 |
| 无关问题 | "今天天气怎么样" | Agent 不调用工具，直接文本回复 |
| 大文件 | "读取 chat_server 二进制" | Agent 收到大小限制错误，建议分段读取 |
| 路径越界 | "读取 /etc/passwd" | 被路径安全拦截 |

### 7.2 验证 API 兼容性

在实施前，需要确认当前使用的 LLM API 是否支持 OpenAI 兼容的 tools 参数：

```bash
curl -X POST "$LLM_API_URL" \
  -H "Authorization: Bearer $LLM_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "messages": [{"role":"user","content":"你好"}],
    "tools": [{
      "type": "function",
      "function": {
        "name": "test_tool",
        "description": "测试工具",
        "parameters": {"type":"object","properties":{}}
      }
    }]
  }'
```

如果 API 不支持 `tools` 参数，需要考虑：
- 方案 A：切换到支持 Function Calling 的 API（如 OpenAI 直连、Azure OpenAI）——**推荐**
- 方案 B：用 prompt 模拟工具调用——详见第八节 Plan B 方案

---

## 八、Plan B：Prompt 模拟工具调用方案

> 如果 API 不支持 `tools` 参数，使用此方案。
> Team 模式的 Leader 已经验证了"prompt 约束 + JSON 解析"的可行性，这里将同样的模式应用到 Agent 工具系统。

### 8.1 为什么 Plan B 是可行的

Team 模式中 Leader 的工作方式：

```
leaderSystemPrompt: "可用 action: dispatch / synthesize / ... 只输出 JSON"
  → LLM 输出: {"action":"dispatch","tasks":[...]}
  → parseLeaderCommand(): 去 markdown 代码块、截取 {...}、json.Unmarshal
  → Go 侧执行 Worker → 结果拼回 messages → 下一轮
```

这和 Agentic Loop 本质相同——**都是 LLM 输出结构化指令 → 服务端解析执行 → 结果回传**。
区别仅在于：原生 Function Calling 由 API 层保证 JSON 格式正确；Prompt 模拟需要自己做容错解析。

Team 的 `parseLeaderCommand()` 已经解决了这个问题（去代码块、截取 JSON、失败则当纯文本），可以直接复用。

### 8.2 Agent 工具调用的 System Prompt

```go
func toolSystemPrompt() string {
    return `你是一个能够访问本地项目文件的 AI 助手。

## 可用工具

| 工具 | 参数 | 用途 |
|------|------|------|
| read_file | path, offset?(行号), limit?(行数) | 读取文件内容 |
| list_files | path?(默认根目录), max_depth?(默认3) | 列出目录结构 |
| grep_search | pattern, path?, include? | 搜索代码 |

## 调用方式

当你需要使用工具时，输出以下 JSON（不要加其他文字）：

{"tool":"read_file","args":{"path":"main.py"}}

一次只调用一个工具。你会收到工具执行结果，然后可以继续调用其他工具或给出最终回答。

## 回答方式

当你已经收集了足够的信息，直接输出回答文本（不要包含 JSON）。

## 规则

- 用户提到文件时，先用工具读取实际内容，不要凭猜测回答
- 不确定文件位置时，先用 list_files 查看目录结构
- 每次工具调用只输出 JSON，不加任何其他文字`
}
```

### 8.3 解析逻辑（复用 Team 模式的经验）

```go
type toolCommand struct {
    Tool string          `json:"tool"`
    Args json.RawMessage `json:"args"`
}

// 从 LLM 输出中解析工具调用（复用 parseLeaderCommand 的容错逻辑）
func parseToolCommand(raw string) *toolCommand {
    raw = strings.TrimSpace(raw)

    // 去掉 markdown 代码块（LLM 经常自作主张加上）
    if strings.HasPrefix(raw, "```") {
        var lines []string
        for _, l := range strings.Split(raw, "\n") {
            if !strings.HasPrefix(strings.TrimSpace(l), "```") {
                lines = append(lines, l)
            }
        }
        raw = strings.Join(lines, "\n")
    }

    // 尝试提取 JSON 对象
    start := strings.Index(raw, "{")
    end := strings.LastIndex(raw, "}")
    if start < 0 || end <= start {
        return nil  // 没有 JSON → 当作纯文本回答
    }

    var cmd toolCommand
    if err := json.Unmarshal([]byte(raw[start:end+1]), &cmd); err != nil {
        return nil
    }
    if cmd.Tool == "" {
        return nil  // 有 JSON 但不是工具调用
    }
    return &cmd
}
```

### 8.4 Plan B 的 Agentic Loop

```go
func handleChatPromptMode(w http.ResponseWriter, r *http.Request, id int) {
    // ... 加载历史、构建 messages（同前）...

    // system prompt 中包含工具说明
    messages[0] = llm.Message{
        Role:    "system",
        Content: toolSystemPrompt() + "\n\n" + SystemPrompt(id),
    }

    for round := 0; round < MaxToolRounds; round++ {
        reply, err := llm.Call(messages, model)  // ← 普通 Call，不带 tools
        if err != nil { ... }

        cmd := parseToolCommand(reply)

        if cmd == nil {
            // 没有工具调用 → 这是最终回答
            finalReply = reply
            break
        }

        // 有工具调用 → 执行
        result, err := tools.Execute(cmd.Tool, cmd.Args)
        content := result
        if err != nil {
            content = fmt.Sprintf("工具执行错误: %v", err)
        }

        // 拼回 messages（assistant 的工具调用 + 工具结果）
        messages = append(messages,
            llm.Message{Role: "assistant", Content: reply},
            llm.Message{Role: "user", Content: fmt.Sprintf("[工具 %s 执行结果]\n%s", cmd.Tool, content)},
        )
    }

    // 保存历史、返回回复 ...
}
```

### 8.5 Plan A vs Plan B 对比

| 维度 | Plan A（原生 Function Calling） | Plan B（Prompt 模拟） |
|------|------|------|
| **API 要求** | 必须支持 `tools` 参数 | 任何 chat completions API |
| **JSON 可靠性** | API 层保证格式正确 | 需要容错解析（Team 已验证） |
| **多工具并行** | API 可一次返回多个 tool_calls | 一次只能调一个 |
| **消息格式** | 标准 `role: "tool"` | 用 `role: "user"` 包裹结果 |
| **Token 效率** | tools schema 只算一次缓存 | 每轮都在 system prompt 中重复 |
| **工具发现** | LLM 天然理解 tool schema | 依赖 prompt 质量 |
| **改动量** | 需要改 llm.Message + 新增 CallWithTools | 只需新增 parseToolCommand |
| **切换成本** | — | 后续升级到 Plan A 只需替换调用层 |

**建议**：先测 API → 支持则走 Plan A → 不支持则立即启用 Plan B → 后续 API 升级后再平滑切换到 Plan A。

两种方案共享 `tools/` 包的所有工具实现（read_file、list_files、grep_search），切换只涉及 LLM 调用层和消息拼接方式。

---

## 九、关键风险与应对策略

### 9.1 风险一：API 兼容性（最高优先级）— ✅ 已解决

| 情况 | 应对 |
|------|------|
| ✅ **API 支持 `tools` 参数** | **走 Plan A，已验证通过** |
| API 不支持但可更换 | 接入 OpenAI / Azure OpenAI / DeepSeek 等支持 FC 的 API |
| API 不支持且不能换 | 走 Plan B，prompt 模拟（Team 模式已验证可行） |

**结果**：curl 测试确认 LLM API 完全支持 tools 参数和 tool_calls 响应，采用 Plan A。

### 9.2 风险二：Token 消耗大幅增加 — ⚠️ 已部分应对，需进一步优化

Agentic Loop 意味着一次用户请求可能产生 **多轮 LLM 调用**。

**实际观测数据**（researcher 分析 analysis 目录）：

```
Round 1: list_files          → 上下文 +2K tokens  → 总计 ~4K  → LLM 耗时 ~3s
Round 2: 9× read_file        → 上下文 +72K tokens → 总计 ~76K → LLM 耗时 ~15s
Round 3: 4× grep_search      → 上下文 +8K tokens  → 总计 ~84K → LLM 耗时 ~20s
Round 4: 3× read_file        → 上下文 +24K tokens → 总计 ~108K → LLM 耗时 ~30s
Round 5: 最终生成             → 面对 108K tokens   → LLM 耗时 60-120s ← 超时
```

**已实施的应急措施**：

| 措施 | 实施 | 效果 |
|------|------|------|
| 结果截断 MaxToolResultChars=4000 | ✅ `tools/workspace.go` | 有限——截断策略错误（应报错引导） |
| workerTimeout 300s | ✅ `team/team.go` | 治标——延迟崩溃点 |
| Worker 提示词效率指导 | ✅ `team/team.go` | 有限——LLM 不一定遵循 |
| maxWorkerToolRounds=6 | ✅ `team/team.go` | 有限——限制轮数但不限上下文大小 |

**根本解决方案**：上下文膨胀治理（第十四节 / [CONTEXT_BLOAT_DESIGN.md](./CONTEXT_BLOAT_DESIGN.md)）

核心改进项：
1. **read_file 行数限制 + 报错引导**：大文件 8K tokens → 报错 ~100 tokens
2. **ReadCache 读取去重**：重复读取 8K → 30 tokens
3. **旧工具结果清理**：Round 5+ 清理前 N 轮结果
4. 预期效果：114K → ~10K tokens，彻底消除超时

### 9.3 风险三：LLM 输出不稳定

| 问题 | 发生场景 | 应对 |
|------|---------|------|
| 工具参数格式错误 | Plan A 中极少；Plan B 中偶发 | Zod/JSON Schema 验证 + 错误提示让 LLM 自修正 |
| 无限循环调用 | LLM 反复调同一个工具 | MaxToolRounds + 已读去重 |
| 不调用工具直接瞎编 | LLM 忽视工具指令 | system prompt 中强调"必须先读取文件" |
| 调用不存在的工具 | LLM 幻觉 | Registry 查找失败 → 返回错误 → LLM 自修正 |

对于 **LLM 工具参数错误的自修正**：

```go
// 执行工具失败时，把错误信息返回给 LLM，让它修正
if err != nil {
    messages = append(messages, llm.Message{
        Role:    "tool",  // 或 Plan B 中用 "user"
        Content: fmt.Sprintf("工具调用错误：%v\n请检查参数后重试。", err),
    })
    continue  // 继续下一轮循环
}
```

---

## 十、与 Team 模式的集成（已完成）

### 10.1 实施结果

原设计中 Team 模式不在 P0 范围（"team/team.go 不变"）。实际使用中发现 Worker 无法读取文件导致分析质量严重不足，因此提前将工具系统集成到 Team 模式：

| 复用方式 | 状态 | 备注 |
|----------|------|------|
| `llm.CallWithToolsRetry` 用于 Worker | ✅ | 新增 `workerAgenticLoop()` |
| `tools.GetToolDefs()` + `tools.Execute()` | ✅ | Worker 直接调用 |
| SSE `worker_tool_call` 事件 | ✅ | 实时推送工具调用进度 |
| Worker 系统提示词增加工具使用指导 | ✅ | 效率优先策略 |

### 10.2 实施中修复的问题

1. **parseLeaderCommand JSON 解析 bug**：Leader 同时返回 dispatch + synthesize 两个 JSON 对象时，`strings.LastIndex("}")` 跨对象匹配导致解析失败。修复为使用 `json.NewDecoder` 只解析第一个 JSON 对象。

2. **Worker 超时**：工具集成后 Worker 上下文膨胀导致超时。应急调整 workerTimeout=300s、MaxToolResultChars=4000、maxWorkerToolRounds=6。根本解决方案见第十四节。

### 10.3 架构关系图

```
                    ┌──────────────┐
                    │   main.go    │
                    │  路由注册     │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
        ┌─────▼─────┐ ┌───▼────┐ ┌────▼─────┐
        │  agent/    │ │ team/  │ │  tools/  │
        │  Agent对话 │ │ 多Agent│ │  工具实现 │
        │            │ │ 编排   │ │          │
        │ Agentic    │ │ Leader │ │ read_file│
        │ Loop ──────┼─┤ Loop   │ │ list_dir │
        │            │ │        │ │ grep     │
        └─────┬──────┘ └───┬────┘ └────┬─────┘
              │            │            │
              │   ┌────────▼────────┐   │
              └──►│     llm/        │◄──┘
                  │  CallWithTools  │ ← Agent 用
                  │  Call           │ ← Team/记忆用
                  │  CallWithRetry  │ ← 两者都可用
                  └─────────────────┘
```

---

## 十一、实施步骤与完成状态

### Phase 0：验证 API 兼容性 ✅

1. ✅ curl 测试确认支持 `tools` 参数
2. ✅ 确认 `tool_calls` 响应格式
3. ✅ 结论：**Plan A**

### Phase 1：核心框架 ✅

1. ✅ 扩展 `llm/client.go`：Message、ToolCall、ToolDef、CallWithTools、CallWithToolsRetry
2. ✅ 创建 `tools/` 包：Tool 接口、Registry、workspace 管理
3. ✅ 实现 `read_file`、`list_files`、`grep_search`
4. ✅ 改造 `agent/agent.go` handleChat 为 Agentic Loop
5. ✅ 配置启动参数（WORKSPACE 环境变量）

### Phase 1+：Team 集成 ✅（计划外）

1. ✅ 创建 `workerAgenticLoop()` 函数
2. ✅ 改造 `executeWorkerWithContext` 使用 Agentic Loop
3. ✅ 添加 SSE `worker_tool_call` 事件
4. ✅ 修复 `parseLeaderCommand` JSON 解析 bug
5. ✅ 调整超时与结果限制参数

### Phase 2：上下文膨胀治理 ⏳ 待实施

1. ⏳ read_file 行数限制 + 报错引导
2. ⏳ 移除统一截断，改为工具自行控制
3. ⏳ ReadCache 读取去重
4. ⏳ cleanOldToolResults 旧结果清理
5. ⏳ maxTurns 替代固定超时

> 详见 [CONTEXT_BLOAT_DESIGN.md](./CONTEXT_BLOAT_DESIGN.md)

### Phase 3：前端展示 ⏳ 待实施

1. ⏳ Agent 模式 `handleChat` 改为 SSE 流式响应
2. ⏳ 前端展示工具调用过程
3. ⏳ Token 消耗统计展示

---

## 十二、与 Claude Code 架构的对照

| Claude Code 特性 | 我们的实现 | 状态 |
|-----------------|-----------|------|
| 40+ 内置工具 | 3 个核心工具（read_file, list_files, grep_search） | ✅ 够用 |
| Zod Schema 验证 | JSON Schema + Go 结构体 | ✅ 等效 |
| 并发/串行编排 | LLM 自主决定并行 tool_calls | ✅ 已支持 |
| 4 层权限系统 | 路径白名单 + 大小限制 | ✅ 简化版 |
| 工具结果大小限制 | MaxToolResultChars = 4000（截断） | ⚠️ 需改为报错引导 |
| 文件读取去重 | — | ❌ 待实施 ReadCache |
| 大文件行数限制 | — | ❌ 待实施 MaxReadLines |
| 旧结果微压缩 | — | ❌ 待实施 cleanOldToolResults |
| 多轮循环 + 安全阀 | Agent: MaxToolRounds=10; Worker: maxWorkerToolRounds=6 | ✅ |
| Agent + Team 双模式 | Agent Agentic Loop + Worker Agentic Loop | ✅ |
| prompt() ≠ description() | 统一用 description | ✅ 工具少 |
| MCP 外部工具 | 不需要（暂时） | — |

**核心思路**：不追求 Claude Code 的完整性，聚焦"让 Agent 能读项目文件"这一核心目标，用最小改动实现最大价值。Plan A 和 Plan B 共享 90% 的代码，切换成本极低。

---

## 十三、实施记录

### Phase 0 结果：API 兼容性验证 ✅

测试确认 LLM API 完全支持 OpenAI 兼容的 `tools` 参数和 `tool_calls` 响应，采用 **Plan A**（原生 Function Calling）。

### Phase 1 结果：核心框架 ✅

已完成全部 P0 功能：

| 实施项 | 文件 | 状态 |
|--------|------|------|
| Message 结构扩展（ToolCalls, ToolCallID） | `llm/client.go` | ✅ |
| CallWithTools / CallWithToolsRetry | `llm/client.go` | ✅ |
| Tool 接口 + Registry + Execute | `tools/tools.go` | ✅ |
| 工作目录 + 路径安全 + 结果截断 | `tools/workspace.go` | ✅ |
| read_file 工具（offset/limit/行号） | `tools/read_file.go` | ✅ |
| list_files 工具（树形/深度/过滤） | `tools/list_files.go` | ✅ |
| grep_search 工具（正则/路径过滤） | `tools/grep_search.go` | ✅ |
| Agent handleChat → Agentic Loop | `agent/agent.go` | ✅ |
| main.go 工具初始化 | `main.go` | ✅ |

### Phase 1+ 结果：Team 模式工具集成 ✅

原设计中 Team 模式不在 P0 范围。在实际使用中发现 Worker 无法读取文件严重影响分析质量，因此提前集成：

| 实施项 | 文件 | 状态 |
|--------|------|------|
| workerAgenticLoop 函数 | `team/team.go` | ✅ |
| executeWorkerWithContext 改造 | `team/team.go` | ✅ |
| Worker 系统提示词增加工具指导 | `team/team.go` | ✅ |
| SSE worker_tool_call 事件 | `team/team.go` | ✅ |
| parseLeaderCommand JSON 解析修复 | `team/team.go` | ✅ |

### 发现的问题：上下文膨胀

实施后发现 Worker 在分析项目代码时反复超时。根因是多轮工具调用导致上下文线性膨胀（17 次调用后达 ~114K tokens）。

**已实施的应急措施**：
- `workerTimeout` 120s → 300s
- `MaxToolResultChars` 8000 → 4000
- Worker 提示词增加效率指导
- `maxWorkerToolRounds` 8 → 6

**根本解决方案**：需要实施上下文膨胀治理，详见第十四节和 [CONTEXT_BLOAT_DESIGN.md](./CONTEXT_BLOAT_DESIGN.md)。

---

## 十四、上下文膨胀治理（P0+ 阶段）

> 完整设计见 [CONTEXT_BLOAT_DESIGN.md](./CONTEXT_BLOAT_DESIGN.md)，此处为核心要点摘要。

### 14.1 问题

Agentic Loop 中工具调用越多 → 上下文越大 → 每轮 LLM 调用越慢 → 越容易超时。实测 researcher 分析项目时 17 次工具调用后上下文达 114K tokens，最后一轮生成需 60-120s。

### 14.2 参照：Claude Code 7 层控制体系

| 层 | 策略 | 当前状态 |
|----|------|---------|
| 1 | 单次结果限制（报错引导 > 截断） | ⚠️ 有截断，但策略错误 |
| 2 | 读取去重（同文件返回 stub） | ❌ 完全缺失 |
| 3 | 聚合预算（单轮总量控制） | ❌ 完全缺失 |
| 4 | 微压缩（清理旧 tool_result） | ❌ 完全缺失 |
| 5 | 自动压缩（LLM 摘要替换历史） | ❌ 暂不需要 |
| 6 | 流式执行 | ❌ 暂不需要 |
| 7 | 溢出兜底（maxTurns） | ✅ MaxToolRounds=10 |

### 14.3 改进方案优先级

| 优先级 | 改动 | 预期效果 |
|--------|------|---------|
| **P0** | read_file 行数限制 200 行 + 超限报错引导用 offset/limit | 大文件 8K tokens → 报错 ~100 tokens |
| **P0** | 移除统一截断，由各工具自行控制 | 避免无效截断浪费 tokens |
| **P0** | ReadCache 读取去重 | 重复读取 8K → 30 tokens |
| **P1** | cleanOldToolResults 旧结果清理 | Round 5+ 不再累积旧结果 |
| **P2** | maxTurns 替代固定超时 | 消除"前功尽弃"的超时模式 |
| **P3** | 聚合预算 + 自动压缩 | 长会话终极兜底 |

### 14.4 预期量化效果

```
实施前:  17 次工具调用 → ~114K tokens → 超时
实施 P0: 行数限制 + 去重 → ~20K tokens → 最后一轮 ~10-15s ✓
实施 P1: + 旧结果清理   → ~10K tokens → 最后一轮 ~5s ✓
```

### 14.5 影响范围

| 文件 | P0 改动 | P1 改动 |
|------|---------|---------|
| `tools/read_file.go` | 加行数限制 + 接受 cache | — |
| `tools/tools.go` | 移除统一截断 | — |
| `tools/workspace.go` | 移除 MaxToolResultChars | — |
| `tools/read_cache.go` | **新增** ReadCache | — |
| `agent/agent.go` | 传入 ReadCache | cleanOldToolResults |
| `team/team.go` | 传入 ReadCache | cleanOldToolResults |

不受影响：`llm/client.go`、`list_files.go`、`grep_search.go`、前端、历史存储。
