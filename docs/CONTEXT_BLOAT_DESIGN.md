# 上下文膨胀治理设计文档

> 基于 Claude Code 7 层上下文控制体系的分析，针对当前 chat_server 的现状与改进方案
>
> **实施状态：P0/P1 措施已全部落地。** 本文档为历史设计文档，以 `current-architecture.md` (v5) 为准。
> 
> 关键差异：
> - `MaxToolResultChars` 已替换为 `DefaultMaxResultChars = 1500` + per-tool 覆盖
> - `ReadCache` 已扩展为 per-session 上下文（含 Model、subAgentFails、HasRead、GetReadTime）
> - `CleanOldToolResults` 已实现并支持 `[SubAgent]` 结果保护
> 
> 相关文档：
> - [current-architecture.md](./current-architecture.md) — 实施后的架构现状（v5）
> - [TOOL_SYSTEM_DESIGN.md](./TOOL_SYSTEM_DESIGN.md) — 工具系统总设计
>
> 更新日期：2026-04-02

---

## 一、问题定义

### 1.1 现象

Team 模式中 researcher Worker 在执行"分析项目代码"任务时反复超时：

```
15:07:28  Worker 启动
          list_files → 9个 read_file → 4个 grep_search → 3个 read_file
          ──── 17 次工具调用，每次 LLM 往返 2-5 秒 ────
15:10:36  执行超时！（180 秒）
```

### 1.2 根因

核心矛盾：**工具调用越多 → 上下文越大 → 每轮 LLM 调用越慢 → 越容易超时**。

用我们的实际数据量化：

```
无优化时的上下文增长：

  Round 1: list_files          → 上下文 +2K tokens  → 总计 ~4K  → LLM 耗时 ~3s
  Round 2: 9× read_file        → 上下文 +72K tokens → 总计 ~76K → LLM 耗时 ~15s
  Round 3: 4× grep_search      → 上下文 +8K tokens  → 总计 ~84K → LLM 耗时 ~20s
  Round 4: 3× read_file        → 上下文 +24K tokens → 总计 ~108K → LLM 耗时 ~30s
  Round 5: 最终生成（综合分析） → 面对 108K tokens   → LLM 耗时 60-120s ← 超时根因
  ──────────────────────────
  总计: ~114K tokens, 总耗时 ~130-190s
```

上下文增长曲线（无优化 vs 理想状态）：

```
Token数
  ^
  |         当前：线性增长
  |        ╱
  |       ╱
  |      ╱      ← 超时/崩溃
  |     ╱
  |    ╱   理想：锯齿形（周期性回落）
  |   ╱   ╱╲    ╱╲
  |  ╱   ╱  ╲  ╱  ╲
  | ╱   ╱    ╲╱    ╲
  |╱   ╱
  └──────────────────→ 工具调用轮次
       ↑     ↑     ↑
       │     │     └── 旧结果清理
       │     └──────── 大结果报错引导
       └────────────── 读取去重
```

### 1.3 为什么"加大超时"不是正确答案

我们之前的修复是把 `workerTimeout` 从 180s 加到 300s。这只是治标：

| 方案 | 效果 | 问题 |
|------|------|------|
| 加大超时 | 延迟崩溃点 | 用户等 5 分钟才拿到结果；文件更多时仍会超时 |
| **控制上下文大小** | 每轮都快 | LLM 始终面对合理大小的上下文，无需等待 |

Claude Code 的设计理念：**不设固定超时，通过控制上下文大小保证每轮速度**。

---

## 二、Claude Code 的 7 层控制体系

