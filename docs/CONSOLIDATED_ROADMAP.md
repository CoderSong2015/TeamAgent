# 工具系统整合路线图

> 整合 [TOOL_SYSTEM_DESIGN.md](./TOOL_SYSTEM_DESIGN.md)（工具系统基础设计）与 [TOOL_INTEGRATION_PLAN.md](./TOOL_INTEGRATION_PLAN.md)（工具扩展规划），对照已实现功能，输出统一的行动计划。
>
> 更新时间：2026-04-02
>
> **状态更新：Phase A–D 已全部实现。** 剩余工作为 Phase E（前端）和 Phase F（Web 能力）。

---

## 一、现状盘点

### 1.1 已实现功能一览

将两份文档的规划与实际代码对照后，当前完成度如下：

#### 工具层

| 工具 | 来源 | 状态 | 文件 |
|------|------|------|------|
| `read_file` | TOOL_SYSTEM P0 | ✅ | `tools/read_file.go` — offset/limit、200 行限制、ReadCache |
| `list_files` | TOOL_SYSTEM P0 | ✅ | `tools/list_files.go` — 树形、深度限制、条目限制 |
| `grep_search` | TOOL_SYSTEM P0 | ✅ | `tools/grep_search.go` — 正则、路径过滤、100 行限制 |
| `explore` | SUBAGENT P0 | ✅ | `tools/subagent.go` — 只读子 Agent，3 轮，800 字符 |
| `delegate_task` | SUBAGENT P0 | ✅ | `tools/subagent.go` — 只读子 Agent，3 轮，1200 字符 |
| `glob_search` | INTEGRATION P1 | ✅ | `tools/glob_search.go` — ** 递归匹配、mtime 排序、100 结果 |
| `edit_file` | INTEGRATION P2 | ✅ | `tools/edit_file.go` — 先读后改 + mtime竞态 + 引号归一化 + diff + 备份 |
| `write_file` | INTEGRATION P2 | ✅ | `tools/write_file.go` — 创建/覆盖 + 安全控制 + 200KB 限制 |
| `run_command` | INTEGRATION P3 | ✅ | `tools/run_command.go` — 默认禁用 + 黑名单 + 命令语义 + 大输出持久化 |
| `web_search` | INTEGRATION P6 | ❌ | 未实现 |
| `web_fetch` | INTEGRATION P6 | ❌ | 未实现 |

#### 上下文治理

| 措施 | 来源 | 状态 | 当前值 |
|------|------|------|--------|
| 工具结果截断 | CONTEXT_BLOAT P0 | ✅ | `DefaultMaxResultChars = 1500` + per-tool(read:3000, grep/list/glob:2000, cmd:8000) |
| 旧结果清理 | CONTEXT_BLOAT P1 | ✅ | `KeepRecentToolResults = 3`，第 2 轮起清理 |
| ReadCache 去重 | CONTEXT_BLOAT P0 | ✅ | `tools/read_cache.go` |
| read_file 行数限制 | CONTEXT_BLOAT P0 | ✅ | `MaxReadLines = 200` |
| continue 模式历史压缩 | — | ✅ | `maxWorkerHistory = 8`，旧 tool 结果压缩到 300 字符 |
| 子 Agent 结果保护 | SUBAGENT 审核 | ✅ | `[SubAgent]` 前缀不被清理 |
| 子 Agent 降级策略 | SUBAGENT 审核 | ✅ | 连续 3 次失败降级为直接工具调用 |

#### 架构层

| 能力 | 来源 | 状态 |
|------|------|------|
| Agent Agentic Loop | TOOL_SYSTEM P0 | ✅ `agent/agent.go` |
| Worker Agentic Loop | TOOL_SYSTEM P1+ | ✅ `team/team.go` |
| Leader 编排 + 多轮循环 | — | ✅ |
| 通用审核工作流 | — | ✅ 所有 Worker 输出经审核者 |
| 架构师角色 + 大功能 Pipeline | — | ✅ 调研→设计→编码，阶段审核 |
| 书记员（公共记忆写入） | — | ✅ Leader 回复前同步调用 |
| Worker 交接消息 | — | ✅ 完成后 @下一流程 |
| 记忆系统（公共+角色+Agent桥接） | — | ✅ `team/memory.go` |
| 子 Agent 并行执行 | SUBAGENT P0 | ✅ semaphore 控制，最多 3 个并行 |
| SSE 实时推送 | TOOL_SYSTEM P1 | ✅ Team 模式全面支持 |

