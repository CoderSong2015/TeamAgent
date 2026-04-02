# TeamAgent

基于 Go 标准库的轻量多 Agent 对话服务，支持单 Agent 对话和多角色 Team 协作两种模式。零外部依赖，内嵌 Web UI，通过 OpenAI 兼容 API 接入任意大语言模型。

## 特性

- **双模式架构** — 单 Agent 对话（Agentic Loop）+ 多角色 Team 协作（Leader 编排 + Worker 执行）
- **9 个内置工具** — 文件读写、搜索、Shell 命令、子 Agent 探索，覆盖完整的代码分析与修改工作流
- **智能记忆系统** — Agent 结构化记忆 + Team 公共记忆池 + Worker 角色经验，跨对话持久化
- **安全优先** — 路径沙箱、写入黑名单、命令安全防护、子 Agent 权限隔离
- **上下文治理** — 读缓存去重、分级截断、旧结果清理、子 Agent 自动降级，防止 token 膨胀
- **内嵌前端** — 单文件 SPA，Team 模式支持 SSE 实时推送工具调用和协作进度
- **零依赖** — 纯 Go 标准库，无需安装任何第三方包

## 快速开始

### 环境要求

- Go 1.22+

### 配置环境变量

```bash
# 必填：LLM API（OpenAI 兼容格式）
export LLM_API_URL=https://your-llm-api/v1/chat/completions
export LLM_API_KEY=your-api-key

# 可选：工作目录（Agent 操作文件的根目录）
export WORKSPACE=/path/to/your/project

# 可选：安全控制
export TOOL_WRITE_ENABLED=true     # 文件写入（默认开启）
export TOOL_COMMAND_ENABLED=false   # Shell 命令执行（默认关闭）
```

### 编译与运行

```bash
go build -o teamagent .
./teamagent
```

或直接运行：

```bash
go run main.go
```

服务启动后访问 http://localhost:8088 打开 Web UI。

## 架构概览

```
┌──────────────────────────────────────────────────────────┐
│                     main.go (HTTP 路由)                   │
├──────────────┬──────────────┬──────────────┬─────────────┤
│   agent/     │    team/     │    tools/    │    web/     │
│              │              │              │             │
│  单 Agent    │  Leader 编排  │  9 个工具    │  嵌入式 SPA │
│  Agentic     │  Worker 执行  │  安全控制    │  文档浏览   │
│  Loop        │  Pipeline    │  子 Agent    │  SSE 推送   │
│  记忆提取    │  记忆系统     │  上下文治理   │             │
├──────────────┴──────────────┴──────┬───────┴─────────────┤
│                  llm/              │       data/          │
│          OpenAI 兼容调用层          │    持久化存储         │
└────────────────────────────────────┴─────────────────────┘
```

## 两种模式

### Agent 模式

适合日常对话、代码分析、文件操作等单人场景。

- 最多 **10 轮** 工具调用的 Agentic Loop
- 对话结束自动提取结构化记忆（用户画像 / 行为反馈 / 项目上下文 / 外部引用）
- 可调用全部 9 个工具 + 子 Agent

### Team 模式

适合复杂任务，如功能开发、架构设计、代码审查等需要多角色协作的场景。

| 角色 | 职责 | 工具 |
|------|------|------|
| **Leader** | 任务分析与编排 | 无（纯文本） |
| **Researcher** | 调研分析，提供背景知识 | ✅ 全部 |
| **Coder** | 编写与修改代码 | ✅ 全部 |
| **Reviewer** | 代码审查，发现问题 | ✅ 全部 |
| **Architect** | 架构设计，编写设计文档 | ✅ 全部 |
| **Secretary** | 提取对话中的有价值知识 | 无（纯文本） |

**Pipeline 模式**：
- 大功能：Researcher → Architect（设计审核）→ Coder（代码审核）→ Leader 综合
- 小功能：Researcher → Coder → Reviewer（返工循环，最多 2 轮）

## 工具系统

### 只读工具

| 工具 | 说明 |
|------|------|
| `read_file` | 读取文件内容（200 行限制，支持 offset/limit） |
| `list_files` | 树形展示目录结构（6 层深度，200 条目） |
| `grep_search` | 正则搜索文件内容（100 行结果） |
| `glob_search` | 按文件名模式搜索（支持 `**` 递归，按修改时间排序） |

### 写入工具

| 工具 | 说明 |
|------|------|
| `edit_file` | 精确字符串替换（先读后改 + mtime 竞态检测 + 备份 + diff 输出） |
| `write_file` | 创建或覆盖文件（自动创建父目录，200KB 限制） |
| `run_command` | 执行 Shell 命令（默认禁用，需 `TOOL_COMMAND_ENABLED=true`） |

