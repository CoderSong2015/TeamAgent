# 子 Agent（SubAgent）系统设计文档

> 基于 Claude Code 子 Agent 模式分析，为 chat_server 设计上下文隔离的子 Agent 能力
>
> **实施状态：已全部实现。** 注意：实际实现与本设计文档有若干差异（见各节 ⚠️ 标注），以代码和 `current-architecture.md` 为准。
>
> 更新日期：2026-04-02

## 1. 背景与动机

### 1.1 当前瓶颈与已实施优化

**已实施的 Context Bloat 治理（基线）：**

| 措施 | 值 | 效果 |
|------|-----|------|
| `MaxToolResultChars` | 1500 字符 | 每个工具结果截断 |
| `KeepRecentToolResults` | 3 | 只保留最近 3 个 tool 结果 |
| `maxWorkerToolRounds` | 4 | 减少工具轮次 |
| `CleanOldToolResults` | 第 2 轮起 | 更早清理旧结果 |
| `maxWorkerHistory` | 8 条，旧 tool 压缩到 300 字 | continue 模式压缩 |

优化后，Worker 单次执行的上下文约 **10K-15K tokens**（优化前 30K-50K+）。总结 LLM 调用耗时从 2-4 分钟降到约 **60-90 秒**。

**仍然存在的瓶颈**：
1. **串行探索**：研究员逐轮调用工具（list → read → grep → read...），4 轮串行 = 每轮等 LLM 决策
2. **单一上下文**：即使截断后，4 轮 × 3 工具/轮 × 1500 字符 = 仍有 ~18K 字符的工具结果
3. **无法分治**：复杂调研（如"分析项目架构"）本质上可分解为多个独立子问题，但当前只能串行处理

### 1.2 Claude Code 的解法

Claude Code 的核心理念：

> **子 Agent 的首要动机不是"分工"，而是"上下文隔离"——把搜索/读取的大量原始输出隔离在子 Agent 中，只把摘要结果返回给父 Agent。**

三种子 Agent 模式：

| 模式 | 核心特征 | 适用场景 |
|------|---------|---------|
| **Explore Agent** | 只读、用小/快模型、无 CLAUDE.md | 代码搜索、架构探索 |
| **Fork** | 继承父上下文、共享 prompt cache | 开放式研究、多步实现 |
| **General-Purpose** | 全功能工具集 | 复杂调查、多步任务 |

其中 **Explore Agent** 最适合移植到 chat_server，因为：
- 研究员 80% 的工作是只读探索（list_files、read_file、grep_search、glob_search）
- 不需要 prompt cache 共享（我们的 API 不支持）
- 实现简单：本质上就是一个轻量级的独立 agentic loop

### 1.3 SubAgent 的独立价值（在 Context Bloat 治理之上）

SubAgent **不是** Context Bloat 治理的替代品，而是在其基础上进一步优化。两者的关系：

| 维度 | Context Bloat 治理 | SubAgent |
|------|-------------------|----------|
| 解决什么 | 降低单次 LLM 调用的上下文大小 | 并行分治 + 进一步隔离 |
| 如何做 | 截断、清理、压缩 | 委派到独立上下文 |
| 核心收益 | 单次调用从 60s→20s | 串行变并行，总耗时从 90s→40s |
| 独立性 | 已实施，持续生效 | 依赖 Context Bloat 治理作为基础 |

**SubAgent 的核心卖点（已剥离 Context Bloat 因素）：**

1. **并行探索**：3 个 explore 并行搜索 ≈ 原来串行 3 轮的 1/3 时间
2. **委派式任务分解**：Worker 只做"规划 + 综合"，搜索委派给子 Agent，职责清晰
3. **上下文进一步隔离**：子 Agent 的工具结果完全不进入 Worker 主上下文
4. **渐进式集成**：不改变 Leader/审核/记忆架构，只在 Worker 层增加能力

---

## 2. 架构设计

### 2.1 整体架构