### 1.2 两份文档的交叉覆盖分析

```
TOOL_SYSTEM_DESIGN.md          TOOL_INTEGRATION_PLAN.md
├─ P0 核心框架     ✅           ├─ Phase 1 glob_search      ❌
├─ P1+ Team 集成   ✅           ├─ Phase 2 edit/write_file  ❌
├─ P2 上下文治理   ✅（大部分）  ├─ Phase 3 run_command      ❌
├─ P3 前端展示     ❌           ├─ Phase 4 执行引擎升级     ⚠️ 部分
└─ Plan B 备份     ✅（不需要）  ├─ Phase 5 SubAgent         ✅
                                └─ Phase 6 Web 能力         ❌
```

**关键发现**：
1. TOOL_SYSTEM_DESIGN 的 P0-P1 全部完成，P2（上下文治理）已超额完成
2. TOOL_INTEGRATION_PLAN 的 Phase 5（SubAgent）已提前完成
3. **真正的缺口**：glob_search、edit_file、write_file、run_command、前端展示

---

## 二、统一路线图

基于现状，重新排序剩余工作。原则：
1. 每步独立可用
2. 收益/工作量比优先
3. 安全递增（只读→写入→命令执行）

### Phase A：文件搜索增强（glob_search）— ✅ 已完成
> 工作量：0.5 天 | 风险：低 | 前置：无

**目标**：支持按文件名模式搜索（`**/*.go`、`**/config*.yaml`），补齐 "知道文件名但不知道路径" 的场景。

**内容**：
- 新增 `tools/glob_search.go`
- 使用 `doublestar/v4` 库支持 `**` 递归匹配
- 结果按修改时间排序，最多 100 个
- 标记 `SubAgentAllowed: true`（explore 可用）

**与 SubAgent 的整合**：
- explore 子 Agent 自动获得 glob_search 能力
- 研究员可以通过 explore 委派文件定位任务

**工具定义**：

```json
{
  "name": "glob_search",
  "description": "按文件名模式搜索项目文件。支持 glob 语法（如 **/*.go, **/test_*.py）。结果按修改时间排序。适合：知道文件名但不确定位置时快速定位。",
  "parameters": {
    "properties": {
      "pattern": { "type": "string", "description": "Glob 模式" },
      "path": { "type": "string", "description": "搜索目录（默认项目根目录）" }
    },
    "required": ["pattern"]
  }
}
```

**文件变更**：

| 文件 | 变更 |
|------|------|
| `tools/glob_search.go`（新增） | 工具实现 |
| `go.mod` | 添加 `doublestar/v4` 依赖 |

---

### Phase B：文件写入能力（edit_file + write_file）— ✅ 已完成
> 工作量：1.5 天 | 风险：中 | 前置：无

**目标**：让 Agent 从"只读分析"升级为"读写操作"。这是能力的质变——编码者角色才能真正写代码。

**内容**：

1. **`edit_file`** — 精确字符串替换（参照 Claude Code FileEditTool）
   - 唯一性检查：`old_string` 在文件中必须恰好出现 1 次（或 `replace_all=true`）
   - 替换后清除 ReadCache
   - 返回简洁 diff 摘要

2. **`write_file`** — 创建新文件/覆盖写入
   - 自动创建父目录
   - 路径安全检查 + 写入大小限制 200KB

3. **安全控制**
   - `WriteEnabled` 总开关（环境变量 `TOOL_WRITE_ENABLED`）
   - `WriteDenyPaths` 黑名单（`.git/`、`go.sum` 等）
   - 写入工具标记 `SubAgentAllowed: false`（子 Agent 禁止写入）

**工具属性**：

| 工具 | SubAgentAllowed | IsSubAgentTool |
|------|----------------|---------------|
| edit_file | ❌ | ❌ |
| write_file | ❌ | ❌ |

