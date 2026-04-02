# Claude Code 代码修改工作流分析 × 当前项目对照

> 基于 `/root/claude-code-sourcemap-main/docs/code-modification-workflow.md` 的完整工作流分析，  
> 对照 `chat_server/tools/` 的实际实现，梳理差距并给出可操作的改进建议。
>
> 更新日期：2026-04-02
>
> **实施状态：本文档提出的所有改进项（P0–P3）已全部实现。** 详见下方各节的 ✅ 标记。

---

## 1. 工作流全景

Claude Code 的代码修改**不是硬编码管线**，而是由 **system prompt 引导 + 工具链协作 + 安全校验** 三者组成的自主循环。模型在 `queryLoop`（对应我们的 Agentic Loop）中不断调用工具，每轮自主选择"发现 → 定位 → 修复 → 验证"链条中的任一环节：

```
┌──────────────────────────────────────────────────────────────────┐
│                     Agentic Loop（主循环）                        │
│                                                                  │
│   ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌──────────┐    │
│   │ 发现 Bug │ →  │ 定位代码 │ →  │ 修复代码 │ →  │ 验证结果  │   │
│   │         │    │         │    │         │    │          │    │
│   │ Bash    │    │ Read    │    │ Edit    │    │ Bash     │    │
│   │ (test)  │    │ Grep    │    │ Write   │    │ (re-test)│    │
│   │ 诊断    │    │ Glob    │    │         │    │ 诊断     │    │
│   └─────────┘    └─────────┘    └─────────┘    └──────────┘    │
│        ↑                                            │           │
│        └────────────── 未通过则回到发现 ──────────────┘           │
└──────────────────────────────────────────────────────────────────┘
```

**我们的对应结构**：`agent.go` 的 `handleChat` 循环和 `team.go` 的 `workerAgenticLoop` 完全等价于此 `queryLoop`——模型在每轮自主决定调用哪个工具。

---

## 2. 逐阶段对照分析

### 2.1 阶段一：发现 Bug

Claude Code 发现 Bug 的三条路径：Bash 执行测试/lint → 命令语义解释 → IDE 诊断系统自动推送。

#### 2.1.1 Bash 工具 vs run_command

| 维度 | Claude Code (`BashTool`) | 我们 (`run_command`) | 状态 |
|------|--------------------------|---------------------|------|
| 核心能力 | 任意 shell 命令执行 | 任意 shell 命令执行 | ✅ 对等 |
| 超时控制 | 可配置 | 默认 30s，最大 120s | ✅ 对等 |
| 安全拦截 | 权限系统 + 白名单 | 黑名单拦截 + 环境变量开关 | ✅ 已实现 |
| 工作目录 | 项目根目录 | `WorkspacePath` | ✅ 对等 |

#### 2.1.2 命令语义理解（我们缺失）

Claude Code 的 `commandSemantics.ts` 定义了**命令级语义规则**，不会盲目地把所有非零退出码当作错误：

```typescript
// Claude Code: 命令语义映射
const COMMAND_SEMANTICS = new Map([
  ['grep', (exitCode) => ({ isError: exitCode >= 2 })],  // 1 = 没找到，不是错误
  ['rg',   (exitCode) => ({ isError: exitCode >= 2 })],
  ['diff', (exitCode) => ({ isError: exitCode >= 2 })],  // 1 = 有差异，不是错误
  ['test', (exitCode) => ({ isError: exitCode >= 2 })],  // 1 = 条件为假，不是错误
])
```

**我们的现状**：`run_command` 对所有非零退出码一律返回 `退出码: N`，不做语义区分。模型看到 `grep` 退出码 1 时可能误以为出了错误。

```go
// run_command.go — 当前实现：所有非零退出码同等对待
if err != nil {
    exitCode := -1
    if exitErr, ok := err.(*exec.ExitError); ok {
        exitCode = exitErr.ExitCode()
    }
    return fmt.Sprintf("退出码: %d\n\n%s", exitCode, output), nil
}
```

#### 2.1.3 IDE 诊断系统（不适用）