```
┌─────────────────────────────────────────────┐
│                   Leader                     │
│        (编排层 — 不感知子 Agent)              │
└──────────────┬──────────────────────────────┘
               │ dispatch / continue_worker
               ▼
┌─────────────────────────────────────────────┐
│              Worker (如 Researcher)           │
│         workerAgenticLoop (主上下文)          │
│                                              │
│  可用工具:                                    │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐     │
│  │read_file │ │list_files│ │grep_search│     │
│  └──────────┘ └──────────┘ └──────────┘     │
│  ┌────────────────┐  ┌────────────────────┐  │
│  │ explore(新增)  │  │ delegate_task(新增)│  │
│  └───────┬────────┘  └────────┬───────────┘  │
│          │                    │               │
│          ▼                    ▼               │
│  ┌──────────────┐    ┌──────────────┐        │
│  │ Explore 子AG │    │ GP 子 Agent  │        │
│  │  只读工具集   │    │  全部工具集   │        │
│  │  2轮上限     │    │  3轮上限     │        │
│  │  独立上下文   │    │  独立上下文   │        │
│  │  返回≤500字  │    │  返回≤800字  │        │
│  └──────────────┘    └──────────────┘        │
│          │                    │               │
│          └──── 摘要结果 ──────┘               │
│               (进入主上下文)                   │
└─────────────────────────────────────────────┘
```

**关键点**：子 Agent 的工具调用结果**完全隔离**在自己的上下文中，Worker 主上下文只看到最终摘要。

### 2.2 两种子 Agent 模式

#### 模式 A：`explore` — 快速只读搜索

参照 Claude Code 的 Explore Agent：

| 属性 | 值 |
|------|-----|
| 工具集 | `list_files`、`read_file`、`grep_search`、`glob_search`（只读） |
| 最大轮次 | 3 轮（兼容 read_file 行数限制报错后重试） |
| 超时 | 60 秒 |
| 结果上限 | 800 字符 |
| 工具结果截断 | 1500 字符（与主 Agent 一致） |
| System Prompt | 只读搜索专家，快速返回精炼结果 |

**触发场景**：
- 研究员需要了解目录结构
- 搜索特定函数或类的定义
- 读取配置文件摘要
- 任何 "查找 + 总结" 类任务

#### 模式 B：`delegate_task` — 通用子任务

参照 Claude Code 的 General-Purpose Agent：

| 属性 | 值 |
|------|-----|
| 工具集 | 标记为 `SubAgentAllowed` 的工具（当前为全部只读工具） |
| 最大轮次 | 3 轮 |
| 超时 | 120 秒 |
| 结果上限 | 1200 字符 |
| System Prompt | 通用任务执行者，完成后给出精炼报告 |

**触发场景**：
- 需要多步操作的子任务
- 写入文件或执行命令（未来扩展）
- 复杂分析需要多次工具交互

### 2.3 工具定义

#### `explore` 工具

```json
{
  "name": "explore",
  "description": "启动一个轻量级只读子 Agent 来搜索和探索代码库。子 Agent 有独立上下文（不会污染你的上下文），会使用 list_files、read_file、grep_search、glob_search 工具进行探索，最终返回精炼的摘要结果。适合：了解目录结构、查找函数定义、读取配置文件摘要等。你可以在一次回复中调用多个 explore 来并行探索不同问题。",
  "parameters": {
    "type": "object",
    "properties": {
      "task": {
        "type": "string",
        "description": "要探索/搜索的具体问题。写清楚要找什么、在哪里找、返回什么格式的结果。像对一个聪明的同事下达搜索指令。"
      },
      "scope": {
        "type": "string",
        "description": "搜索范围（目录路径），限定子 Agent 的探索范围。默认为项目根目录。",
        "default": ""
      }
    },
    "required": ["task"]
  }
}
```

#### `delegate_task` 工具

```json
{
  "name": "delegate_task",
  "description": "启动一个通用子 Agent 来执行多步子任务。子 Agent 有独立上下文和安全工具权限，完成后返回精炼报告。适合：需要多步操作的复杂子任务。注意：比 explore 慢，只在 explore 不够用时才使用。",
  "parameters": {
    "type": "object",
    "properties": {
      "task": {
        "type": "string",
        "description": "要执行的具体任务。提供完整的背景和期望输出格式。"
      }
    },
    "required": ["task"]
  }
}
```

### 2.4 子 Agent 内部实现