| 层 | 策略 | 触发条件 | 效果 |
|----|------|---------|------|
| **1. 单次限制** | read_file 25K token 硬限制 + **报错引导** | 每次读文件时 | 大文件不进上下文 |
| **2. 读取去重** | 同文件再次读取返回 stub（~20 tokens） | 重复 read_file 时 | N 份 → 1 份 + (N-1) stub |
| **3. 聚合预算** | 单轮所有 tool_result 总计 200K 字符上限 | 每轮工具执行后 | 贪心淘汰最大结果 |
| **4. 微压缩** | 清理旧 tool_result，只保留最近 5 个 | 每轮开始前 | 历史不累积 |
| **5. 自动压缩** | LLM 摘要替换整个历史（150K → 8K） | 接近窗口限制时 | 终极兜底 |
| **6. 流式执行** | 边收 LLM 响应边执行工具 | 每轮工具执行时 | 减少等待时间 |
| **7. 溢出兜底** | maxTurns + Prompt Too Long 恢复 | 极端情况 | 防止无限循环 |

### 关键设计原则

**a) 报错 > 截断**

Claude Code 实验 #21841 的结论：截断大文件后的内容消耗 25K tokens 且信息不完整；报错消息只有 ~100 bytes 且**教会 LLM 用 offset/limit 精确读取**。截断方案的 token 消耗是报错方案的 **250 倍**。

**b) 粒度化上下文 > 摘要化上下文**

系统竭力避免做全量压缩（第 5 层），因为压缩会丢失细节。前 4 层的目标是：在不丢失信息的前提下，尽可能控制上下文大小。只有当前 4 层都不够时，才触发摘要替换。

**c) 决策不可逆（Cache 稳定性）**

一旦决定某个 tool_result 不替换（frozen），后续轮次即使预算不够也不能动它——因为改变早期消息会使 prompt cache key 失效。

---

## 三、当前项目现状逐层对照

### 3.1 第 1 层：单次工具结果限制

**当前实现**：

```go
// tools/workspace.go:11-13
const (
    MaxFileSize        = 100 * 1024 // 100KB per file
    MaxToolResultChars = 4000       // single tool result character limit
)

// tools/read_file.go:66-68 — 大文件处理（正确：报错引导）
if info.Size() > MaxFileSize {
    return "", fmt.Errorf("文件过大（%d bytes，限制 %d bytes），请使用 offset + limit 参数分段读取", ...)
}

// tools/tools.go:33-35 — 统一截断（错误：截断而非报错）
if len(result) > MaxToolResultChars {
    result = truncateResult(result)  // ← 截断浪费 tokens 且信息不全
}
```

**问题**：

| 维度 | 当前 | Claude Code | 差距 |
|------|------|-------------|------|
| 文件大小限制 | 100KB（字节） | 25K tokens（语义级） | 100KB 的代码文件约 30K+ tokens，远超合理范围 |
| 超限处理 | `MaxFileSize` 报错（好）；`MaxToolResultChars` 截断（差） | 一律报错 + 引导用 offset/limit | 截断浪费 4000 tokens 且信息不全 |
| 行数限制 | 无 | N/A（用 token 限制等效） | 200 行的 Python 文件约 1K tokens，2000 行约 8K |

**核心差距**：`MaxToolResultChars = 4000` 的截断策略是错误的。一个被截断到 4000 字符的文件内容——LLM 不知道哪些被截断了、截断了多少、剩余内容是否重要——**还不如直接报错让 LLM 重新精确读取**。

### 3.2 第 2 层：读取去重

**当前实现**：无。

```go
// agent/agent.go:498-531 — handleChat Agentic Loop
for round := 0; round < MaxToolRounds; round++ {
    msg, err := llm.CallWithToolsRetry(messages, toolDefs, model, 1)
    // ... 没有任何去重逻辑 ...
    // 每次 read_file 都返回完整文件内容
}

// team/team.go:676-713 — workerAgenticLoop 同样无去重
for round := 0; round < maxWorkerToolRounds; round++ {
    // ...
}
```

**实际影响**：

