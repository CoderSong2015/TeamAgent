# 实施路线图

> 基于 [多轮编排改进设计](./multi-turn-design.md)，规划分阶段落地路径。
>
> 每个阶段独立可交付，不阻塞后续阶段的开发。

---

## 阶段总览

```
Phase 1 (基础)          Phase 2 (增强)          Phase 3 (完善)
────────────────        ────────────────        ────────────────
多轮编排循环             异步命令队列             Agent-Team 记忆打通
Worker 上下文持久化       Worker 进度感知          团队级 Scratchpad
Leader 协议扩展          智能结果摘要             LLM Streaming
错误重试                 SSE 事件流扩展           UI 改进
API Key 环境变量化       依赖执行顺序             Worker 动态角色
────────────────        ────────────────        ────────────────
预计：3-4 天             预计：2-3 天             预计：3-4 天
```

---

## Phase 1：多轮编排基础（核心价值）

> 目标：让 Leader 能在多轮中迭代决策，Worker 保持上下文连续性。

### 1.1 Leader 多轮循环

**改动范围**：`team/team.go` — `runOrchestration` 函数

**具体步骤**：

1. 将 `runOrchestration` 从线性流程改为 `for` 循环
2. 每轮调用 Leader LLM，解析 action，按 action 类型分支处理
3. 增加 `maxLeaderTurns = 6` 安全阀，触发时强制综合
4. Leader 每轮结果注入下一轮上下文（assistant + user 消息对）

**关键数据结构**：

```go
// 不需要新文件，在 team.go 中扩展
var maxLeaderTurns = 6
```

**向后兼容**：
- 如果 Leader 第一轮返回 `direct_reply` 或 `dispatch` + 第二轮 `synthesize`，行为与当前完全一致
- Pipeline 模式作为 `dispatch` 内部逻辑保留

**验证标准**：
- [x] Leader 可以在看到 Worker 结果后再次 dispatch
- [x] Leader 可以在多轮后用 synthesize 结束
- [x] maxLeaderTurns 触发时不会死循环
- [x] 当前的 direct_reply 场景不受影响

### 1.2 Worker 上下文持久化

**改动范围**：`team/team.go` — `executeWorkers`、新增 `WorkerState`

**具体步骤**：

1. 定义 `WorkerState` 结构体（内存中的 session 级状态）
2. 定义 `SessionContext` 结构体（per-request 的 Team 会话上下文）
3. 修改 `executeWorkers`：执行前加载 Worker 历史，执行后追加到历史
4. 新增 `saveWorkerSessionHistory` / `loadWorkerSessionHistory`
5. 修改 `continueWorkers`：复用已有 Worker 上下文

**持久化路径**：`data/teams/<id>/workers/<name>/session_history.json`

**验证标准**：
- [x] Coder 在修订循环中能看到之前的对话上下文
- [x] Continue 模式下 Worker 保有完整历史
- [x] Worker history 超过 20 条时自动截断

### 1.3 Leader 指令协议扩展

**改动范围**：`team/team.go` — `leaderSystemPrompt`、`parseLeaderCommand`、`leaderCommand` 结构体

**具体步骤**：

1. 扩展 `leaderCommand` 结构体：增加 `reason` 字段，`taskAssign` 增加 `mode` 字段
2. 扩展 `leaderSystemPrompt`：增加四阶段工作流说明、新 action 类型说明
3. `parseLeaderCommand` 增加对新 action 的兼容处理
4. 增加 `continue_worker` 和 `verify` 两个 action 的处理逻辑

**验证标准**：
- [x] 旧格式 JSON（只有 action/content/plan/tasks）仍可正常解析
- [x] 新格式 JSON（含 reason/mode）能正确解析
- [x] Leader 解析失败时仍降级为 direct_reply

### 1.4 错误重试

**改动范围**：`llm/client.go`

**具体步骤**：

1. 新增 `CallWithRetry(messages, model, maxRetries)` 函数
2. 指数退避：1s → 2s → 4s
3. Worker 调用统一使用 `CallWithRetry(msgs, model, 2)`
4. Leader 调用使用 `CallWithRetry(msgs, model, 1)`