只有 Worker 主循环可以调用写入工具，explore/delegate_task 子 Agent 不可写。

**文件变更**：

| 文件 | 变更 |
|------|------|
| `tools/edit_file.go`（新增） | edit_file 实现 |
| `tools/write_file.go`（新增） | write_file 实现 |
| `tools/workspace.go` | 新增 WriteEnabled / WriteDenyPaths / MaxWriteSize |

---

### Phase C：Shell 命令执行（run_command）— ✅ 已完成
> 工作量：1.5 天 | 风险：高 | 前置：Phase B

**目标**：让 Agent 能执行 shell 命令（编译、测试、git 操作）。配合 edit_file 可以实现完整的"改代码→编译→看错误→再改"闭环。

**内容**：

1. **`run_command`** — 执行 shell 命令
   - 在 `WorkspacePath` 目录下执行
   - 超时控制（默认 30s，最大 120s）
   - 捕获 stdout + stderr

2. **三层安全防护**
   - 第 1 层：黑名单硬拒绝（`rm -rf /`、`mkfs`、`dd if=` 等）
   - 第 2 层：白名单自动放行（`go build`、`go test`、`git status` 等只读命令）
   - 第 3 层：灰名单允许但记日志

3. **输出管理**
   - 最大 8000 字符（命令输出比文件内容更紧凑）
   - 超长输出保留头尾各一半

4. **环境变量控制**
   - `TOOL_COMMAND_ENABLED=false`（默认关闭，需显式开启）

**子 Agent 整合**：
- `run_command` 标记 `SubAgentAllowed: false`
- 只有 Worker 主循环可以执行命令
- 未来 delegate_task 可选择性开放只读命令

**文件变更**：

| 文件 | 变更 |
|------|------|
| `tools/run_command.go`（新增） | 工具实现 |
| `tools/command_safety.go`（新增） | 黑名单 / 白名单 / 安全检查 |
| `tools/workspace.go` | 新增 CommandEnabled 配置 |

---

### Phase D：执行引擎升级（读写分流 + 分级截断）— ✅ 已完成
> 工作量：1 天 | 风险：中 | 前置：Phase B

**目标**：有了写操作工具后，需要升级执行引擎——只读工具可并行，写工具必须串行。

**内容**：

1. **工具属性升级**

```go
type Tool struct {
    Name            string
    Description     string
    Parameters      json.RawMessage
    Execute         func(args json.RawMessage, cache *ReadCache) (string, error)
    SubAgentAllowed bool
    IsSubAgentTool  bool
    IsReadOnly      bool   // 新增：影响并发策略
    MaxResultChars  int    // 新增：0 = 使用全局默认
}
```

2. **并行/串行执行（在 workerAgenticLoop 中）**

```
一轮 tool_calls:
  [read_file, read_file, grep_search]  → 全部只读 → 并行执行
  [edit_file]                          → 写操作 → 串行执行
  [read_file, edit_file, read_file]    → 按顺序：并行(read) → 串行(edit) → 并行(read)
```

3. **分级截断**

| 工具 | 结果上限 | 原因 |
|------|---------|------|
| read_file | 0（自行控制：200 行限制） | 已有行数限制 |
| list_files | 3000 | 目录树较大 |
| grep_search | 3000 | 搜索结果较多 |
| glob_search | 3000 | 文件列表 |
| edit_file | 1500 | diff 摘要 |
| write_file | 500 | 确认消息 |
| run_command | 8000 | 命令输出需要更多空间 |
| explore | 0（自行控制：800 字符） | SubAgent 已截断 |
| delegate_task | 0（自行控制：1200 字符） | SubAgent 已截断 |

**文件变更**：

| 文件 | 变更 |
|------|------|
| `tools/tools.go` | Tool 结构体升级，Execute 分流逻辑 |
| `team/team.go` | workerAgenticLoop 使用读写分流 |
| `agent/agent.go` | handleChat 的循环也使用读写分流 |

---

### Phase E：前端工具调用展示
> 工作量：1-2 天 | 风险：低 | 前置：Phase A（最好在 B 之后）

