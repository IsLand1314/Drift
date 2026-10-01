# Drift

使用 Go 构建的本地只读 Coding Agent Runtime。当前版本为 M1.9：除单次 `-p` 请求外，还支持 `drift chat` 多轮交互、默认本地完整会话保存、`--resume` 恢复、`--no-session` 临时模式、`/status` 状态面板、`/compact` 手动压缩和活动轮次 Ctrl+C 取消。模型可以在选定 workspace 内列出文件、搜索文本、分页读取大文件，并根据每轮结果继续探索后给出解释；可选 `--trace` 会把安全运行摘要写到 stderr，`session list/show` 查看脱敏审计，`conversation` 命令支持会话列表、标题、恢复、精确删除和过期清理。Provider 默认是 OpenAI Compatible，也可显式选择 Anthropic Messages。

默认提供三个工具：`list_files`、`search_text`、`read_file`。单次运行最多 4 次模型请求、6 次工具调用；成功工具结果累计最多 512 KiB，单文件最多 128 KiB。`list_files` 最多返回 200 个文件，`search_text` 最多扫描 200 个文件、返回 100 个匹配，输出最多 32 KiB。工具按顺序串行执行，达到限制后使用无工具 schema 的请求生成说明。没有写文件、删除文件、执行命令或运行程序的能力。

## 快速开始

需要 Go 1.26+。推荐先复制可提交的配置模板，再在 PowerShell 中编辑本地 `.env`：

```powershell
Copy-Item .env.example .env
```

`.env` 是可选的本地文件，已被 Git 忽略；其中的 `OPENAI_API_KEY` 只应填写本机密钥，不要提交、打印或放进截图和日志。也可以使用进程环境变量：

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

默认 chat 会显示 Session ID，并把完整上下文保存到当前 workspace 的 `.drift/conversations/`。下次可恢复最近或指定会话；不希望保存完整上下文时使用：

```powershell
go run ./cmd/drift chat --resume -w .
go run ./cmd/drift chat --resume conv-<id> -w .
go run ./cmd/drift chat --no-session -w .
go run ./cmd/drift conversation list
go run ./cmd/drift conversation show conv-<id>
go run ./cmd/drift conversation delete conv-<id> --yes
go run ./cmd/drift conversation list --limit 10
go run ./cmd/drift conversation rename conv-<id> "FoxCode 分析"
go run ./cmd/drift conversation prune --before 2026-09-01T00:00:00Z
go run ./cmd/drift conversation prune --before 2026-09-01T00:00:00Z --yes
```

进入 chat 后可用 `/status` 查看 Session ID、Model、Context、Tokens、Tools 和 Workspace；Context 使用十进制 KB 估算，Tokens 使用 Provider 返回的真实 usage，未返回时显示 `unavailable`，混合状态显示 `(partial)`。用 `/compact` 请求模型生成摘要并保留最近消息；`/compact` 不提供文件工具，失败时保留原上下文。`/clear` 才是完全清空；普通 `clear`、`status`、`compact` 只会提示使用对应的斜杠命令。真实终端会以分隔线、彩色提示符、助手标记和本轮完成耗时区分交互；非终端输出保持纯文本。
模型流或只读工具执行期间按 Ctrl+C 只取消当前轮，chat 继续等待下一条输入；空闲等待输入时按 Ctrl+C 以退出码 130 结束。取消轮不会写入半轮完整会话，只在脱敏审计中记录 `agent_cancelled`。真实终端会显示安全的工具进度摘要和英文 `Done - <seconds>s` 完成标记。

长对话达到上下文上限时，输入 `/clear` 可清空当前上下文；持久会话会先保存空快照，保存成功后才清理。普通文本 `clear` 不会清理上下文，只会提示使用 `/clear`。完整快照可能包含提示词、回答和工具结果，不是脱敏日志，不应上传或分享；`.drift/sessions/` 仍只保存脱敏审计，永远不作为恢复来源。M1.3 的 `conversation prune` 不带 `--yes` 时只预览，不会删除文件。