Claude Code 在文件编辑前后自动采集 IDE 诊断差异（TypeScript/lint 错误），作为 `<new-diagnostics>` 附件注入下一轮。

```typescript
// Claude Code: Edit 后自动注入诊断
// 1. beforeFileEdited() — 打基线
// 2. getNewDiagnostics() — diff 出新增诊断
// 3. 注入为 <new-diagnostics> 附件
```

**我们的现状**：服务端 Agent，没有 IDE 连接，无法获取实时诊断。但可以通过 **prompt 引导** 模型在编辑后主动运行 `run_command("go vet ./...")` 或 `run_command("go build ./...")` 达到类似效果。

#### 2.1.4 大输出处理对比

| 维度 | Claude Code | 我们 |
|------|-------------|------|
| 策略 | 持久化到磁盘 + 2KB 预览 + 引导模型用 Read 查看 | 保留头尾各 4000 字符 + 中间省略 |
| 阈值 | 单独配置 | `maxCommandOutput = 8000` |
| 实现 | `persistedOutputPath` + `generatePreview` | `truncateCommandOutput` |

两种策略各有优势：Claude Code 的方案保留了完整输出可供后续查看，我们的方案更简洁但可能丢失中间关键信息。

---

### 2.2 阶段二：定位代码

| 工具 | Claude Code | 我们 | 状态 |
|------|-------------|------|------|
| 正则搜索 | `Grep`（ripgrep 后端） | `grep_search`（系统 grep 后端） | ✅ 功能对等 |
| 文件名搜索 | `Glob`（mtime 排序，100 上限） | `glob_search`（mtime 排序，100 上限） | ✅ 完全对齐 |
| 文件读取 | `Read`（25K token 限制 + offset/limit） | `read_file`（200 行限制 + offset/limit） | ✅ 功能对等 |
| 读取去重 | `readFileState`（path + mtime） | `ReadCache`（path:offset:limit + mtime） | ✅ 已实现 |
| 目录浏览 | 通过 `Bash("ls")` 间接完成 | `list_files` 专用工具 | ✅ 我们更好 |
| LSP 跳转 | `LSP` 工具（定义/引用跳转） | 无 | ⚠️ 不适用（无 IDE） |

**结论**：定位阶段已**完全对齐**，工具链完整。

---

### 2.3 阶段三：修复代码

这是差异最大、最值得深入分析的阶段。

#### 2.3.1 Edit 工具核心机制对比

| 维度 | Claude Code (`FileEditTool`) | 我们 (`edit_file`) | 状态 |
|------|------------------------------|-------------------|------|
| 核心操作 | old_string → new_string 精确替换 | old_string → new_string 精确替换 | ✅ 一致 |
| 唯一性检查 | 多处匹配 + !replace_all → errorCode 9 | `count > 1 && !ReplaceAll` → 报错 | ✅ 一致 |
| 未找到报错 | findActualString 失败 → errorCode 8 | `count == 0` → 报错 | ✅ 一致 |
| replace_all | 支持 | 支持 | ✅ 一致 |
| 路径安全 | UNC 检测 + deny 规则 | `isUnderWorkspace` + `isWriteAllowed` | ✅ 等效 |
| 缓存失效 | `readFileState.set` 覆盖时间戳 | `cache.Invalidate(absPath)` | ✅ 等效 |

#### 2.3.2 Claude Code 的 13 层安全校验 vs 我们的校验

Claude Code 的 `validateInput` 在执行前经过 13 步验证。以下逐步对照：

