# 工具扩展集成规划

> 基于 Claude Code 工具箱源码分析，为 chat_server 制定分批整合方案
>
> 参考源码：`/root/claude-code-sourcemap-main/restored-src/src/tools/`
> 参考文档：`/root/claude-code-sourcemap-main/docs/toolbox-analysis.md`
>
> 相关文档：
> - [current-architecture.md](./current-architecture.md) — 当前架构现状
> - [SUBAGENT_DESIGN.md](./SUBAGENT_DESIGN.md) — 子 Agent 设计（Phase 4）

---

## 一、现状与目标

### 1.1 当前工具清单

| 工具 | 类型 | 说明 |
|------|------|------|
| `read_file` | 只读 | 读取文件（带行号、offset/limit、200 行限制、ReadCache 去重） |
| `list_files` | 只读 | 树形目录列表（深度限制、条目限制） |
| `grep_search` | 只读 | 正则搜索（基于系统 grep，100 行结果限制） |

**核心缺失**：Agent 只能"看"项目，不能"改"。无法执行命令、编辑文件、搜索文件名模式。

### 1.2 Claude Code 工具矩阵（40+ 工具）

```
我们已有的（3）：     Read ≈ read_file, Grep ≈ grep_search, (list_files ≈ Glob 简化版)
高价值可移植（5）：   Bash, Edit, Write, Glob, Agent
中等价值（3）：       WebSearch, WebFetch, TodoWrite
低优先级/不需要（30+）：LSP, NotebookEdit, PowerShell, Cron 系列, MCP 系列, ...
```

### 1.3 分批原则

1. **每批独立可用**——每个 Phase 完成后就能立即投入使用
2. **只读先行**——先扩展搜索能力，再引入写操作
3. **安全递增**——写操作需要权限控制，命令执行需要沙箱
4. **参考但不照搬**——Claude Code 是 TypeScript 大型系统，我们取其精华用 Go 简洁实现

---

## 二、Phase 1：文件搜索增强（只读）

> 预计工作量：0.5 天 | 风险：低 | 前置依赖：无

### 2.1 目标

用 `glob_search` 替代或增强 `list_files`，支持按文件名模式快速定位文件。

### 2.2 新增工具：`glob_search`

**参考**：`restored-src/src/tools/GlobTool/GlobTool.ts`

Claude Code 的 Glob 工具核心能力：
- 按 glob 模式（`**/*.go`, `**/test_*.py`）搜索文件
- 结果按**修改时间排序**（最近修改的排前面）
- 最多返回 100 个结果

**工具定义**：

```json
{
  "name": "glob_search",
  "description": "按文件名模式搜索项目文件。支持 glob 语法（如 **/*.go, **/test_*.py）。结果按修改时间排序（最近修改的在前）。适合：知道文件名但不确定位置时快速定位。",
  "parameters": {
    "type": "object",
    "properties": {
      "pattern": {
        "type": "string",
        "description": "Glob 模式（如 **/*.go, src/**/config*.yaml）"
      },
      "path": {
        "type": "string",
        "description": "搜索目录（默认项目根目录）"
      }
    },
    "required": ["pattern"]
  }
}
```

**Go 实现要点**：

```go
// tools/glob_search.go
func executeGlobSearch(args json.RawMessage, _ *ReadCache) (string, error) {
    // 1. 解析参数
    // 2. 路径安全检查（isUnderWorkspace）
    // 3. 使用 filepath.Glob 或 doublestar 库匹配
    // 4. 对结果按 ModTime 降序排序
    // 5. 最多返回 100 个结果
    // 6. 输出相对路径列表
}
```

**依赖**：可能需要 `github.com/bmatcuk/doublestar/v4` 支持 `**` 递归匹配（Go 标准库 `filepath.Glob` 不支持 `**`）。

### 2.3 增强 `grep_search`：升级为 ripgrep

**参考**：`restored-src/src/tools/GrepTool/GrepTool.ts`

当前 `grep_search` 使用系统 `grep -rn -E`，Claude Code 使用 ripgrep (`rg`)。如果服务器有 `rg`，可以升级以获得更好的性能和功能：