**目标**：Agent 模式（单 Agent）也能实时展示工具调用过程，而不是等全部完成才返回。

**当前状态**：
- Team 模式已有完整 SSE 推送（worker_tool_call、subagent_start/done 等）
- Agent 模式的 handleChat 仍是同步 HTTP 响应

**内容**：

1. Agent 模式 `handleChat` 改为 SSE 流式响应
2. 前端 Agent 聊天界面渲染工具调用过程
3. 复用 Team 模式的 SSE 事件格式

```
用户: "帮我分析 main.py"
  │
  ├── 🔍 explore("列出项目结构")          ← 子 Agent 探索
  │   └── 📄 返回摘要
  ├── 🔍 explore("读取 main.py 核心逻辑")
  │   └── 📄 返回摘要
  │
  └── 💬 "这个项目是一个股票分析工具..."    ← 最终回复
```

**文件变更**：

| 文件 | 变更 |
|------|------|
| `agent/agent.go` | handleChat → SSE 流式响应 |
| `web/index_html.go` | Agent 聊天界面渲染工具调用 |

---

### Phase F：Web 能力（远期）
> 工作量：2-3 天 | 风险：中 | 前置：Phase C

**目标**：让 Agent 能搜索和获取互联网信息。

**内容**：
1. `web_search` — 调用搜索 API
2. `web_fetch` — HTTP GET + HTML 转 Markdown

**暂不排期**，需要先确定搜索 API 的接入方案。

---

## 三、总览与排期

```
                    ┌──────────────────────────────────────┐
    已完成 ✅       │ 核心工具（read/list/grep）            │
                    │ SubAgent（explore/delegate_task）     │
                    │ 上下文治理（截断/清理/缓存/压缩）      │
                    │ Team 全套（审核/架构师/书记员/交接）    │
                    │ 记忆系统（公共+角色+Agent桥接）        │
                    └──────────────────────────────────────┘
                                     │
                    ┌────────────────┼───────────────────┐
                    ▼                ▼                   ▼
              Phase A (0.5天)   Phase B (1.5天)     Phase E (1.5天)
              glob_search       edit_file           前端工具展示
              文件名搜索        write_file          Agent SSE
                    │           文件写入
                    │                │
                    │           ┌────┴─────┐
                    │           ▼          ▼
                    │     Phase C (1.5天)  Phase D (1天)
                    │     run_command     执行引擎升级
                    │     Shell 命令      读写分流+分级截断
                    │           │
                    │           ▼
                    │     Phase F (2-3天)
                    │     web_search
                    │     web_fetch
                    │
                    └──── 总计约 8 天（A-E 核心约 6 天）
```

### 建议实施顺序

| 顺序 | Phase | 工作量 | 核心收益 |
|------|-------|--------|---------|
| 1 | **A — glob_search** | 0.5 天 | 补齐"文件名搜索"能力，零风险 |
| 2 | **B — edit/write_file** | 1.5 天 | 从只读→读写，编码者能真正写代码 |
| 3 | **D — 执行引擎升级** | 1 天 | 读写工具安全并发，分级截断 |
| 4 | **C — run_command** | 1.5 天 | 完整闭环：改代码→编译→看错误→再改 |
| 5 | **E — 前端展示** | 1.5 天 | Agent 模式体验提升 |
| 6 | **F — Web 能力** | 2-3 天 | 远期，按需排入 |

### 每 Phase 完成后的能力矩阵

| 能力 | 当前 | +A | +B | +D | +C | +E |
|------|------|-----|-----|-----|-----|-----|
| 文件名搜索 | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 正则搜索 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 读取文件 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 子 Agent 探索 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 编辑文件 | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ |
| 创建文件 | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ |
| 读写工具安全并发 | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ |
| 执行命令 | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ |
| Agent 模式 SSE | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ |

---

## 四、新工具与 SubAgent/Team 的整合矩阵

新工具加入后，各角色和子 Agent 的工具权限：