```
场景：Leader continue_worker → researcher 重新分析
  Round 1: read_file("analysis/__init__.py")          → 返回全文（500 tokens）
  Round 4: read_file("analysis/__init__.py")          → 再次返回全文（500 tokens）
  Round 6: read_file("analysis/__init__.py")          → 第三次返回全文（500 tokens）

无去重: 1500 tokens
有去重: 500 + 20 + 20 = 540 tokens（节省 64%）
```

在我们的 17 次工具调用场景中，如果 3 个文件被重复读取，去重可节省 ~24K tokens。

### 3.3 第 3 层：聚合预算

**当前实现**：无。

LLM 一次返回 9 个 `tool_calls`（并行读 9 个文件），全部结果无条件拼入上下文。

**实际影响**：

```
Round 2: LLM 返回 9 个 read_file 调用
  文件 1: 2000 行 → ~8K tokens
  文件 2: 600 行  → ~2.5K tokens
  文件 3: 380 行  → ~1.5K tokens
  ...
  文件 9: 1500 行 → ~6K tokens
  ────────────────
  总计: ~40K tokens 一次性灌入上下文

Claude Code 会做什么：
  如果聚合超过预算 → 贪心淘汰最大的结果
  → 文件 1（8K）被替换为磁盘摘要（~200 tokens）
  → 文件 9（6K）被替换为磁盘摘要（~200 tokens）
  → 节省 ~13K tokens
```

### 3.4 第 4 层：微压缩

**当前实现**：无。

所有历史工具结果永驻上下文。在 Agent 模式中，`handleChat` 的 Agentic Loop 内 `messages` 数组只增不减。

```go
// agent/agent.go:511-530 — messages 只 append 不清理
messages = append(messages, *msg)          // assistant + tool_calls
for _, tc := range msg.ToolCalls {
    // ...
    messages = append(messages, llm.Message{  // tool results
        Role: "tool", Content: content,
    })
}
// Round 1 的 tool_result 在 Round 5 仍占据上下文

// team/team.go:686-709 — workerAgenticLoop 同样只 append 不清理
msgs = append(msgs, *msg)
for _, tc := range msg.ToolCalls {
    msgs = append(msgs, llm.Message{
        Role: "tool", Content: content,
    })
}
```

**Claude Code 的做法**：在每轮开始前，保留最近 5 个 tool_result，其余替换为 `"[Old tool result content cleared]"`。

### 3.5 第 5-7 层

这三层（自动压缩、流式执行、溢出兜底）在我们的规模下暂不需要。前 4 层的优化就足以解决当前问题。

---

## 四、量化影响模拟

用我们的实际场景（researcher 分析 analysis 目录，17 次工具调用）模拟各层优化效果：

```
基线（当前实现）:
  9 × read_file（平均 8K tokens） = 72K tokens
  4 × grep_search（平均 2K tokens）= 8K tokens
  3 × read_file（平均 8K tokens） = 24K tokens
  + 结构开销                       = 10K tokens
  ─────────────────────────────────
  总计: ~114K tokens → 最后一轮生成 60-120s → 超时

应用第 1 层（行数限制 + 报错引导）:
  大文件（>200行）不再全量读入，LLM 学会用 offset/limit
  → 每个 read_file 平均从 8K 降到 ~3K tokens
  → 12 × 3K + 4 × 2K = 44K tokens
  → 最后一轮生成 ~20-30s ✓

应用第 2 层（读取去重）:
  假设 3 个文件被重复读取
  → 节省 3 × 8K = 24K tokens → 重复读取仅消耗 ~60 tokens
  → 总计降到 ~20K tokens
  → 最后一轮生成 ~10-15s ✓

应用第 4 层（微压缩）:
  Round 5 开始前，清理 Round 1-2 的旧 tool_result
  → 再节省 ~10K tokens
  → 总计降到 ~10K tokens
  → 最后一轮生成 ~5s ✓

累积效果: 114K → ~10K tokens, 60-120s → ~5s
```