| 特性 | 当前 grep | ripgrep |
|------|----------|---------|
| 速度 | 慢（大项目） | 快 10-100 倍 |
| .gitignore 支持 | 手动排除 | 自动尊重 |
| 输出模式 | 仅内容 | content / files_only / count |
| Unicode | 有限 | 完整支持 |

**改造方案**：检测 `rg` 是否存在，有则用 `rg`，否则回退 `grep`。新增 `output_mode` 参数。

### 2.4 文件变更

| 文件 | 变更 |
|------|------|
| `tools/glob_search.go`（新增） | glob_search 工具实现 |
| `tools/grep_search.go` | 可选：升级为 ripgrep 后端 |
| `go.mod` | 可选：添加 doublestar 依赖 |

### 2.5 Worker prompt 更新

```
可用搜索工具：
- grep_search：按内容搜索（正则匹配文件内容）
- glob_search：按文件名搜索（glob 模式匹配文件路径）
- list_files：目录结构概览（树形列表）

搜索策略：
- 知道文件名 → glob_search
- 知道代码内容 → grep_search
- 了解目录结构 → list_files
```

---

## 三、Phase 2：文件写入能力

> 预计工作量：1-1.5 天 | 风险：中 | 前置依赖：Phase 1（可选）

### 3.1 目标

让 Agent 能够编辑和创建文件，从"只读分析"升级为"读写操作"。

### 3.2 新增工具：`edit_file`

**参考**：`restored-src/src/tools/FileEditTool/FileEditTool.ts`

Claude Code 的 Edit 工具是**精确字符串替换**，不是行号编辑。这种设计更可靠——LLM 直接指定要替换的文本，不需要精确记住行号。

**工具定义**：

```json
{
  "name": "edit_file",
  "description": "对文件进行精确的字符串替换。必须先用 read_file 读取文件内容，然后指定要替换的原文和新文本。适合：修改代码、更新配置、修复 bug。注意：old_string 必须与文件中的内容完全匹配（包括缩进和空格）。",
  "parameters": {
    "type": "object",
    "properties": {
      "path": {
        "type": "string",
        "description": "文件路径，相对于项目根目录"
      },
      "old_string": {
        "type": "string",
        "description": "要替换的原始文本（必须与文件中的内容完全匹配）"
      },
      "new_string": {
        "type": "string",
        "description": "替换后的新文本"
      },
      "replace_all": {
        "type": "boolean",
        "description": "是否替换所有匹配（默认 false，只替换第一个）"
      }
    },
    "required": ["path", "old_string", "new_string"]
  }
}
```

**Go 实现要点**：

```go
// tools/edit_file.go
func executeEditFile(args json.RawMessage, cache *ReadCache) (string, error) {
    // 1. 解析参数 + 路径安全检查
    // 2. 前置检查：文件必须存在
    // 3. 读取文件内容
    // 4. 唯一性检查：old_string 在文件中出现次数
    //    - 0 次 → 报错"未找到匹配文本，请确认内容正确"
    //    - >1 次且 replace_all=false → 报错"匹配到 N 处，请使用 replace_all=true 或提供更多上下文"
    // 5. 执行替换
    // 6. 写回文件
    // 7. 清除 ReadCache（文件已变更）
    // 8. 返回简洁的 diff 摘要
}
```

**Claude Code 的 9 步验证链（简化版）**：

| 步骤 | Claude Code | 我们的实现 |
|------|-------------|-----------|
| 1 | 文件存在检查 | ✅ 实现 |
| 2 | 必须先 read_file | ⏭️ 跳过（用 prompt 引导） |
| 3 | old_string 唯一性 | ✅ 实现 |
| 4 | 空格/缩进校验 | ⏭️ 跳过（精确匹配已足够） |
| 5 | 文件时间戳检查（防并发修改） | ⏭️ 跳过（单用户场景） |
| 6 | .ipynb 检测 | ⏭️ 不需要 |
| 7 | settings 文件特殊检查 | ⏭️ 不需要 |
| 8 | 执行替换 | ✅ 实现 |
| 9 | diff 生成 | ✅ 简化版 |

### 3.3 新增工具：`write_file`

**参考**：`restored-src/src/tools/FileWriteTool/FileWriteTool.ts`

**工具定义**：