```go
// subagent.go — 子 Agent 核心

type SubAgentConfig struct {
    Mode        string   // "explore" | "delegate"
    Task        string   // 任务描述
    Scope       string   // 搜索范围（explore 模式）
    Tools       []string // 可用工具白名单，空 = 全部
    MaxRounds   int      // 最大工具轮次
    Timeout     time.Duration
    MaxResult   int      // 结果最大字符数
    Model       string   // LLM 模型
}

func RunSubAgent(config SubAgentConfig) (string, error) {
    // 1. 构建独立的消息上下文
    msgs := []llm.Message{
        {Role: "system", Content: subAgentSystemPrompt(config)},
        {Role: "user", Content: config.Task},
    }

    // 2. 获取工具定义（按白名单过滤）
    toolDefs := filterToolDefs(config.Tools)

    // 3. 独立的 ReadCache（不共享）
    cache := tools.NewReadCache()

    // 4. 独立的 agentic loop（上下文完全隔离）
    for round := 0; round < config.MaxRounds; round++ {
        if round >= 1 {
            tools.CleanOldToolResults(msgs)
        }

        msg, err := llm.CallWithToolsRetry(msgs, toolDefs, config.Model, 1)
        if err != nil {
            return "", err
        }

        if len(msg.ToolCalls) == 0 {
            return truncateResult(msg.Content, config.MaxResult), nil
        }

        msgs = append(msgs, *msg)
        for _, tc := range msg.ToolCalls {
            result, _ := tools.Execute(tc.Function.Name, ...)
            msgs = append(msgs, llm.Message{
                Role: "tool", ToolCallID: tc.ID, Content: result,
            })
        }
    }

    // 5. 达到轮次上限 → 强制生成总结
    msgs = append(msgs, llm.Message{
        Role: "user",
        Content: "你已达到工具调用上限。请立即用一段精炼文字总结你找到的所有信息。",
    })
    finalMsg, err := llm.CallWithToolsRetry(msgs, nil, config.Model, 1)
    if err != nil {
        return "子 Agent 总结生成失败", nil
    }
    return truncateResult(finalMsg.Content, config.MaxResult), nil
}
```

### 2.5 并行执行

Worker 的 `workerAgenticLoop` 已经支持在一轮中处理多个 tool_calls。当 LLM 在同一轮返回多个 `explore` 调用时，**天然可以并行执行**：

```go
// workerAgenticLoop 中处理 tool_calls 的部分（改造后）
for _, tc := range msg.ToolCalls {
    if tc.Function.Name == "explore" || tc.Function.Name == "delegate_task" {
        // 子 Agent 类工具 → 并行启动
        go func(tc ToolCall) {
            result := RunSubAgent(...)
            resultCh <- result
        }(tc)
    } else {
        // 普通工具 → 串行执行
        result := tools.Execute(...)
    }
}
```

**并行限制**：同一轮最多并行 3 个子 Agent，超出部分串行执行。

### 2.6 System Prompt 设计

#### Explore 子 Agent Prompt

```
你是一个只读文件搜索专家。

=== 严格只读模式 ===
- 你只能使用 list_files、read_file、grep_search、glob_search 工具
- 禁止任何修改操作

你的优势：
- 快速定位文件和代码
- 用 grep_search 搜索模式匹配
- 阅读并提炼文件内容

工作原则：
- 快速、精炼：尽快完成搜索并返回核心发现
- 高效使用工具：一次调用多个搜索，优先覆盖面
- 大文件只读前 50 行和关键段落
- 返回结构化的简要结论，不要原样复制文件内容

搜索范围：{scope}
```

#### Delegate 子 Agent Prompt

```
你是一个任务执行专家。

给定任务后，使用可用工具高效完成。完成后用精炼的文字报告：
1. 做了什么
2. 关键发现
3. 如有问题，说明原因

原则：
- 不要过度打磨，但也不要留半成品
- 效率优先，聚焦核心问题
- 大文件只读关键部分
```

---

## 3. Worker 层集成

### 3.1 工具注册

在 `tools/` 包中新增 `subagent.go`，注册 `explore` 和 `delegate_task` 两个工具。

### 3.2 Worker Prompt 引导