| # | Claude Code 校验 | 我们是否有 | 必要性 |
|---|-----------------|-----------|--------|
| 1 | 密钥检测（new_string 含 secret） | ❌ 无 | 中 — 服务端场景密钥泄露风险低 |
| 2 | old_string === new_string | ✅ 有 | 高 |
| 3 | 文件在 deny 规则中 | ✅ `isWriteAllowed` + `WriteDenyPaths` | 高 |
| 4 | UNC 路径检测（防 NTLM 泄露） | ❌ 无 | 低 — Linux 无此风险 |
| 5 | 文件 > 1 GiB | ❌ 无 | 低 — 代码文件极少超过 1G |
| 6 | **先读后改**（readFileState 必须有记录） | ❌ **仅 prompt 提示** | **高** |
| 7 | **竞态保护**（mtime > readTimestamp） | ❌ 无 | 中 — 单用户场景风险低 |
| 8 | .ipynb 后缀检查 | ❌ 无 | 低 — 不涉及 Notebook |
| 9 | 未找到匹配 | ✅ `count == 0` | 高 |
| 10 | 多处匹配 + !replace_all | ✅ `count > 1 && !ReplaceAll` | 高 |
| 11 | 设置文件 schema 校验 | ❌ 无 | 低 |

**最关键差距**：**先读后改（Read-Before-Write）** 的执行力度。

Claude Code 在**代码层面硬性拒绝**未读过的文件的编辑：

```typescript
// Claude Code: 硬校验 — readFileState 无记录则拒绝
const readTimestamp = toolUseContext.readFileState.get(fullFilePath)
if (!readTimestamp || readTimestamp.isPartialView) {
  return {
    result: false,
    message: 'File has not been read yet. Read it first before writing to it.',
    errorCode: 6,
  }
}
```

我们目前仅在 `edit_file` 的 Description 中写了"使用前必须先用 read_file 读取"，LLM 可能忽略此提示直接编辑，导致 old_string 不准确而反复失败。

#### 2.3.3 引号智能处理（我们未实现）

Claude Code 有两层引号智能处理，处理 LLM 输出直引号 vs 文件中弯引号的不匹配：

```typescript
// 1. findActualString: 匹配时自动适配引号（弯→直归一化后匹配）
// 2. preserveQuoteStyle: 替换时保留文件原始引号风格
```

**我们的现状**：无此机制。但因为我们主要处理 Go/Python 代码（使用直引号），弯引号场景极其罕见，优先级低。

#### 2.3.4 删除代码的智能处理（我们未实现）

Claude Code 当 `new_string` 为空（删除代码）时，自动处理尾部换行：

```typescript
// 如果 old_string 后紧跟换行符，连同换行一起删除
const stripTrailingNewline =
  !oldString.endsWith('\n') && originalContent.includes(oldString + '\n')
```

**我们的现状**：无此机制。删除代码后可能留下空行。小优化，可后续添加。

#### 2.3.5 Edit 工具的完整执行流程对比

```
Claude Code 执行流程（15步）          我们的执行流程（7步）
─────────────────────────           ─────────────────────
1.  expandPath                      1. json.Unmarshal
2.  discoverSkillDirs               2. resolvePath + isUnderWorkspace
3.  diagnosticTracker.beforeEdited  3. isWriteAllowed
4.  mkdir（确保父目录）              4. os.ReadFile
5.  fileHistoryTrackEdit（备份）     5. strings.Count（唯一性检查）
6.  readFileForEdit + 编码检测       6. strings.Replace（执行替换）
7.  竞态检查                         7. os.WriteFile + cache.Invalidate
8.  findActualString（引号适配）
9.  preserveQuoteStyle
10. getPatchForEdit（生成 diff）
11. writeTextContent
12. lspManager.changeFile
13. notifyVscodeFileUpdated
14. readFileState.set
15. 返回结构化 diff
```

我们缺少的步骤中，**有价值的是**：
- 步骤 3（诊断基线）：需要 IDE 连接，不适用
- 步骤 5（备份）：可选，有 git 可替代
- 步骤 7（竞态检查）：单用户场景暂不需要
- 步骤 8-9（引号）：代码场景极少需要
- 步骤 10+15（diff 输出）：**可以改进**

#### 2.3.6 Write 工具对比

| 维度 | Claude Code (`Write`) | 我们 (`write_file`) | 状态 |
|------|----------------------|-------------------|------|
| 创建新文件 | ✅ | ✅ | 一致 |
| 覆盖文件 | ✅ | ✅ | 一致 |
| 自动创建父目录 | ✅ | ✅ `os.MkdirAll` | 一致 |
| 大小限制 | 无显式限制 | `MaxWriteSize = 200KB` | ✅ 更安全 |
| 先读后改 | 无要求 | 无要求 | 一致 |