**结论**：仅前两层就能将超时问题从根本上解决（114K → 20K），是最高 ROI 的改进。

---

## 五、改进方案设计

### 5.1 P0：read_file 行数限制 + 报错引导

**原则**：不截断，而是报错并引导 LLM 使用 offset/limit。

**改动文件**：`tools/read_file.go`

**方案**：

```go
const MaxReadLines = 200  // 单次读取最多 200 行（约 800-2000 tokens）

func executeReadFile(args json.RawMessage) (string, error) {
    // ... 解析参数、安全检查 ...

    lines := strings.Split(string(data), "\n")

    // 无 offset/limit 时，如果文件超过 MaxReadLines 行 → 报错引导
    if input.Offset == 0 && input.Limit == 0 && len(lines) > MaxReadLines {
        return "", fmt.Errorf(
            "文件 %s 共 %d 行，超过单次读取限制（%d 行）。\n"+
            "请使用以下方式精确读取：\n"+
            "- offset + limit 读取特定行范围（如 offset=1, limit=100 读取前100行）\n"+
            "- 或先用 grep_search 搜索相关内容的位置",
            input.Path, len(lines), MaxReadLines)
    }

    // 有 offset/limit 时不限制（用户已明确指定范围）
    // ... 正常读取逻辑 ...
}
```

**同时移除截断逻辑**（`tools/tools.go`）：

```go
func Execute(name string, args json.RawMessage) (string, error) {
    result, err := t.Execute(args)
    // 移除: if len(result) > MaxToolResultChars { result = truncateResult(result) }
    // 改为: 由各工具自行控制输出大小
    return result, nil
}
```

**grep_search 也需要类似的行数限制**：当前限制 100 行匹配结果，这个可以保留但改为更精确的控制。

**预期效果**：
- 200 行的文件直接读取（约 800-2000 tokens，合理）
- 2000 行的文件 → 报错 → LLM 学会 `offset=1, limit=100` → 仅消耗 ~500 tokens
- 单个工具结果上限从 ~8K tokens 降到 ~2K tokens

### 5.2 P0：读取去重（readFileCache）

**原则**：同一个 Agentic Loop 内，如果文件未修改，重复读取返回 stub。

**改动文件**：新增 `tools/read_cache.go`，修改 `tools/read_file.go`

**方案**：

```go
// tools/read_cache.go

type readCacheEntry struct {
    Path    string
    Offset  int
    Limit   int
    ModTime time.Time
}

type ReadCache struct {
    mu      sync.Mutex
    entries map[string]readCacheEntry  // key = "path:offset:limit"
}

func NewReadCache() *ReadCache {
    return &ReadCache{entries: make(map[string]readCacheEntry)}
}

func (c *ReadCache) Check(path string, offset, limit int) bool {
    c.mu.Lock()
    defer c.mu.Unlock()
    key := fmt.Sprintf("%s:%d:%d", path, offset, limit)
    entry, ok := c.entries[key]
    if !ok {
        return false
    }
    info, err := os.Stat(path)
    if err != nil {
        return false
    }
    return info.ModTime().Equal(entry.ModTime)
}

func (c *ReadCache) Mark(path string, offset, limit int) {
    c.mu.Lock()
    defer c.mu.Unlock()
    key := fmt.Sprintf("%s:%d:%d", path, offset, limit)
    info, _ := os.Stat(path)
    c.entries[key] = readCacheEntry{
        Path: path, Offset: offset, Limit: limit,
        ModTime: info.ModTime(),
    }
}
```

**在 read_file 工具中使用**：

```go
func executeReadFile(cache *ReadCache, args json.RawMessage) (string, error) {
    // ... 解析参数 ...

    absPath := resolvePath(input.Path)

    // 去重检查
    if cache != nil && cache.Check(absPath, input.Offset, input.Limit) {
        return fmt.Sprintf("（文件 %s 内容未变化，与上次读取相同，请使用之前的结果）", input.Path), nil
    }

    // ... 正常读取 ...

    // 标记已读
    if cache != nil {
        cache.Mark(absPath, input.Offset, input.Limit)
    }
    return result, nil
}
```