配置优先级为：命令行 `-model`/`-base-url` > 进程环境变量 > 当前目录 `.env` > 内置默认值。API Key 没有命令行参数，只从环境变量或 `.env` 读取；缺少 Key 或模型时，请求不会发出。

该示例会让模型按需探索并解释当前项目；stdout 只输出最终回答，同一次运行的安全审计事件会写入被 Git 忽略的 `.drift/sessions/`。需要观察过程时加 `--trace`，摘要会写入 stderr；需要查看历史摘要时运行 `drift session list` 或 `drift session show <path>`。新 JSONL 不保存提示词、原始工具参数、文件内容或回答正文，只保存安全的相对路径、脱敏占位符、字节数和结束原因。失败事件含稳定的 `stage`，例如 `provider_timeout`、`provider_sse_invalid_json`、`agent_empty_response` 或 `agent_context_limit`。完整对话恢复和边界见 [M1.2 阶段说明](doc/m1.2-conversation-persistence.md)，进程内上下文规则见 [M1.1 阶段说明](doc/m1.1-context-control.md) 和 [M1.0 阶段说明](doc/m1.0-interactive-chat.md)。当前范围和验收标准见 [spec/current.md](spec/current.md)。

Drift 目前只接受原生工具调用中的三个只读工具，没有 `run_command`、shell 或 exec 工具，因此不会执行命令。OpenAI Compatible 与 Anthropic Messages 的工具调用都会归一化为同一套 Agent 事件；若模型输出 `<｜｜DSML｜｜ calls>`（或 ASCII 变体）等文本，这不是原生工具调用，而是模型生成的不兼容伪工具格式。所有无原生工具调用且以 `stop` 结束的最终文本都会经过兼容性守卫，不会把伪工具文本打印到 stdout，也不会执行它。

## 开发

```powershell
go test ./...
go vet ./...
go build ./cmd/drift
```

运行时使用 Go 标准库和 Bubble Tea/Bubbles 的终端输入组件。测试使用本地模拟服务，无需 API Key。

## 文档

- [当前交付范围与验收标准](spec/current.md)
- [架构草案](doc/architecture.md)
- [M0.2.2 配置、边界与兼容说明](doc/m0.2.2-env-and-native-tools.md)
- [M0.3 Runtime 核心阶段说明](doc/m0.3-runtime-core.md)
- [M0.4 多轮只读探索阶段说明](doc/m0.4-multiturn-read-agent.md)
- [M0.8 Trace 可观测性阶段说明](doc/m0.8-trace.md)
- [M0.9 Session 审计与查看器阶段说明](doc/m0.9-session-audit.md)
- [M1.1 交互上下文管理阶段说明](doc/m1.1-context-control.md)
- [M1.2 本地对话持久化阶段说明](doc/m1.2-conversation-persistence.md)
- [M1.3 会话索引与生命周期管理阶段说明](doc/m1.3-conversation-management.md)
- [M1.4 手动上下文压缩阶段说明](doc/m1.4-context-compaction.md)
- [M1.5 运行时卫生与边界对齐](doc/m1.5-runtime-hygiene.md)
- [M1.6 真实 Token 用量](doc/m1.6-token-usage.md)
- [M1.7 单轮取消](doc/m1.7-turn-cancellation.md)
- [M1.8 终端输入层](doc/m1.8-terminal-input.md)
- [M1.9 Anthropic Provider](doc/m1.9-anthropic-provider.md)
- [M1.10 运行过程反馈](doc/m1.10-runtime-feedback.md)
- [M1.0 交互式只读对话阶段说明](doc/m1.0-interactive-chat.md)
- [M0.7 Provider 诊断与读取保护阶段说明](doc/m0.7-provider-reliability.md)
- [M0.2 Read Agent 说明](doc/m0.2-read-agent.md)