在 Worker 的 system prompt 中添加子 Agent 使用指导：

```
## 子 Agent 使用策略（分层搜索）

你有两个特殊工具来委派搜索和子任务：

1. explore（推荐优先使用）
   - 只读搜索专家，有独立上下文
   - 适合：查目录结构、搜索函数定义、读配置文件
   - 你可以同时调用多个 explore 并行搜索不同问题
   - 返回精炼摘要，不会膨胀你的上下文

2. delegate_task
   - ⚠️ 实际实现：只读子 Agent，与 explore 使用相同的只读工具集，但超时更长、结果更大
   - 比 explore 慢，仅在 explore 不够时使用
   - 适合：需要多步搜索和分析的复杂只读任务

决策指南：
- 能用 1 次 grep_search / read_file 解决的 → 直接用工具
- 需要 3+ 次搜索才能回答的 → 用 explore
- 需要多步操作（如读多个文件后交叉分析）→ 用 delegate_task
- 可以分解为多个独立子问题的 → 多个 explore 并行
```

### 3.3 研究员专属优化

研究员的 Specialty prompt 更新为：

```
你是团队中的研究员。你的核心策略是「委派搜索，综合分析」：

1. 收到任务后，先规划需要了解哪些方面
2. 用 explore 并行委派搜索任务（每个 explore 负责一个方面）
3. 收到所有 explore 的摘要后，综合分析并输出结论
4. 只有 explore 返回信息不足时，才自己直接使用工具补充

这样你的上下文保持精简，分析更快更准。
```

### 3.4 对 Agent 模式的影响

Agent 模式（单 Agent）的 `handleChat` 同样可以受益。在 `agent/agent.go` 的 agentic loop 中，`explore` 和 `delegate_task` 工具同样可用。

---

## 4. 上下文对比分析

> 注意：以下对比基于 **已实施 Context Bloat 治理后** 的基线（MaxToolResultChars=1500, KeepRecentToolResults=3, maxWorkerToolRounds=4）。

### 当前状态（Context Bloat 已治理，无子 Agent）

```
Worker 主上下文:
  [system]  研究员 prompt (~600 tokens)
  [user]    Leader 任务 (~200 tokens)
  [asst]    调用 list_files + grep_search（轮次1）
  [tool]    结果 ×2 (截断到 3000 chars → ~1000 tokens)
  [asst]    调用 read_file × 2（轮次2）
  [tool]    结果 ×2 (截断到 3000 chars → ~1000 tokens)
  [asst]    调用 read_file × 2（轮次3，旧结果被清理）
  [tool]    结果 ×2 (截断到 3000 chars → ~1000 tokens，保留最近3个)
  [asst]    调用 grep_search + read_file（轮次4）
  [tool]    结果 ×2 (截断到 3000 chars → ~1000 tokens)
  ─────────────────────────────────
  总计: ~4000-5000 tokens（清理后实际保留 ~3000 tokens 工具结果）
  总结 LLM 调用耗时: 60-90 秒
  4 轮串行工具调用: 每轮 LLM 决策 ~10-15 秒 × 4 = 40-60 秒
  端到端总耗时: ~100-150 秒
```

### 加入子 Agent 后

```
Worker 主上下文:
  [system]  研究员 prompt (~700 tokens)
  [user]    Leader 任务 (~200 tokens)
  [asst]    调用 explore × 3（并行）       ← 1 轮 LLM 决策
  [tool]    [SubAgent] explore #1 (800 chars → ~270 tokens)  ← 高密度摘要
  [tool]    [SubAgent] explore #2 (800 chars → ~270 tokens)
  [tool]    [SubAgent] explore #3 (800 chars → ~270 tokens)
  [asst]    综合分析结论                    ← 直接输出，无需更多工具
  ─────────────────────────────────
  Worker 主上下文: ~2000 tokens → 综合 LLM 调用耗时 10-20 秒

  子 Agent #1 (独立上下文，执行完即销毁):
    3 轮工具调用 × ~5-8 秒/轮 LLM = ~15-24 秒
  子 Agent #2, #3 (并行执行) → 同时完成

  端到端总耗时:
    Worker 第1次 LLM 决策: ~10 秒
    + 3 个 explore 并行: ~25 秒（取最慢的一个）
    + Worker 综合 LLM 调用: ~15 秒
    = ~50 秒
```