**Cache 生命周期**：
- Agent 模式：每次 `handleChat` 创建新的 `ReadCache`，请求结束后释放
- Team 模式：每次 `workerAgenticLoop` 创建新的 `ReadCache`，Worker 执行结束后释放
- 不跨请求持久化（因为文件可能在两次请求间被修改）

**预期效果**：
- 重复读取：8K tokens → ~30 tokens（stub）
- 我们的场景中 3 个文件重复读取 → 节省 ~24K tokens

### 5.3 P1：旧工具结果清理（简版微压缩）

**原则**：在 Agentic Loop 中，保留最近 N 个 tool_result，更早的替换为占位文本。

**改动文件**：`agent/agent.go`、`team/team.go` 中的 loop 函数

**方案**：

```go
const keepRecentToolResults = 5  // 保留最近 5 个工具结果

func cleanOldToolResults(messages []llm.Message) []llm.Message {
    // 从后往前找 tool_result 消息，保留最近 5 个
    toolCount := 0
    for i := len(messages) - 1; i >= 0; i-- {
        if messages[i].Role == "tool" {
            toolCount++
            if toolCount > keepRecentToolResults {
                messages[i].Content = "[旧工具结果已清理，请参考后续结果]"
            }
        }
    }
    return messages
}
```

**在 Agentic Loop 中调用**：

```go
for round := 0; round < MaxToolRounds; round++ {
    // 每轮开始前清理旧结果
    if round >= 3 {
        messages = cleanOldToolResults(messages)
    }

    msg, err := llm.CallWithToolsRetry(messages, toolDefs, model, 1)
    // ...
}
```

**预期效果**：
- Round 5+ 的上下文不再包含 Round 1-2 的完整工具结果
- 上下文从线性增长变为"锯齿形"增长

### 5.4 P2：maxTurns 替代固定超时

**原则**：用工具调用轮次限制代替时间限制，避免"前面工作全白费"的超时。

**当前问题**：

```
固定超时 300s 的失败模式：
  Round 1-4: 工具调用，共耗时 120s（LLM 越来越慢）
  Round 5: LLM 面对 100K+ tokens → 生成综合分析 → 需要 200s
  → 在 300s 时超时 → 前面读的所有文件、做的所有分析全部丢失
```

**Claude Code 的做法**：不用时间超时，用 `maxTurns`（最大轮次）。每轮内有 LLM 自己的 HTTP 超时（120s），但整体流程不会因为"总耗时太长"而被杀。

**方案**：

```go
// team/team.go
const (
    workerTimeout       = 600 * time.Second  // 放宽为安全网（10分钟），不作为主控制
    maxWorkerToolRounds = 6                   // 主控制：最多 6 轮工具调用
)
```

上下文控制做好后，6 轮工具调用的上下文始终在合理范围（<30K tokens），每轮 LLM 耗时不超过 15s，6 轮总计 ~90s，不存在超时风险。

---

## 六、改进优先级与预期效果

| 优先级 | 改动 | 文件 | 工作量 | 效果（token 节省） |
|--------|------|------|--------|-------------------|
| **P0** | read_file 行数限制 + 报错引导 | `tools/read_file.go` | 小 | 大文件 8K → 报错 100B，平均 -60% |
| **P0** | 移除截断，由工具自行控制 | `tools/tools.go`, `tools/workspace.go` | 小 | 避免无效截断 |
| **P0** | 读取去重 readFileCache | 新增 `tools/read_cache.go`，改 `read_file.go` | 中 | 重复读取 8K → 30 tokens |
| **P1** | 旧工具结果清理 | `agent/agent.go`, `team/team.go` | 中 | Round 5+ 清理前 N 轮结果 |
| **P2** | maxTurns 替代固定超时 | `team/team.go` | 小 | 消除"前功尽弃"的超时模式 |
| **P3** | 聚合预算（单轮总量控制） | `tools/tools.go` | 大 | 进一步控制单轮峰值 |
| **P3** | 自动压缩（LLM 摘要） | `agent/agent.go`, `team/team.go` | 大 | 长会话终极兜底 |