| 工具 | explore 子AG | delegate 子AG | 研究员 | 编码者 | 架构师 | 审核者 |
|------|-------------|--------------|--------|--------|--------|--------|
| read_file | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| list_files | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| grep_search | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| glob_search | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| edit_file | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| write_file | ❌ | ❌ | ❌ | ✅ | ✅* | ❌ |
| run_command | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| explore | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ |
| delegate_task | ❌ | ❌ | ✅ | ✅ | ✅ | ❌ |

*架构师可以 write_file 来创建设计文档，但不能 edit_file 修改代码。

**实现方式**：通过 `SubAgentAllowed` + Worker 角色的工具白名单控制。

---

## 五、与两份原始文档的对照

### 5.1 TOOL_SYSTEM_DESIGN.md 状态更新

| 原规划 | 原状态 | 实际状态 | 说明 |
|--------|--------|---------|------|
| P0 核心框架 | ✅ | ✅ | 无变化 |
| P1+ Team 集成 | ✅ | ✅ | 无变化 |
| P2 上下文治理 | ⏳ 待实施 | **✅ 已完成** | DefaultMaxResultChars=1500 + per-tool限制, KeepRecentToolResults=3, ReadCache(per-session上下文), 200行限制, 历史压缩, SubAgent结果保护 |
| P3 前端展示 | ⏳ | ⏳ → **Phase E** | 纳入本路线图 |

### 5.2 TOOL_INTEGRATION_PLAN.md 状态更新

| 原规划 | 原状态 | 实际状态 | 说明 |
|--------|--------|---------|------|
| Phase 1 glob_search | 未开始 | **→ Phase A** | 保持，0.5 天 |
| Phase 1 grep ripgrep | 未开始 | **降级为可选** | 当前 grep 够用，rg 升级收益有限 |
| Phase 2 edit/write_file | 未开始 | **→ Phase B** | 保持，1.5 天 |
| Phase 3 run_command | 未开始 | **→ Phase C** | 保持，1.5 天 |
| Phase 4 执行引擎升级 | 未开始 | **⚠️ 部分完成 → Phase D** | SubAgent 并行已完成，读写分流待实现 |
| Phase 5 SubAgent | 未开始 | **✅ 已完成** | explore + delegate_task + 并行 + 降级 |
| Phase 6 Web 能力 | 未开始 | **→ Phase F** | 远期 |

### 5.3 计划外的已完成工作

以下功能在两份原始文档中均未规划，但已经实现：

| 功能 | 说明 |
|------|------|
| 通用审核工作流 | 所有 Worker 输出经审核者审核 |
| 架构师角色 + 大功能 Pipeline | 调研→架构设计→编码，每阶段审核 |
| 书记员角色 | 公共记忆池唯一写入者 |
| Worker 交接消息 | 完成后 100 字摘要 + @下一流程 |
| 记忆系统 | 公共记忆池 + 角色经验 + Agent 桥接 |
| autoReviewResults 返工修复 | 返工成功后替换 NEEDS_REWORK 结果 |
| 返工失败标记 | ReworkFailed 字段 + Leader 提示不重试 |

---

## 六、关键设计决策

### 6.1 写入工具的子 Agent 权限

**决策**：SubAgent 禁止写入。

理由：
- 子 Agent 没有审核机制，不受 Reviewer 监督
- 写操作出错的代价远高于读操作
- 编码者应自己执行写入，可被审核
- 与 Claude Code 的 Explore Agent 设计一致（disallowedTools 包含 FileEdit/FileWrite）

### 6.2 run_command 默认关闭

**决策**：`TOOL_COMMAND_ENABLED` 默认 `false`。

理由：
- 命令执行是最高风险操作
- 安全模型（黑白名单）是简化版，非生产级
- 用户需要显式开启并理解风险

### 6.3 分级截断 vs 统一截断

**决策**：Phase D 实现分级截断，替代当前的统一 `MaxToolResultChars`。

理由：
- read_file 已有 200 行限制，不需要再截断
- run_command 输出比文件内容更紧凑，可以给更大空间
- edit_file 的 diff 摘要本身就很短

### 6.4 grep 是否升级为 ripgrep

**决策**：暂不升级，作为可选优化。

理由：
- 当前 grep 功能正常，项目规模不大
- 升级需要确认服务器有 rg
- 收益有限，不阻塞其他工作