### 子 Agent 工具

| 工具 | 说明 |
|------|------|
| `explore` | 启动只读搜索子 Agent（3 轮 / 60s 超时 / 800 字符结果） |
| `delegate_task` | 启动只读多步子 Agent（3 轮 / 120s 超时 / 1200 字符结果） |

子 Agent 拥有独立上下文，不会膨胀父级 Worker 的上下文。最多 3 个并行运行，连续 3 次失败自动降级为直接工具调用。

## API

### Agent

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/agents` | 获取 Agent 列表 |
| POST | `/api/agents` | 创建 Agent |
| GET | `/api/agent/{id}` | 获取 Agent 详情 |
| POST | `/api/agent/{id}/chat` | 对话（`{"message": "...", "model": "..."}` ） |
| GET | `/api/agent/{id}/history` | 获取对话历史 |
| GET | `/api/agent/{id}/memory` | 获取 Agent 记忆 |

### Team

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/teams` | 获取 Team 列表 |
| POST | `/api/teams` | 创建 Team |
| GET | `/api/team/{id}` | 获取 Team 详情 |
| POST | `/api/team/{id}/chat` | 对话（SSE 流式响应） |
| GET | `/api/team/{id}/memory` | 获取 Team 公共记忆 |
| DELETE | `/api/team/{id}/memory` | 清空 Team 公共记忆 |

### 文档

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/docs` | 获取设计文档列表 |
| GET | `/api/doc/{name}` | 获取指定文档内容（Markdown） |

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `LLM_API_URL` | — | **必填**，LLM API 地址（OpenAI 兼容的 `/v1/chat/completions`） |
| `LLM_API_KEY` | — | **必填**，LLM API 密钥 |
| `WORKSPACE` | `/root/stockAnalysis` | 工具系统工作目录（Agent 可操作的文件根路径） |
| `TOOL_WRITE_ENABLED` | `true` | 设为 `false` 禁用 `edit_file` / `write_file` |
| `TOOL_COMMAND_ENABLED` | `false` | 设为 `true` 启用 `run_command` |

## 数据存储

所有运行时数据存储在 `data/` 目录下：

```
data/
├── meta.json                  # Agent 注册表
├── teams_meta.json            # Team 注册表
├── team_memory.json           # Team 公共记忆池（上限 30 条，30 天过期）
├── worker_memory/             # Worker 角色经验
│   └── {role}.json
├── agents/{id}/
│   ├── history.json           # 对话历史（仅保存 user + assistant）
│   └── memory.json            # 结构化长期记忆
└── teams/{id}/
    ├── messages.json           # 协作消息日志
    ├── leader/history.json
    └── workers/{role}/
        └── session_history.json
```

## 安全机制

- **路径沙箱**：所有文件操作限制在 `WORKSPACE` 目录内
- **写入控制**：`.git/`、`go.sum` 等受保护路径拒绝写入
- **命令安全**：`run_command` 默认禁用；启用后有黑名单拦截（`rm -rf /`、`mkfs` 等危险命令）、超时控制（默认 30s，最大 120s）
- **子 Agent 隔离**：子 Agent 仅可使用 4 个只读工具，禁止写操作
- **大文件保护**：单文件读取 100KB / 200 行限制，写入 200KB 限制

## 项目结构

```
├── main.go                 # 入口：路由注册与初始化
├── go.mod
├── agent/
│   └── agent.go            # Agent CRUD、Agentic Loop、记忆系统
├── team/
│   ├── team.go             # Team 编排、Worker 循环、Pipeline
│   └── memory.go           # 公共记忆池 + Worker 角色经验
├── llm/
│   └── client.go           # LLM API 调用层（支持 Function Calling + 重试）
├── tools/
│   ├── tools.go            # 工具注册表、执行引擎、读写分流
│   ├── workspace.go        # 工作目录、路径安全、写入/命令安全控制
│   ├── context.go          # 旧工具结果清理
│   ├── read_cache.go       # per-session 读缓存 + 子 Agent 降级
│   ├── read_file.go        # read_file 工具
│   ├── list_files.go       # list_files 工具
│   ├── grep_search.go      # grep_search 工具
│   ├── glob_search.go      # glob_search 工具
│   ├── edit_file.go        # edit_file 工具
│   ├── write_file.go       # write_file 工具
│   ├── run_command.go       # run_command 工具
│   └── subagent.go         # 子 Agent 引擎 + explore / delegate_task
├── web/
│   ├── template.go         # 模板服务
│   ├── index_html.go       # 嵌入式前端 SPA
│   └── docs.go             # 设计文档浏览 API
├── docs/                   # 设计文档
└── data/                   # 运行时持久化数据
```

## License

MIT
