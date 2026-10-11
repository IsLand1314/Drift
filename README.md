# Drift

Drift 是一个使用 Go 构建的本地受控 Coding Agent Runtime。模型负责理解目标，Runtime 负责工具、权限、审批、沙箱、会话、审计和验证。当前交付阶段为 M5.26。

Drift 参考 Hermes 的经验沉淀与 Agent 协作思路，以及 Pi-agent 的运行时和终端交互设计；同时坚持按 Drift 当前真实能力交付：配置采用工作区级 `.drift`，MCP 只作为 Client 调用外部工具，不对外提供 MCP Server；Codex 登录态当前保持 text-only。

## 设计与运行链路

| 关注点 | Drift 的处理 |
| --- | --- |
| Provider 差异 | OpenAI、OpenAI-compatible、Anthropic 和 Codex 适配为统一 LLM 事件 |
| 工具可见性 | `ToolSearch` 按需发现和加载，支持 `eager`、`dispatch`、`native` |
| 变更安全 | 文件预览、审批、文件状态检查、change set、失败回滚 |
| 命令安全 | Bash 受审批、workspace 边界、超时、输出限制和 OS 沙箱约束 |
| 会话与审计 | `session` 保存可恢复模型上下文，`audit` 保存脱敏安全记录 |
| 协作能力 | Plan、Task、Coordinator、子 Agent、Git worktree 和冲突状态 |
| 可观测性 | 可选的本地 Prometheus/Pushgateway/Grafana 运行指标 |

```text
用户 / TUI
  ↓
Agent Runtime
  ↓
Provider Client → 统一 LLM Stream Events → Agent Event Bridge / Collector
  ↓                                      ↓
Session Context                         Tool Registry
Audit Security Record                   ↓
Task / Coordinator / Worktree     权限 / 审批 / 沙箱
                                      ↓
                              内置工具或 MCP Client 外部工具
```

Provider 不把 OpenAI、Anthropic 或第三方 SSE 格式泄漏给 Agent；Agent 也不把内部事件格式泄漏给 TUI。模型流事件描述 Provider 返回了什么，Agent 事件描述运行过程中发生了什么。

## 当前能力

### Provider 与 LLM

- OpenAI Chat/Responses、OpenAI-compatible（包括 DeepSeek）和 Anthropic Messages。
- Codex Provider 可使用本机官方登录态，但当前只支持文本请求，不接管 Drift 工具。
- 统一文本、思考、工具调用、流结束和错误事件；工具参数完整后才执行。
- `ToolSearch` 支持 `eager`、`dispatch`、`native`。`native` 只有目标 Provider 支持原生 deferred tool reference 时才使用，否则安全降级为 `dispatch`。

### 内置工具

只读模式与 `-p` 默认提供 `Glob`、`Grep`、`ReadFile`。chat 还可以按权限使用 `WriteFile`、`EditFile`、`DeleteFile`、`Bash`、AskUserQuestion、Plan、Task、Memory、Skill 和已连接的 MCP 工具。工具和命令都有结果大小、调用次数、超时和 workspace 边界限制；写入提交前会检查文件是否被外部修改。

### MCP Client

Drift 连接外部 MCP Server，但不对外提供 MCP Server 端点。当前支持 stdio、HTTP、Streamable HTTP、工具发现、按需加载、资源和提示词发现、重试、超时、连接状态、权限审批和审计。外部 MCP 返回内容始终视为不可信数据，仍必须通过 Drift 的权限和沙箱边界。

### Plan、任务与协作

Plan Mode 只允许读取、分析和任务规划；用户确认后才可退出 Plan 并执行变更。Task、Coordinator、子 Agent 和 worktree 用于拆分任务、隔离修改、验证、合并和报告冲突。

### 安全与沙箱

权限模式：`default`（写入、删除和命令需审批）、`acceptEdits`（写入和编辑自动允许）、`plan`（只读规划）和 `bypassPermissions`（跳过普通审批但不能突破边界）。沙箱模式：`off`、`auto`、`required`。Linux 使用 OS 隔离后端，Windows 使用 AppContainer；macOS 当前没有 OS 沙箱后端。审批结果、沙箱结果和工具失败原因分别写入脱敏审计。

## 快速开始

要求 Go 1.26+：

```powershell
go run ./cmd/drift chat -w .
go run ./cmd/drift -p "解释 README.md 的项目作用"
go run ./cmd/drift doctor -w .
```

`doctor` 只检查配置、Provider、MCP 和沙箱能力，不发起模型请求、不启动 MCP Server、不修改文件，也不会输出密钥。目录或文件都可以作为 workspace：

```powershell
go run ./cmd/drift chat -w F:\code\your-project
go run ./cmd/drift -w .\README.md -p "解释这个文件"
```

连续对话中可使用 `exit`、`/exit` 或 `quit` 退出；Ctrl+C 在模型流或工具执行期间只取消当前轮，空闲输入时退出程序。

## 工作区配置

配置按 workspace 相对路径读取，不受项目位于哪个盘符影响：

```text
.drift/config.toml       # Provider、协议和模型
.drift/settings.toml     # 权限、沙箱、工具加载、Hook、记忆和监控行为
.drift/auth.json         # 本机 API Key，不提交 Git
.drift/mcp.json          # 外部 MCP Client 连接
```

### `config.toml`

```toml
version = 1

[[providers]]
name = "deepseek"
protocol = "openai-compat"
base_url = "https://api.deepseek.com"
model = "deepseek-chat"
api_key = "DEEPSEEK_API_KEY"

[[providers]]
name = "anthropic"
protocol = "anthropic"
base_url = "https://api.anthropic.com/v1"
model = "claude-sonnet"
api_key = "ANTHROPIC_API_KEY"
```