```json
{
  "name": "write_file",
  "description": "创建新文件或完全覆盖现有文件。注意：如果文件已存在，将被完全覆盖。修改现有文件请优先使用 edit_file 工具。适合：创建新文件、生成配置文件、写入全新内容。",
  "parameters": {
    "type": "object",
    "properties": {
      "path": {
        "type": "string",
        "description": "文件路径，相对于项目根目录"
      },
      "content": {
        "type": "string",
        "description": "文件的完整内容"
      }
    },
    "required": ["path", "content"]
  }
}
```

**Go 实现要点**：

```go
// tools/write_file.go
func executeWriteFile(args json.RawMessage, cache *ReadCache) (string, error) {
    // 1. 解析参数 + 路径安全检查
    // 2. 检查路径是否在白名单写入目录下（安全限制）
    // 3. 自动创建父目录（os.MkdirAll）
    // 4. 写入文件
    // 5. 清除 ReadCache
    // 6. 返回结果（新建 vs 覆盖，文件大小，行数）
}
```

### 3.4 安全控制

写操作需要额外的安全措施：

**写入白名单**（建议默认策略）：

```go
// tools/workspace.go
var (
    WriteEnabled    = true  // 总开关，可通过环境变量 TOOL_WRITE_ENABLED=false 关闭
    WriteDenyPaths  = []string{
        ".git/",           // 禁止直接修改 .git 目录
        "go.sum",          // 禁止修改依赖锁文件
    }
)

func isWriteAllowed(absPath string) error {
    if !WriteEnabled {
        return fmt.Errorf("文件写入功能已禁用")
    }
    for _, deny := range WriteDenyPaths {
        if strings.Contains(absPath, deny) {
            return fmt.Errorf("不允许修改 %s", deny)
        }
    }
    return nil
}
```

**最大写入大小限制**：

```go
const MaxWriteSize = 200 * 1024  // 200KB，防止 LLM 生成巨大文件
```

### 3.5 文件变更

| 文件 | 变更 |
|------|------|
| `tools/edit_file.go`（新增） | edit_file 工具实现 |
| `tools/write_file.go`（新增） | write_file 工具实现 |
| `tools/workspace.go` | 新增写入安全控制（WriteEnabled, WriteDenyPaths, MaxWriteSize） |
| `tools/tools.go` | 注册新工具 |

### 3.6 工具属性标记

参照 Claude Code，为工具添加只读/并发属性：

```go
type Tool struct {
    Name        string
    Description string
    Parameters  json.RawMessage
    Execute     func(args json.RawMessage, cache *ReadCache) (string, error)
    IsReadOnly  bool  // 新增：是否只读（影响并发和权限）
}
```

| 工具 | IsReadOnly |
|------|-----------|
| read_file | ✅ |
| list_files | ✅ |
| grep_search | ✅ |
| glob_search | ✅ |
| edit_file | ❌ |
| write_file | ❌ |

---

## 四、Phase 3：Shell 命令执行

> 预计工作量：1.5-2 天 | 风险：高 | 前置依赖：Phase 2

### 4.1 目标

让 Agent 能执行 shell 命令（编译、运行测试、git 操作等），是能力的质变。

### 4.2 新增工具：`run_command`

**参考**：`restored-src/src/tools/BashTool/BashTool.tsx` + `bashPermissions.ts`

Bash 是 Claude Code 中**权限系统最复杂的工具**。我们取其核心，做安全简化。

**工具定义**：

```json
{
  "name": "run_command",
  "description": "执行 shell 命令。适合：运行代码、执行测试、git 操作、安装依赖等。注意：命令在项目根目录下执行。长时间运行的命令有超时限制。",
  "parameters": {
    "type": "object",
    "properties": {
      "command": {
        "type": "string",
        "description": "要执行的 shell 命令"
      },
      "timeout": {
        "type": "integer",
        "description": "超时秒数（默认 30，最大 120）"
      },
      "description": {
        "type": "string",
        "description": "命令的简要说明（用于日志和安全审计）"
      }
    },
    "required": ["command"]
  }
}
```

### 4.3 安全模型：三层防护

**参考**：Claude Code 的 `bashPermissions.ts` 前缀规则 + `readOnlyValidation.ts`