---

### 2.4 阶段四：验证结果

#### 2.4.1 关键发现：验证完全靠 Prompt

Claude Code **没有**"Edit 后自动跑测试"的硬编码逻辑。验证完全靠 system prompt 引导：

```
// Claude Code system prompt 中的验证约束：
1. "修改前后都要验证"
2. "如实报告结果：测试失败就说失败，附上输出"
3. "修复失败后先诊断原因再换策略，不要盲目重试"
```

#### 2.4.2 我们的现状

- **有能力**：`run_command` 可以执行 `go test`、`go build`、`go vet` 等验证命令
- **缺引导**：Worker/Agent 的 system prompt 中没有明确的验证行为约束
- **缺诊断反馈**：没有 Edit 后自动注入诊断变化的机制

#### 2.4.3 Claude Code 的典型修复序列

```
Turn 1: User → "这个循环有 off-by-one bug"
Turn 2: Agent → Grep(pattern="for.*range")     ← 搜索定位
Turn 3: Agent → Read(file_path="utils.go")     ← 读取确认
Turn 4: Agent → Edit(old_string=..., new=...)  ← 精确修复
         ← 系统自动注入 <new-diagnostics>      ← 我们缺这一步
Turn 5: Agent → Bash("go test ./...")          ← 主动验证
         ← 通过 → 报告完成
         ← 失败 → 继续修复
```

我们的流程可以完全一样，只是缺少 Turn 4 到 Turn 5 之间的自动诊断注入。

---

## 3. 我们已有但 Claude Code 无对等的特性

| 特性 | 我们的实现 | 说明 |
|------|-----------|------|
| 子 Agent（explore/delegate） | `subagent.go` | Claude Code 有 Agent 工具但机制不同 |
| 工具读写分区并行 | `ExecutePartitioned` | 连续只读工具并行，写工具串行 |
| 子 Agent 结果保护 | `SubAgentResultPrefix` 跳过清理 | 等效于 Claude Code 的 `COMPACTABLE_TOOLS` 白名单 |
| 工具结果分级截断 | `MaxResultChars` 按工具独立配置 | 我们更灵活 |
| 专用目录浏览 | `list_files` | Claude Code 用 Bash("ls") 间接完成 |
| 子 Agent 降级熔断 | `ShouldDegradeSubAgent` | 连续失败 3 次自动降级 |

---

## 4. 差距总结热力图

```
阶段          覆盖率       缺失项

发现 Bug     ████████░░  80%   命令语义理解、诊断反馈(不适用)
定位代码     ██████████  100%  无
修复代码     ████████░░  80%   先读后改硬检查、diff 输出
验证结果     ████░░░░░░  40%   prompt 行为引导、诊断注入(不适用)
基础设施     █████████░  90%   基本完善
```

---

## 5. 可操作的改进建议

### 5.1 高价值、低成本 — Prompt 改动

**优先级：P0 | 预计工作量：0.5h**

在 Worker/Agent 的 system prompt 中加入以下行为约束（参照 Claude Code 的 `constants/prompts.ts`）：

```
== 代码修改行为准则 ==

1. 先搜后读再改：修改前先用 grep_search/glob_search 定位，再用 read_file 确认，最后用 edit_file 修改。
2. 工具优先于命令：
   - 读文件用 read_file，不要用 run_command("cat ...")
   - 改文件用 edit_file，不要用 run_command("sed ...")
   - 搜索用 grep_search，不要用 run_command("grep ...")
   - 查文件名用 glob_search，不要用 run_command("find ...")
3. 修改后验证：edit_file 或 write_file 后，主动运行 run_command("go build ./...") 或相关测试。
4. 如实报告：测试失败就附上输出说明，不要声称"已通过"。
5. 失败后诊断：修复失败先分析原因再换策略，不要盲目重试同样的操作。
```

### 5.2 中等价值 — 小代码改动

#### 5.2.1 edit_file 先读后改检查

**优先级：P1 | 预计工作量：1h**