**对比**（Context Bloat 已治理的基线）：

| 维度 | 当前（无子 Agent） | 加入子 Agent |
|------|-------------------|-------------|
| Worker 主上下文 | 3000-5000 tokens | ~1700 tokens |
| Worker LLM 调用次数 | 5 次（4轮+总结） | 2 次（决策+综合） |
| 子 Agent LLM 调用次数 | 0 | 6-9 次（3个×2-3轮） |
| 串行 LLM 调用次数 | 5 次 | 2 次 + 1 批并行 |
| 端到端耗时 | 100-150 秒 | **40-60 秒** |
| 核心加速来源 | — | 串行→并行（4轮→1批） |

---

## 5. SSE 事件设计

前端需要感知子 Agent 的执行状态：

| 事件 | 数据 | 说明 |
|------|------|------|
| `subagent_start` | `{worker, id, mode, task}` | 子 Agent 启动 |
| `subagent_tool_call` | `{worker, id, tool, args}` | 子 Agent 执行工具 |
| `subagent_done` | `{worker, id, result}` | 子 Agent 完成，返回摘要 |
| `subagent_error` | `{worker, id, error}` | 子 Agent 失败 |

前端展示建议：Worker 卡片下方折叠显示子 Agent 活动，类似 "🔍 子搜索 #1: 正在探索目录结构..."

---

## 6. 资源控制与安全

### 6.1 资源限制

| 限制项 | 值 | 说明 |
|--------|-----|------|
| 单个 Worker 最大子 Agent 数 | 10（⚠️ 原设计 6，已调大） | 防止无限递归 |
| 单轮最大并行子 Agent | 3 | 控制并发 LLM 调用 |
| explore 最大轮次 | 3 | 兼容 read_file 行数限制报错后重试 |
| delegate_task 最大轮次 | 3 | 适度深入 |
| explore 超时 | 60s | |
| delegate_task 超时 | 120s | |
| explore 结果上限 | 800 字符 | 足够返回 3-4 个文件的摘要 |
| delegate_task 结果上限 | 1200 字符 | |

### 6.2 递归防护

子 Agent **不能再启动子 Agent**。实现方式：

```go
func GetToolDefs() []llm.ToolDef { ... }         // 全部工具（含 explore/delegate_task）
func GetToolDefsNoSubAgent() []llm.ToolDef { ... } // 排除子 Agent 工具（子 Agent 内部用）
```

子 Agent 的 agentic loop 使用 `GetToolDefsNoSubAgent()`，从工具层面禁止递归。

### 6.3 工具权限控制

`delegate_task` 不自动继承所有工具。在工具注册时增加安全标记：

```go
type Tool struct {
    Name             string
    Description      string
    Parameters       json.RawMessage
    Execute          func(args json.RawMessage, cache *ReadCache) (string, error)
    SubAgentAllowed  bool  // 是否允许子 Agent 使用
}
```

- 当前所有只读工具（read_file, list_files, grep_search）标记为 `SubAgentAllowed: true`
- 未来新增的写入/执行类工具默认为 `false`，需显式授权
- explore 模式额外限制为只读工具白名单，不受此标记影响

### 6.4 子 Agent 结果保护

子 Agent 返回的摘要是高信息密度结果，**不应被 `CleanOldToolResults` 清理**。

实现方式：子 Agent 结果添加 `[SubAgent]` 前缀标记，`CleanOldToolResults` 跳过此类结果：

```go
func CleanOldToolResults(messages []llm.Message) {
    toolCount := 0
    for i := len(messages) - 1; i >= 0; i-- {
        if messages[i].Role == "tool" {
            // 跳过子 Agent 返回的高密度摘要
            if strings.HasPrefix(messages[i].Content, "[SubAgent]") {
                continue
            }
            toolCount++
            if toolCount > KeepRecentToolResults {
                messages[i].Content = "[旧工具结果已清理]"
            }
        }
    }
}
```

### 6.5 与现有上下文管理的关系