```
命令输入
  │
  ├─ 第 1 层：黑名单拦截（硬拒绝）
  │   rm -rf /, mkfs, dd if=, chmod -R 777, :(){ :|:& };:
  │   → 直接拒绝，返回错误
  │
  ├─ 第 2 层：白名单自动放行（只读命令）
  │   ls, cat, head, tail, wc, file, stat, which, echo
  │   go build, go test, go vet, go fmt
  │   python *.py, pip list
  │   git status, git log, git diff, git branch
  │   → 自动允许
  │
  └─ 第 3 层：灰名单（需确认/限制）
  │   其他所有命令
  │   → 当前策略：允许但记录日志
  │   → 未来策略：可配置为需要用户确认
```

**Go 实现**：

```go
// tools/run_command.go

var commandBlacklist = []string{
    "rm -rf /", "rm -rf /*", "mkfs", "dd if=",
    "chmod -R 777 /", ":(){ :|:& };:",
    "> /dev/sda", "curl | sh", "wget | sh",
}

var readOnlyPrefixes = []string{
    "ls", "cat", "head", "tail", "wc", "file", "stat", "which", "echo", "pwd",
    "go build", "go test", "go vet", "go fmt", "go run",
    "python", "pip list", "pip show",
    "git status", "git log", "git diff", "git branch", "git show",
    "grep", "find", "tree", "du", "df",
}

func executeRunCommand(args json.RawMessage, _ *ReadCache) (string, error) {
    // 1. 解析参数
    // 2. 黑名单检查
    // 3. 超时控制（默认 30s，最大 120s）
    // 4. exec.CommandContext 执行（在 WorkspacePath 目录下）
    // 5. 捕获 stdout + stderr
    // 6. 结果截断（最多 MaxToolResultChars）
    // 7. 返回：exit code + output
}
```

### 4.4 输出管理

Claude Code 的 Bash 工具 `maxResultSizeChars = 30,000`，超长输出会持久化到磁盘。我们简化为截断 + 提示：

```go
const maxCommandOutput = 8000  // 字符

func truncateOutput(output string) string {
    runes := []rune(output)
    if len(runes) <= maxCommandOutput {
        return output
    }
    // 保留头尾各一半
    half := maxCommandOutput / 2
    return string(runes[:half]) +
        fmt.Sprintf("\n\n... 省略 %d 字符 ...\n\n", len(runes)-maxCommandOutput) +
        string(runes[len(runes)-half:])
}
```

### 4.5 只读分类（影响并发）

参照 Claude Code 的 `isReadOnly` 判断：只读命令可以并发执行，写命令需要串行。

```go
func isReadOnlyCommand(cmd string) bool {
    cmd = strings.TrimSpace(cmd)
    for _, prefix := range readOnlyPrefixes {
        if strings.HasPrefix(cmd, prefix) {
            return true
        }
    }
    return false
}
```

### 4.6 文件变更

| 文件 | 变更 |
|------|------|
| `tools/run_command.go`（新增） | run_command 工具实现 |
| `tools/command_permissions.go`（新增） | 黑名单/白名单规则 |
| `tools/tools.go` | 注册新工具、IsReadOnly 属性 |

### 4.7 环境变量控制

```
TOOL_COMMAND_ENABLED=true|false    # 总开关（默认 false，需显式开启）
TOOL_COMMAND_TIMEOUT=30            # 默认超时秒数
TOOL_COMMAND_MAX_TIMEOUT=120       # 最大超时秒数
```

---

## 五、Phase 4：工具执行引擎升级

> 预计工作量：1 天 | 风险：中 | 前置依赖：Phase 2

### 5.1 目标

升级工具执行引擎，支持只读工具并行、写工具串行、结果大小精细管控。

### 5.2 并行/串行执行

**参考**：`restored-src/src/services/tools/toolOrchestration.ts`

Claude Code 的编排策略：
1. 扫描一轮中所有 tool_calls
2. 连续的只读工具组成一个**并行批次**
3. 遇到非只读工具时切换为**串行执行**
4. 并行上限 10 个

当前 `workerAgenticLoop` 中工具是**串行逐个执行**的。改造方案：