合法协议是 `openai`、`openai-compat`、`anthropic` 和 `codex`。这里的 `api_key` 是环境变量名，不是密钥本身；Codex Provider 不配置 `base_url` 或 `api_key`。

### `settings.toml`

```toml
version = 1
default_provider = "deepseek"
permission_mode = "default"
sandbox_mode = "auto"
sandbox_auto_allow = false
tool_loading = "dispatch"
tui_mode = "main"

[memory]
auto_extract = true
auto_retrieve = true

[observability]
enabled = false
pushgateway_url = "http://127.0.0.1:9091"
instance = "local"
```

`sandbox_auto_allow` 默认关闭。开启后也只会自动放行经过 OS 沙箱验证的内建低风险只读命令；普通变更命令、未知命令、显式 `ask/deny` 规则和危险命令仍分别进入审批或直接拒绝。

Hook 事件包括 `session_start`、`session_end`、`turn_start`、`turn_end`、`pre_tool_use`、`post_tool_use` 和 `plan_state_changed`。Hook 只加载可信配置，外部命令仍受超时和错误策略约束。

### `auth.json`

```json
{
  "version": 1,
  "providers": {
    "deepseek": { "type": "api_key", "key": "sk-your-key" }
  }
}
```

认证优先级：命令行 `-api-key` → `.drift/auth.json` → `config.toml` 中 `api_key` 指定的 OS 环境变量。使用 `go run ./cmd/drift login --provider deepseek -w .` 保存密钥。不要把真实 `auth.json`、会话、change set 或运行日志提交到仓库。

## MCP 配置与命令

`.drift/mcp.json` 示例：

```json
{
  "servers": [
    {
      "name": "local-tools",
      "transport": "stdio",
      "command": "node",
      "args": ["server.js"],
      "env_refs": ["MCP_TOKEN"],
      "timeout_ms": 30000,
      "retry_count": 1
    }
  ]
}
```

chat 内可使用：

```text
/mcp connect <name>
/mcp list
/mcp status
/mcp resources
/mcp prompts
/mcp disconnect <name>
/mcp reconnect <name>
```

连接后工具不会全部自动塞进上下文；模型先用 `ToolSearch` 搜索并加载 schema，再调用 `mcp__<server>__<tool>`。

## 会话、审计与本地数据

```text
.drift/sessions/    # 可恢复的模型上下文和会话元数据
.drift/audits/      # 脱敏的工具、审批、沙箱和错误记录
.drift/changes/     # 文件变更预览、提交和回滚信息
.drift/memory/      # 已验证的长期记忆与候选记忆
.drift/skills/      # 工作区 Skill 定义
```

`session` 负责模型上下文、恢复、搜索和工具配对修复；`audit` 负责安全记录。审计不是会话恢复来源，完整会话可能含有敏感内容。

常用命令：

```powershell
go run ./cmd/drift session list
go run ./cmd/drift session show <id>
go run ./cmd/drift session timeline <id>
go run ./cmd/drift audit list
go run ./cmd/drift audit show <path>
```

chat 内还支持 `/status`、`/compact`、`/clear`、`/resume`、`/new`、`/rename`、`/permissions` 和 `/memory`。

## 本地运行监控（可选）

Drift 默认不发送运行指标。启动本地 Prometheus、Pushgateway 与 Grafana：

```powershell
docker compose -p drift-observability -f ops/observability/docker-compose.yml up -d
```

地址：Pushgateway `http://127.0.0.1:9091`、Prometheus `http://127.0.0.1:9090`、Grafana `http://127.0.0.1:3000/d/drift-local-runs/drift-local-runs`。在 `.drift/settings.toml` 中启用：

```toml
[observability]
enabled = true
pushgateway_url = "http://127.0.0.1:9091"
instance = "local"
```

第一版只允许回环地址，不上传提示词、回答、会话 ID、workspace、文件路径、命令、参数、原始错误或 API Key。Pushgateway 不可用不会影响 Drift 主流程。停止：`docker compose -p drift-observability -f ops/observability/docker-compose.yml down`。

## 项目结构

```text
cmd/drift/             CLI 入口
internal/app/          命令、TUI 和运行编排
internal/agent/        单 Agent 运行时与事件桥接
internal/agents/       多 Agent、Team、mailbox 和工具过滤
internal/llm/          统一事件、错误和 Provider Client
internal/tool/         内置工具、注册表和 ToolSearch
internal/mcp/          外部 MCP Client
internal/session/      上下文、恢复、快照和工具配对
internal/audit/        脱敏审计与安全记录
internal/config/       workspace 配置与认证加载
internal/memory/       记忆提取、检索和 Skill 经验
internal/coordinator/  任务依赖、调度和合并协调
internal/git/           worktree、回滚和冲突处理
internal/observability/ 本地指标
ops/observability/      Prometheus、Pushgateway、Grafana 配置
```

## 开发与验证

```powershell
go test ./...
go vet ./...
go build ./cmd/drift
git diff --check
```

测试使用本地模拟服务，不需要真实 API Key。真实验收还应覆盖 Provider 原生工具调用、ToolSearch、写入审批、Bash 沙箱、MCP 外部工具、会话恢复、worktree 合并和本地监控。

## 当前边界

- Drift 是本地 Runtime，不提供远程 Web UI 或托管云沙箱。
- Drift 是 MCP Client，不是 MCP Server。
- Codex 登录态当前 text-only，不接管 Drift 内置工具。
- macOS 当前没有 OS 沙箱后端；`required` 模式会拒绝执行。
- 监控默认关闭且只支持本地 Pushgateway。
- 设计文档和实施笔记保留在本地，不作为 README 的交付链接。
