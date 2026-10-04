# Drift

使用 Go 构建的本地受控 Coding Agent Runtime。当前版本为 M5.26：除单次 `-p` 请求外，还支持 `drift chat` 多轮交互、默认本地完整会话保存、`--resume` 启动恢复、chat 内 `/resume` 会话切换、`/new` 新建会话、`--no-session` 临时模式、`/status` 状态面板、`/compact` 手动压缩、活动轮次 Ctrl+C 取消、workspace Skills、确认式文件创建/编辑/删除、受控命令执行和终端审批选择器。模型可以在选定 workspace 内按模式查找文件、搜索文本、分页读取大文件，并根据每轮结果继续探索后给出解释；可选 `--trace` 会把安全运行摘要写到 stderr。`session` 管理可恢复会话，`audit` 查看脱敏审计。Provider 支持 OpenAI Compatible、Anthropic Messages 和 Codex 登录态；MCP Client 支持 stdio、HTTP、Streamable HTTP、Resource、Prompt 和按需工具加载；可选开启用户明确偏好/纠正的长期记忆候选提取和已验证记忆自动检索，也可将已验证经验生成 review-only Skill 草稿。M5.26 提供 Anthropic native、外部 MCP 和 Windows MCP 沙箱回归证据。

`-p` 只注册三个只读工具：`Glob`、`Grep`、`ReadFile`。`chat` 额外注册 `WriteFile`、`EditFile`、`DeleteFile` 和 `Bash`；文件变更与命令执行前必须由用户通过审批选择器确认；一轮多文件操作聚合到一个 `.drift/changes` change set。写入预览会保存目标文件状态，提交前检测外部修改，避免覆盖用户的新内容。单次运行最多 6 次模型请求、12 次工具调用；成功工具结果累计最多 512 KiB，单文件最多 128 KiB。`Glob` 最多返回 200 个文件，`Grep` 最多扫描 200 个文件、返回 100 个匹配，输出最多 32 KiB。命令默认 30 秒超时，最长 60 秒，stdout/stderr 各自限流并只把结果摘要写入审计。

只读诊断：`drift doctor -w <workspace>` 检查 Provider、MCP 配置和 OS 沙箱能力，不发起模型请求、不启动 MCP Server、不修改文件，也不会输出 API Key。

## 快速开始

需要 Go 1.26+。M5.2 使用当前 workspace 的 `.drift` 配置目录：

```text
.drift/settings.toml  # 行为默认值
.drift/config.toml    # Provider 和模型
.drift/auth.json      # API Key（仅本机保存，不提交 Git）
```

`settings.toml` 可用 `tool_loading = "dispatch"` 选择工具 schema 策略；也可以临时使用 `--tool-loading eager|dispatch|native`。Anthropic Provider 的 `native` 使用原生 deferred tool reference；OpenAI Compatible、DeepSeek 和 Codex 不支持时会安全降级为 `dispatch`。

示例 `config.toml`：

```toml
version = 1

[[providers]]
name = "deepseek"
protocol = "openai-compat"
base_url = "https://api.deepseek.com"
model = "deepseek-chat"
api_key = "DEEPSEEK_API_KEY"
```

示例 `auth.json`（不要提交真实密钥）：

```json
{"version":1,"providers":{"deepseek":{"type":"api_key","key":"sk-..."}}}
```

配置存在时，命令行 `-provider`、`-model`、`-base-url` 和 `-api-key` 只覆盖本次运行；认证优先级是 `-api-key` > `auth.json` > `api_key` 指定的 OS 环境变量。运行时不再读取 workspace `.env` 文件。

`api_key` 的值是环境变量名，不是密钥本身；真实密钥应放在 `.drift/auth.json` 或对应 OS 环境变量中。

```powershell
$env:OPENAI_API_KEY = "你的 API Key"
$env:OPENAI_BASE_URL = "https://你的服务地址/v1"
$env:OPENAI_MODEL = "你的模型名称"
go run ./cmd/drift -p "解释 README.md 的项目作用"
```

选择 Anthropic Messages Provider 时使用独立配置，不会把 API Key 放进命令行：

```powershell
$env:ANTHROPIC_API_KEY = "你的 Anthropic API Key"
$env:ANTHROPIC_BASE_URL = "https://api.anthropic.com/v1"
$env:ANTHROPIC_MODEL = "你的模型名称"
go run ./cmd/drift -provider anthropic -p "解释 README.md 的项目作用"
```

`-provider` 的优先级高于 `DRIFT_PROVIDER`；Provider 失败会在安全审计中记录稳定 stage，原始响应和密钥不会落盘。

也可以用 `-w` 指定目录或文件。目录成为 workspace，文件使用其父目录作为 workspace，并只把文件名作为首轮 focus 提示：

```powershell
go run ./cmd/drift -w F:\code\foxcode -p "分析这个项目"
go run ./cmd/drift -w .\README.md -p "解释这个文件"
```

需要连续追问时使用交互模式；上下文只在当前进程内存中保留，输入 `exit`、`/exit` 或 `quit` 退出：

```powershell
go run ./cmd/drift chat -w .
```

可以在当前 workspace 中创建一个显式 Skill，并在一次运行或 chat 中选择它：

```text
.drift/skills/project-overview/SKILL.md
```

```powershell
go run ./cmd/drift skill list -w .
go run ./cmd/drift skill show -w . project-overview
go run ./cmd/drift -skill project-overview -p "分析当前项目"
go run ./cmd/drift chat -skill project-overview -w .
```