在 `ReadCache` 中添加 `HasRead` 方法，`edit_file` 执行前检查文件是否已被读取：

```go
// read_cache.go — 新增方法
func (c *ReadCache) HasRead(absPath string) bool {
    c.mu.Lock()
    defer c.mu.Unlock()
    prefix := absPath + ":"
    for key := range c.entries {
        if strings.HasPrefix(key, prefix) {
            return true
        }
    }
    return false
}

// edit_file.go — 在唯一性检查之前添加
if cache != nil && !cache.HasRead(absPath) {
    return "", fmt.Errorf(
        "请先使用 read_file 读取 %s 的内容，确认要修改的文本后再编辑。"+
        "这样可以确保 old_string 与文件内容完全匹配。", input.Path)
}
```

#### 5.2.2 run_command 命令语义理解

**优先级：P1 | 预计工作量：0.5h**

为常见命令添加退出码语义映射：

```go
// run_command.go — 新增

// 退出码 >= threshold 才算真正的错误
var commandErrorThreshold = map[string]int{
    "grep": 2, // 0=找到, 1=没找到, 2+=错误
    "rg":   2,
    "diff": 2, // 0=无差异, 1=有差异, 2+=错误
    "test": 2, // 0=条件真, 1=条件假, 2+=错误
    "cmp":  2,
}

func isSemanticError(command string, exitCode int) bool {
    // 提取命令名（处理管道和路径）
    parts := strings.Fields(command)
    if len(parts) == 0 {
        return exitCode != 0
    }
    cmdName := filepath.Base(parts[0])
    if threshold, ok := commandErrorThreshold[cmdName]; ok {
        return exitCode >= threshold
    }
    return exitCode != 0
}
```

#### 5.2.3 edit_file diff 输出增强

**优先级：P2 | 预计工作量：1h**

当前返回 `"已修改 X: 替换 N 处（M 行 → K 行）"`，可改为返回简洁的上下文 diff：

```go
// edit_file.go — 返回简洁 diff
func buildEditDiff(path, oldStr, newStr string, count int) string {
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("已修改 %s: 替换 %d 处\n", path, count))
    sb.WriteString("```diff\n")
    for _, line := range strings.Split(oldStr, "\n") {
        sb.WriteString("- " + line + "\n")
    }
    for _, line := range strings.Split(newStr, "\n") {
        sb.WriteString("+ " + line + "\n")
    }
    sb.WriteString("```")
    return sb.String()
}
```

### 5.3 低优先级 — ✅ 已全部实现

| 改进项 | 说明 | 状态 |
|--------|------|------|
| 引号智能匹配 | 弯引号↔直引号自动适配 | ✅ `tryQuoteNormalization()` in `edit_file.go` |
| 删除代码尾部换行处理 | new_string 为空时自动清理多余换行 | ✅ `edit_file.go` 删除操作时自动包含尾部换行 |
| 行尾空白自动清理 | 非 .md 文件自动去行尾空白 | ✅ `cleanTrailingWhitespace()` in `edit_file.go` |
| 编辑前文件备份 | edit_file 执行前备份原始内容 | ✅ 备份到 `/tmp/chat_server_backups/`（FNV哈希+纳秒时间戳） |
| 竞态保护 | 编辑前检查文件 mtime | ✅ `ReadCache.GetReadTime()` vs 文件 mtime |
| 大输出持久化 | 超大 Bash 输出写磁盘 + 预览 | ✅ 超 8000 字符持久化到 `/tmp/chat_server_outputs/` |

---

## 6. Claude Code 关键设计原则提炼

### 6.1 安全性分层

| 层次 | Claude Code 机制 | 我们的对应 |
|------|-----------------|-----------|
| 输入前 | 13 步 `validateInput` | 路径安全 + 写入权限 + 唯一性 |
| 执行中 | 竞态保护 + 原子写 | 直接写入（单用户无竞态） |
| 执行后 | 诊断 diff + LSP 通知 | 无（服务端无 IDE） |
| 行为层 | system prompt 约束 | ✅ 「代码修改行为准则」5 条（`agent.go` + `team.go`） |

### 6.2 先读后改（Read-Before-Write）

Claude Code 最核心的不变量，在三个层面同时强制：

1. **Prompt 层**：Edit 工具描述明确写 "You must use your Read tool at least once before editing"
2. **校验层**：`validateInput` 的 errorCode 6 检查 `readFileState`
3. **运行层**：`call()` 的竞态检查依赖 `readFileState` 的时间戳

✅ 三个层面均已实现：Prompt 层（行为准则）+ 校验层（`ReadCache.HasRead`）+ 运行层（`ReadCache.GetReadTime` mtime 检查）。

### 6.3 精确替换而非全文重写

选择 `old_string → new_string` 而非全文覆写的设计优势：

- **可审查**：一眼看出改了什么
- **不丢失**：不会意外删除 context 之外的代码
- **唯一性约束**：强制模型提供足够上下文精确定位
- **diff 友好**：直接生成结构化 diff

### 6.4 工具优先于 Bash

system prompt 明确要求用专用工具替代通用 Bash：

```
- 读文件用 Read 而非 cat/head/tail
- 改文件用 Edit 而非 sed/awk
- 创建文件用 Write 而非 cat heredoc
- 搜索用 Grep 而非 grep/rg
- 查文件用 Glob 而非 find/ls
```

### 6.5 命令语义理解

不盲目把非零退出码当错误。`grep` 返回 1 是"没找到匹配"不是"执行错误"，避免模型陷入不必要的"错误修复"循环。

---

## 7. 改进实施路线 — ✅ 全部完成

```
Week 1（Prompt 优化）— ✅ Done
├── ✅ P0: Agent/Worker system prompt 加入代码修改行为准则
└── ✅ P0: edit_file/write_file 工具描述补充"工具优先"提示