子 Agent 内部仍然使用现有的上下文管理机制：
- `ReadCache`：子 Agent 有独立的 cache。**P0 不共享**（避免并发问题），P1 考虑只读快照共享
- `CleanOldToolResults`：子 Agent 第 1 轮后就开始清理
- `MaxToolResultChars`：工具结果截断仍然生效

### 6.6 并行执行与错误处理

子 Agent 并行执行需要完整的超时和错误处理：

```go
func executeSubAgentsParallel(configs []SubAgentConfig) []SubAgentResult {
    ctx, cancel := context.WithTimeout(context.Background(), maxParallelTimeout)
    defer cancel()

    results := make([]SubAgentResult, len(configs))
    var wg sync.WaitGroup

    for i, cfg := range configs {
        wg.Add(1)
        go func(idx int, c SubAgentConfig) {
            defer wg.Done()
            subCtx, subCancel := context.WithTimeout(ctx, c.Timeout)
            defer subCancel()

            result, err := RunSubAgentWithContext(subCtx, c)
            if err != nil {
                results[idx] = SubAgentResult{
                    Error: fmt.Sprintf("子 Agent 失败: %v", err),
                }
            } else {
                results[idx] = SubAgentResult{Content: result}
            }
        }(i, cfg)
    }
    wg.Wait()
    return results
}
```

关键原则：
- **超时传播**：每个子 Agent 有独立超时（explore 60s, delegate 120s），整批有总超时
- **部分失败**：一个子 Agent 失败不影响其他结果，失败信息作为 error 返回
- **结果收集**：使用 `sync.WaitGroup` 等待所有子 Agent 完成（或超时）

### 6.7 降级策略

当子 Agent 连续失败或 API 限流时，自动降级为直接工具调用：

```go
// ⚠️ 实际实现已改为 per-session（存储在 ReadCache 中），不再使用全局变量
// ReadCache.subAgentFails int32 (atomic)
// (*ReadCache).ShouldDegradeSubAgent() bool — 连续 3 次失败后降级
// (*ReadCache).IncrSubAgentFail() / ResetSubAgentFail()
```

降级后 Worker 回退到传统的 `workerAgenticLoop` 模式（直接使用 read_file 等工具），保证功能可用。失败计数在成功执行后重置。

---

## 7. 实施计划

### Phase 1：核心子 Agent 引擎（P0）

**文件变更**：

| 文件 | 变更 |
|------|------|
| `tools/subagent.go` (新增) | SubAgentConfig、RunSubAgent 执行引擎、explore/delegate_task 工具注册、并行执行器 |
| `tools/tools.go` | Tool 结构体增加 `SubAgentAllowed`；新增 `GetToolDefsNoSubAgent()` 和 `GetSubAgentAllowedToolDefs()` |
| `tools/context.go` | `CleanOldToolResults` 跳过 `[SubAgent]` 前缀的结果 |
| `team/team.go` | `workerAgenticLoop` 中识别子 Agent 工具调用并分发执行 |

**核心要点**：
- 并行执行器包含 `context.WithTimeout`、部分失败处理、`sync.WaitGroup` 收集
- 子 Agent 结果添加 `[SubAgent]` 前缀，防止被 CleanOldToolResults 清理
- 降级策略：连续 3 次失败后回退到直接工具调用

**预计工作量**：1 天

### Phase 2：Worker Prompt 优化 + SSE（P1）

| 文件 | 变更 |
|------|------|
| `team/team.go` | Worker system prompt 中添加子 Agent 使用指导（分层搜索策略） |
| `team/team.go` | 研究员 Specialty 更新为"委派搜索，综合分析"策略 |
| `team/team.go` | SSE 事件：subagent_start / subagent_tool_call / subagent_done / subagent_error |

**预计工作量**：0.5 天

### Phase 3：Agent 模式集成 + 前端展示（P2）

| 文件 | 变更 |
|------|------|
| `agent/agent.go` | handleChat 的 agentic loop 支持子 Agent 工具 |
| `web/index_html.go` | 前端渲染子 Agent 活动折叠面板 |

**预计工作量**：1 天

### Phase 4：ReadCache 共享优化（P3，可选）