### 预期效果总览

```
实施 P0（行数限制 + 去重）:
  114K tokens → ~20K tokens → 最后一轮 ~10-15s → 不再超时 ✓

实施 P0 + P1（+ 微压缩）:
  114K tokens → ~10K tokens → 最后一轮 ~5s → 快速响应 ✓

实施 P0 + P1 + P2（+ maxTurns）:
  即使 10 轮工具调用也能在 ~60s 内完成，不存在超时风险 ✓
```

---

## 七、与现有代码的兼容性

### 7.1 影响范围

| 模块 | P0 改动 | P1 改动 | 影响 |
|------|---------|---------|------|
| `tools/read_file.go` | 加行数限制 + 接受 cache 参数 | 无 | 所有使用 read_file 的地方 |
| `tools/tools.go` | 移除统一截断 | 无 | Execute 接口变化 |
| `tools/workspace.go` | 移除 MaxToolResultChars | 无 | 删除不再需要的常量 |
| `tools/read_cache.go` | 新增 | 无 | 新文件 |
| `agent/agent.go` | 传入 ReadCache | cleanOldToolResults | Agent Agentic Loop |
| `team/team.go` | 传入 ReadCache | cleanOldToolResults | Worker Agentic Loop |

### 7.2 不受影响

| 模块 | 原因 |
|------|------|
| `llm/client.go` | 不涉及上下文管理 |
| `tools/list_files.go` | 目录列表本身很小 |
| `tools/grep_search.go` | 已有 100 行限制，结果通常较小 |
| Team Leader 编排 | Leader 不使用工具 |
| 前端 | 后端透明优化 |
| 历史存储 | 只保存 user + final reply，不保存工具中间过程 |

### 7.3 ReadCache 的传递方式

当前 `tools.Execute` 是全局函数，不接受上下文参数。要传入 ReadCache 有两种方案：

**方案 A：改 Execute 签名（推荐）**

```go
func Execute(name string, args json.RawMessage, cache *ReadCache) (string, error)
```

调用方（agent.go、team.go）在每次请求开始时创建 cache，传入 Execute。

**方案 B：用 context.Context 传递**

```go
func Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
    cache := ReadCacheFromContext(ctx)
    // ...
}
```

更 Go-idiomatic，但改动稍大。

**建议采用方案 A**——简单直接，改动小。

---

## 八、实施计划

### Phase 1：P0 改造（预计 1 天）

1. `read_file.go`：加 `MaxReadLines = 200` 行数限制 + 报错引导
2. `tools.go`：移除统一截断逻辑，由各工具自行控制输出
3. `workspace.go`：移除 `MaxToolResultChars` 和 `truncateResult()`
4. 新增 `read_cache.go`：ReadCache 实现
5. `read_file.go`：集成 ReadCache 去重逻辑
6. `agent/agent.go`：handleChat 中创建并传入 ReadCache
7. `team/team.go`：workerAgenticLoop 中创建并传入 ReadCache
8. 编译验证 + 功能测试

### Phase 2：P1 改造（预计 0.5 天）

1. 实现 `cleanOldToolResults` 函数
2. 集成到 Agent 和 Team 的 Agentic Loop 中
3. 测试多轮场景

### Phase 3：P2 调整（预计 0.5 天）

1. 放宽 workerTimeout 为安全网（不作为主控制）
2. 确认 maxToolRounds 作为主控制手段
3. 测试极端场景（10 轮工具调用）