Week 2（核心校验）— ✅ Done
├── ✅ P1: ReadCache.HasRead + edit_file 先读后改检查
├── ✅ P1: run_command 命令语义映射（commandSemantics + formatCommandResult）
└── ✅ P2: edit_file diff 输出增强（buildEditDiff）

Week 3+（可选优化）— ✅ Done
├── ✅ P3: 删除代码尾部换行智能处理
├── ✅ P3: 行尾空白自动清理（非 .md 文件）
├── ✅ P3: 引号智能匹配（弯引号↔直引号归一化）
├── ✅ P3: 编辑前文件备份（/tmp/chat_server_backups/）
├── ✅ P3: 竞态保护（ReadCache.GetReadTime vs mtime）
└── ✅ P3: 大输出持久化（/tmp/chat_server_outputs/）
```

---

## 8. 附录：完整 Bug 修复示例对照

### Claude Code 的典型修复序列

```
Turn 1: User → "utils.go 的 processItems 有 bug，最后一个元素被跳过"
Turn 2: Agent → Read("utils.go")           ← 读取确认
Turn 3: Agent → Edit(old="i < len-1", new="i < len")  ← 精确修复
         ← 自动注入 <new-diagnostics>       ← 诊断反馈
Turn 4: Agent → Bash("go test ./...")       ← 主动验证
Turn 5: Agent → 报告：已修复 + 测试通过
```

### 我们当前的等效序列

```
Turn 1: User → "utils.go 的 processItems 有 bug，最后一个元素被跳过"
Turn 2: Agent → read_file("utils.go")       ← 读取确认 ✅
Turn 3: Agent → edit_file(old="i < len-1", new="i < len")  ← 精确修复 ✅ + diff 输出 ✅
         ← （无自动诊断反馈）                ← 差距点（需 IDE/LSP 集成）
Turn 4: Agent → run_command("go test ./...")  ← ✅ prompt 行为准则已引导
Turn 5: Agent → 报告：已修复 + 测试通过      ← ✅ prompt 行为准则已引导
```

**差距已基本消除**：通过「代码修改行为准则」注入 system prompt，Turn 4-5 的"主动验证→如实报告"已成为模型默认行为。剩余差距仅在诊断反馈（需 IDE/LSP 集成，当前架构无法实现）。