```go
func executeToolCalls(toolCalls []llm.ToolCall, cache *ReadCache) []toolResult {
    batches := partitionByConcurrency(toolCalls)
    var results []toolResult
    for _, batch := range batches {
        if batch.isConcurrencySafe {
            results = append(results, runParallel(batch.calls, cache)...)
        } else {
            for _, tc := range batch.calls {
                results = append(results, runSingle(tc, cache))
            }
        }
    }
    return results
}
```

### 5.3 结果大小分级管控

**参考**：`restored-src/src/constants/toolLimits.ts` + `restored-src/src/utils/toolResultStorage.ts`

Claude Code 的分级策略：

| 工具 | 结果上限 | 超限处理 |
|------|---------|---------|
| Read | ∞（内部 25K token 限制） | 报错引导 |
| Grep | 20K 字符 | 持久化到磁盘，上下文保留 2K 预览 |
| Bash | 30K 字符 | 持久化到磁盘，上下文保留 2K 预览 |
| 其他 | 100K 字符 | 持久化到磁盘 |

**我们的简化方案**：去掉磁盘持久化（复杂度高、我们场景不需要），改为分级截断：

```go
// 各工具独立的结果上限
var toolResultLimits = map[string]int{
    "read_file":    0,     // 不走统一截断，自行控制（200 行限制）
    "grep_search":  3000,
    "glob_search":  3000,
    "list_files":   3000,
    "run_command":  8000,  // 命令输出允许更大
    "edit_file":    1500,
    "write_file":   500,
}
```

移除 `tools.go` 中的统一 `MaxToolResultChars` 截断，改为各工具在 `Execute` 函数内自行控制，或在注册时声明上限。

### 5.4 Tool 接口升级

```go
type Tool struct {
    Name              string
    Description       string
    Parameters        json.RawMessage
    Execute           func(args json.RawMessage, cache *ReadCache) (string, error)
    IsReadOnly        bool
    MaxResultChars    int   // 0 = 不截断（工具自行控制）
}
```

### 5.5 文件变更

| 文件 | 变更 |
|------|------|
| `tools/tools.go` | Tool 接口升级 + 分级截断 + 并行执行支持 |
| `agent/agent.go` | handleChat 中使用新的并行执行函数 |
| `team/team.go` | workerAgenticLoop 中使用新的并行执行函数 |

---

## 六、Phase 5：子 Agent 系统

> 预计工作量：1-2 天 | 风险：中 | 前置依赖：Phase 4

详见 [SUBAGENT_DESIGN.md](./SUBAGENT_DESIGN.md)，此处仅列出与工具扩展的关联点。

### 6.1 与新工具的关系

子 Agent 工具集配置：

| 子 Agent 类型 | 可用工具 |
|---------------|---------|
| `explore` | read_file, list_files, grep_search, glob_search（只读） |
| `delegate_task` | 全部工具（含 edit_file, write_file, run_command） |

### 6.2 子 Agent 不可用的工具

参照 Claude Code 的 `ALL_AGENT_DISALLOWED_TOOLS`：
- `explore` / `delegate_task`（禁止递归）
- 未来的用户交互工具

---

## 七、Phase 6：Web 能力（远期）

> 预计工作量：2-3 天 | 风险：中 | 前置依赖：Phase 3

### 7.1 `web_search` — 联网搜索

**参考**：`restored-src/src/tools/WebSearchTool/WebSearchTool.ts`

让 Agent 能搜索实时信息。实现方案：调用搜索 API（Google Custom Search / Bing / SerpAPI）。

```json
{
  "name": "web_search",
  "description": "搜索互联网获取实时信息。适合：查询最新文档、API 参考、错误信息排查。",
  "parameters": {
    "properties": {
      "query": { "type": "string", "description": "搜索关键词" }
    },
    "required": ["query"]
  }
}
```

### 7.2 `web_fetch` — URL 内容抓取

**参考**：`restored-src/src/tools/WebFetchTool/WebFetchTool.ts`

让 Agent 能读取网页或 API 文档。

```json
{
  "name": "web_fetch",
  "description": "获取指定 URL 的内容。适合：读取在线文档、API 响应、网页内容。",
  "parameters": {
    "properties": {
      "url": { "type": "string", "description": "要获取的 URL" },
      "prompt": { "type": "string", "description": "对内容的处理指令（如：提取关键 API 说明）" }
    },
    "required": ["url"]
  }
}
```