| 文件 | 变更 |
|------|------|
| `tools/read_cache.go` | 新增 `Snapshot()` 方法，返回只读 cache 副本 |
| `tools/subagent.go` | 子 Agent 启动时传入父 cache 快照 |

**预计工作量**：0.5 天

---

## 8. 设计权衡

### 8.1 为什么不实现 Fork 模式？

Claude Code 的 Fork 模式核心优势是 **prompt cache 共享**——所有 fork 共享父级的 API 缓存前缀。这依赖于 Anthropic API 的特定能力（对相同前缀的请求缓存）。

当前 chat_server 使用的 LLM API 不确定是否支持此特性。即使支持，实现 Fork 的消息构建复杂度远高于 Explore + Delegate 方案，收益不确定。

**结论**：先实现 Explore + Delegate（确定收益），后续如果 API 支持 cache，再考虑 Fork。

### 8.2 为什么子 Agent 不能再启动子 Agent？

- **简单性**：递归子 Agent 增加调试和资源控制的复杂度
- **Claude Code 也这样做**：Fork 禁止再 Fork，Explore 的工具黑名单中包含 Agent 工具
- **深度 vs 广度**：两层已经足够——Worker 负责规划，子 Agent 负责执行

### 8.3 LLM 调用次数会增加吗？

会轻微增加：原来 4 轮主循环 = 5 次 LLM 调用（4 轮 + 1 总结），现在 3 个子 Agent 各 2 次 + Worker 1-2 次 = 7-8 次。

但**单次调用速度提升远大于次数增加**：
- 原来：5 次 × 每次 30-60 秒 = 2.5-5 分钟
- 现在：子 Agent 6 次 × 5-10 秒 + Worker 2 次 × 10-15 秒 = 50-80 秒

子 Agent 的上下文极小（<1000 tokens），LLM 推理速度接近实时。

### 8.4 与现有 Team 编排的关系

子 Agent 是 **Worker 内部的实现细节**，对 Leader 编排完全透明：
- Leader 不知道 Worker 内部用了子 Agent
- Worker 对外表现不变（接收任务、返回结果）
- 审核者看到的仍然是 Worker 的最终输出
- 记忆系统、交接消息等不受影响

---

## 9. 未来演进

### 9.1 专用小模型

如果 API 支持指定不同模型，explore 子 Agent 可以使用更小更快的模型（类似 Claude Code 用 haiku）。这会进一步提升速度并降低成本。

### 9.2 缓存共享

如果 API 支持 prompt cache，可以让子 Agent 共享 Worker 的 system prompt 缓存前缀，进一步降低 token 开销。

### 9.3 流式子 Agent

当前设计是子 Agent 执行完再返回结果。未来可以支持子 Agent 的中间状态流式推送到前端，提升用户体感。

### 9.4 用户自定义子 Agent

允许用户通过配置定义自己的子 Agent 类型（类似 Claude Code 的 `loadAgentsDir`），指定工具集、prompt、轮次限制等。

---

## 10. 设计审核记录

### v2 审核（基于 v1 的 7 项反馈）

| # | 审核意见 | 判断 | 处理 |
|---|---------|------|------|
| 1 | Context Bloat 优先级关系不明确 | 认同 | 更新 §1.1 基线为已实施状态，§1.3 明确 SubAgent 独立价值，§4 重新计算对比 |
| 2 | explore 2 轮不够 | 认同 | 上调到 3 轮（§2.2, §6.1） |
| 3 | 500/800 字符偏小 | 部分认同 | explore→800, delegate→1200，平衡信息量与上下文膨胀 |
| 4 | 并行错误处理缺失 | 认同 | 新增 §6.6 完整并行执行模板 |
| 5 | ReadCache 不共享 | 认同但低优先级 | P0 接受重复读取，§6.5 标注取舍，新增 P3 ReadCache 快照 |
| 6 | delegate_task 全权限风险 | 前瞻性认同 | 新增 §6.3 SubAgentAllowed 标记机制 |
| 7 | 子 Agent 结果被误清理 | 认同 | 新增 §6.4 `[SubAgent]` 前缀保护机制 |
| — | 补充降级策略 | 认同 | 新增 §6.7 降级策略 |