**验证标准**：
- [x] 临时性 API 错误（429/500/503）自动重试
- [x] 永久性错误（400/401）不重试
- [x] 重试延迟不超过 7s（1+2+4）

### 1.5 API Key 环境变量化

**改动范围**：`llm/client.go`

**具体步骤**：

1. 将 `ApiURL` 和 `ApiKey` 改为从环境变量读取，带默认值
2. 启动时打印使用的 endpoint（不打印 key）

```go
var (
    ApiURL = getEnvDefault("LLM_API_URL", "https://...")
    ApiKey = getEnvDefault("LLM_API_KEY", "")
)
```

---

## Phase 2：异步增强

> 目标：提升编排灵活性和用户体验。

### 2.1 异步命令队列

**改动范围**：`team/team.go` — 新增 `CommandQueue` 相关逻辑

**设计**：
- 用 Go channel 替代当前的 WaitGroup 等待模式
- Worker 完成后发送 Command 到 channel
- 主循环用 `select` 消费，支持"先到先处理"

**验证标准**：
- [x] Worker A 先完成时立即通知前端，不等 Worker B
- [x] 超时 Worker 不阻塞其他 Worker 的结果处理

### 2.2 Worker 进度感知

**改动范围**：`team/team.go` — `executeWorkerWithContext`

**方案选择**：
- **首选**：心跳机制（15s 间隔发送 `worker_heartbeat` SSE 事件）
- **进阶**：如果 LLM API 支持 streaming，实时转发 token

**验证标准**：
- [x] Worker 执行超过 15s 时前端能看到心跳
- [x] 心跳不影响最终结果的正确性

### 2.3 智能结果摘要

**改动范围**：`team/team.go` — 新增 `processWorkerResult`、`generateSummary`

**设计**：
- 完整结果存入 Worker session history（最大 10000 rune）
- 超过 1500 rune 的结果用 LLM 生成摘要传给 Leader
- 摘要失败时降级为硬截断

**验证标准**：
- [x] 短结果（<1500 rune）不额外调用 LLM
- [x] 长结果的摘要保留关键结论和代码片段
- [x] 摘要调用失败时不影响主流程

### 2.4 SSE 事件流扩展

**改动范围**：`team/team.go`、`web/index_html.go`

**新增事件**：

| 事件 | 触发时机 |
|------|---------|
| `leader_turn` | Leader 第 N 轮开始 |
| `worker_continue` | Worker 接收后续指令 |
| `worker_heartbeat` | Worker 执行心跳 |
| `worker_result_received` | 异步模式下单个 Worker 完成 |
| `verify_start` | 验证阶段开始 |
| `max_turns_reached` | Leader 达到最大轮数 |

**前端适配**：
- 显示当前 Leader 轮数
- Worker 心跳时显示加载动画
- 验证阶段有独立的 UI 区块

### 2.5 依赖执行顺序

**改动范围**：`team/team.go` — `executeWorkersAsync`

**设计**：
- `tasks[].depends_on` 指定依赖关系
- 无依赖的 Worker 并行执行
- 有依赖的 Worker 等待依赖完成后启动
- 依赖的 Worker 结果自动注入到依赖者的 task 中

**验证标准**：
- [x] `depends_on: ["researcher"]` 的 Coder 在 Researcher 完成后才启动
- [x] 无依赖关系时行为与现有并行执行一致

---

## Phase 3：生态完善

> 目标：打通 Agent-Team 边界，提升整体智能水平。

### 3.1 Agent-Team 记忆打通

**改动范围**：`team/team.go`、`agent/agent.go`（需要跨包引用）

**设计**：
- Team Worker 的 system prompt 自动注入 `project` 类型的 Agent 记忆
- 通过共享 `agent.LoadMemory` 函数实现
- 只读取 `project` 类型，不读取 `user`/`feedback` 等隐私类型

**验证标准**：
- [x] Agent 中记录的"后端使用 Go"等项目记忆在 Team Worker 中可用
- [x] Team Worker 不会看到用户画像等隐私记忆
- [x] Agent 没有 project 记忆时不影响 Team 功能

### 3.2 团队级 Scratchpad

**改动范围**：`team/team.go` — 新增 Scratchpad 读写逻辑