Skill 只提供额外的只读分析说明；一次运行最多加载一个，不能新增工具、执行命令、写文件或越出 workspace。Skill 正文不会写入脱敏审计或会话元数据。

默认 chat 会显示 Session ID，并把完整上下文保存到当前 workspace 的 `.drift/sessions/`。脱敏运行审计保存到 `.drift/audits/`；下次可恢复最近或指定会话；不希望保存完整上下文时使用：

```powershell
go run ./cmd/drift chat --resume -w .
go run ./cmd/drift chat --resume conv-<id> -w .
go run ./cmd/drift chat --no-session -w .
go run ./cmd/drift session list
go run ./cmd/drift session show conv-<id>
go run ./cmd/drift session timeline conv-<id>
go run ./cmd/drift session delete conv-<id> --yes
go run ./cmd/drift session list --limit 10
go run ./cmd/drift session rename conv-<id> "FoxCode 分析"
go run ./cmd/drift session prune --before 2026-09-01T00:00:00Z
go run ./cmd/drift session prune --before 2026-09-01T00:00:00Z --yes
go run ./cmd/drift audit list
go run ./cmd/drift audit show .drift/audits/run-<timestamp>.jsonl
```

进入 chat 后，`/resume` 打开当前 workspace 的可搜索会话选择器，`/resume <id>` 直接切换指定会话，`/new` 保存当前会话并创建新的空会话，`/rename <标题>` 更新当前持久会话标题。选择器取消或恢复失败时，当前上下文保持不变；`--no-session` 模式只能创建新的临时上下文。

进入 chat 后可用 `/status` 查看 Session ID、Model、Context、Tokens、Tools 和 Workspace；Context 使用十进制 KB 估算，Tokens 使用 Provider 返回的真实 usage，未返回时显示 `unavailable`，混合状态显示 `(partial)`。用 `/compact` 请求模型生成摘要并保留最近消息；`/compact` 不提供文件工具，失败时保留原上下文。`/clear` 才是完全清空；普通 `clear`、`status`、`compact` 只会提示使用对应的斜杠命令。真实终端会以分隔线、彩色提示符、助手标记和本轮完成耗时区分交互；非终端输出保持纯文本。
模型流或只读工具执行期间按 Ctrl+C 只取消当前轮，chat 继续等待下一条输入；空闲等待输入时按 Ctrl+C 以退出码 130 结束。取消轮不会写入半轮完整会话，只在脱敏审计中记录 `agent_cancelled`。真实终端会显示安全的工具进度摘要和英文 `Done - <seconds>s` 完成标记。

长对话达到上下文上限时，输入 `/clear` 可清空当前上下文；持久会话会先保存空快照，保存成功后才清理。普通文本 `clear` 不会清理上下文，只会提示使用 `/clear`。完整快照可能包含提示词、回答和工具结果，不是脱敏日志，不应上传或分享；`.drift/audits/` 只保存脱敏审计，永远不作为恢复来源。`session prune` 不带 `--yes` 时只预览，不会删除文件。

没有 `config.toml` 时不会再读取 `.env`；请使用 `.drift/config.toml`、`.drift/auth.json` 或命令行参数。缺少 Key 或模型时，请求不会发出。配置 `protocol = "codex"` 时，Drift 通过官方 `codex app-server` 使用本机登录态，不从 `auth.json` 读取 Codex token；当前 Codex Provider 只支持文本请求。

该示例会让模型按需探索并解释当前项目；stdout 只输出最终回答，同一次运行的安全审计事件会写入被 Git 忽略的 `.drift/audits/`。`chat` 中的写入过程记录在 `.drift/changes/YYYY/MM/DD/`，可能包含项目敏感内容，不应上传。需要观察过程时加 `--trace`，摘要会写入 stderr；需要查看完整会话时使用 `drift session list/show`，`drift session timeline <id>` 只显示消息角色、工具名、调用 ID 和字节数，不显示正文；需要查看脱敏审计时使用 `drift audit list/show`。这两套命令不互为别名，聊天输入区也不提供 `/audit`。当前范围和验收标准见 [spec/current.md](spec/current.md)。

Drift 的 `-p` 模式只接受三个只读工具；`chat` 额外接受经过确认的 `WriteFile`、`EditFile`、`DeleteFile` 和 `Bash`。命令固定在 workspace cwd 运行，当前 chat 的“允许此类操作”只匹配同一工具、cwd 和精确命令，退出后失效。OpenAI Compatible 与 Anthropic Messages 的工具调用都会归一化为同一套 Agent 事件；Codex Provider 当前为 text-only，不接管 Drift 工具。若模型输出 `<｜｜DSML｜｜ calls>`（或 ASCII 变体）等文本，这不是原生工具调用，而是模型生成的不兼容伪工具格式。所有无原生工具调用且以 `stop` 结束的最终文本都会经过兼容性守卫，不会把伪工具文本打印到 stdout，也不会执行它。

## 开发

```powershell
go test ./...
go vet ./...
go build ./cmd/drift
```

运行时使用 Go 标准库和 Bubble Tea/Bubbles 的终端输入组件。测试使用本地模拟服务，无需 API Key。

## 规格

当前交付范围和验收标准见 [spec/current.md](spec/current.md)。`doc/` 与 `docs/superpowers/` 是本地设计笔记和实施计划，不作为远程仓库交付内容。