**实现方案**：HTTP GET + HTML 转 Markdown（用 `html-to-markdown` 类库）+ LLM 摘要（可选）。

---

## 八、总览与排期

```
Phase 1: 文件搜索增强     ─── 0.5 天 ─── 只读，零风险
  └─ glob_search + grep_search 增强

Phase 2: 文件写入能力     ─── 1.5 天 ─── 写操作，需安全控制
  └─ edit_file + write_file

Phase 3: Shell 命令执行   ─── 2 天   ─── 高风险，三层防护
  └─ run_command

Phase 4: 执行引擎升级     ─── 1 天   ─── 基础设施
  └─ 并行执行 + 分级截断 + Tool 接口升级

Phase 5: 子 Agent 系统    ─── 2 天   ─── 架构级
  └─ explore + delegate_task

Phase 6: Web 能力          ─── 3 天   ─── 远期
  └─ web_search + web_fetch
                              ─────────
                         总计约 10 天
```

### 每 Phase 完成后的能力对照

| 能力 | 当前 | +P1 | +P2 | +P3 | +P4 | +P5 |
|------|------|-----|-----|-----|-----|-----|
| 文件名搜索 | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 正则搜索 | ✅ | ✅+ | ✅+ | ✅+ | ✅+ | ✅+ |
| 读取文件 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 编辑文件 | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ |
| 创建文件 | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ |
| 执行命令 | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ |
| 工具并行 | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ |
| 上下文隔离 | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ |

---

## 九、与 Claude Code 的关键差异

| 维度 | Claude Code | 我们的方案 | 原因 |
|------|-------------|-----------|------|
| 语言 | TypeScript | Go | 现有技术栈 |
| 工具数量 | 40+ | 10 | 聚焦核心能力 |
| 延迟加载 | ToolSearch 动态发现 | 全部立即加载 | 工具少，不需要 |
| 大结果处理 | 持久化到磁盘 + 2K 预览 | 分级截断 | 简单有效 |
| 权限系统 | 4 层（deny/ask/allow/classifier） | 2 层（黑名单/白名单） | 无用户交互 UI |
| Bash 安全 | AST 解析 + LLM 分类器 + 沙箱 | 前缀匹配 + 黑名单 | 简化，后续可增强 |
| 文件编辑 | 9 步验证 + 时间戳防并发 | 唯一性检查 + 路径安全 | 单用户场景 |
| 并发控制 | 流式执行器 + 10 并发 | 简单 goroutine 池 | Go 天然支持 |
| 聚合预算 | 200K 字符/消息 + 贪心淘汰 | 不实现 | 工具少，结果小 |

---

## 十、参考源码速查

| 功能 | Claude Code 文件 | 关键函数/类 |
|------|-----------------|------------|
| 工具接口 | `src/Tool.ts` | `Tool` 接口、`buildTool()`、`ToolUseContext` |
| 工具注册 | `src/tools.ts` | `getAllBaseTools()`、`assembleToolPool()` |
| 工具编排 | `src/services/tools/toolOrchestration.ts` | `runTools()`、`partitionToolCalls()` |
| 工具执行 | `src/services/tools/toolExecution.ts` | `runToolUse()`、`checkPermissionsAndCallTool()` |
| 结果管理 | `src/utils/toolResultStorage.ts` | `maybePersistLargeToolResult()`、`enforceToolResultBudget()` |
| 限制常量 | `src/constants/toolLimits.ts` | `DEFAULT_MAX_RESULT_SIZE_CHARS`、`MAX_TOOL_RESULT_TOKENS` |
| Bash 权限 | `src/tools/BashTool/bashPermissions.ts` | `bashToolCheckPermission()`、前缀规则 |
| 文件读取 | `src/tools/FileReadTool/FileReadTool.ts` | `call()`、`readFileState` 去重 |
| 文件编辑 | `src/tools/FileEditTool/FileEditTool.ts` | `call()`、唯一性验证 |
| Glob | `src/tools/GlobTool/GlobTool.ts` | `call()`、mtime 排序 |
| Grep | `src/tools/GrepTool/GrepTool.ts` | `call()`、ripgrep 参数构建 |
| 子 Agent | `src/tools/AgentTool/AgentTool.tsx` | `call()`、`runAgent.ts` |