**设计**：
- 每个 Team 会话创建 `data/teams/<id>/scratchpad/` 目录
- Worker 可以将大段内容写入 scratchpad（通过特殊指令格式）
- 其他 Worker 的 system prompt 中提示可用的 scratchpad 文件

**验证标准**：
- [x] Researcher 的调研报告可以写入 scratchpad
- [x] Coder 可以引用 Researcher 的 scratchpad 内容
- [x] 会话结束后 scratchpad 保留，下次可清理

### 3.3 LLM Streaming

**改动范围**：`llm/client.go`、`team/team.go`

**设计**：
- `llm.CallStream` 支持 SSE 流式响应
- Worker 执行时实时将 token 流通过 SSE 推送到前端
- 前端逐字渲染 Worker 输出

**验证标准**：
- [x] Worker 输出逐字显示，而非等待全部完成
- [x] 流式失败时降级为非流式调用
- [x] Leader 调用仍使用非流式（需要解析 JSON）

### 3.4 Worker 动态角色

**改动范围**：`team/team.go` — Worker 注册机制

**设计**：
- 允许 Leader 在 dispatch 时定义临时 Worker 角色

```json
{"action": "dispatch", "tasks": [
    {"worker": "security_auditor", "task": "安全审计", "specialty": "你是安全审计专家..."}
]}
```

- 如果 Worker name 不在预定义列表中，使用 task 中的 specialty 作为 system prompt
- 动态 Worker 没有持久化历史

**验证标准**：
- [x] Leader 可以创建任意角色的临时 Worker
- [x] 预定义 Worker（researcher/coder/reviewer）不受影响
- [x] 动态 Worker 在 UI 中有独立的展示

### 3.5 UI 改进

**改动范围**：`web/index_html.go`

**设计**：
- 显示 Leader 决策轮数指示器
- Worker 状态面板：running / idle / stopped
- 多轮编排过程的可视化时间线
- 验证阶段的独立展示区块
- Worker 流式输出的逐字渲染

---

## 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| Leader 无限循环 | 消耗大量 token | `maxLeaderTurns` 安全阀 |
| Worker 上下文过长 | token 超限 / 成本增加 | 历史截断（最近 20 条） + 未来做摘要压缩 |
| 多轮延迟累积 | 用户等待过久 | SSE 实时进度 + 心跳 + 可中断 |
| Leader 不遵守新协议 | 解析失败 | 向后兼容的 `parseLeaderCommand` + 降级 |
| LLM 重试增加延迟 | Worker 响应变慢 | 只重试临时错误 + 上限 2 次 |
| 记忆跨模块依赖 | 包耦合增加 | Phase 3 才引入，通过接口隔离 |

---

## 测试策略

### 单元测试

| 模块 | 测试点 |
|------|--------|
| `parseLeaderCommand` | 新旧格式解析、容错降级 |
| `CallWithRetry` | 重试逻辑、退避间隔、错误分类 |
| `processWorkerResult` | 短结果直传、长结果摘要、摘要失败降级 |
| `WorkerState` | 状态转换、历史截断 |

### 集成测试

| 场景 | 验证内容 |
|------|---------|
| 简单问题 | Leader 第 1 轮 direct_reply，行为与当前一致 |
| 标准 dispatch | Leader dispatch → Workers → synthesize，与当前一致 |
| 多轮编排 | Research → Leader 分析 → Implementation → Verify → synthesize |
| Pipeline + 修订 | 现有 coder↔reviewer 循环在新框架下正常工作 |
| Worker 失败重试 | Worker 调用失败 → 自动重试 → 成功/最终失败 |
| Leader 安全阀 | 模拟 Leader 不断 dispatch → maxLeaderTurns 触发强制综合 |

---

## 文档索引

| 文档 | 位置 | 内容 |
|------|------|------|
| 当前架构分析 | `docs/current-architecture.md` | 现有代码的完整梳理 |
| 多轮编排设计 | `docs/multi-turn-design.md` | 改进方案的详细技术设计 |
| 实施路线图 | `docs/implementation-roadmap.md` | 本文档，分阶段落地计划 |
| 参考文献 | Claude Code `team-mode-design.md` | Coordinator + Swarm 模式源码分析 |
