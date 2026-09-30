# Drift

使用 Go 构建的本地只读 Coding Agent Runtime。当前版本为 M0.5：模型可以在选定 workspace 内列出文件、搜索文本、读取文件，并根据每轮结果继续探索后给出解释。Runtime 具备统一事件流、只读工具注册表和 JSONL 会话审计。

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

也可以用 `-w` 指定目录或文件。目录成为 workspace，文件使用其父目录作为 workspace，并只把文件名作为首轮 focus 提示：

```powershell
go run ./cmd/drift -w F:\code\foxcode -p "分析这个项目"
go run ./cmd/drift -w .\README.md -p "解释这个文件"
```

配置优先级为：命令行 `-model`/`-base-url` > 进程环境变量 > 当前目录 `.env` > 内置默认值。API Key 没有命令行参数，只从环境变量或 `.env` 读取；缺少 Key 或模型时，请求不会发出。

该示例会让模型按需探索并解释当前项目；stdout 只输出最终回答，同一次运行的审计事件会写入被 Git 忽略的 `.drift/sessions/`。模型可在上述边界内组合多个工具调用。API 地址不含 `/chat/completions`；模型和地址也可通过 `-model`、`-base-url` 指定，`-h` 查看帮助。当前范围和验收标准见 [spec/current.md](spec/current.md)，M0.5 的 Workspace/Focus 规则见 [M0.5 阶段说明](doc/m0.5-workspace-focus.md)，M0.4 的实现与调用流程见 [M0.4 阶段说明](doc/m0.4-multiturn-read-agent.md)；M0.2/M0.3 文档与[历史快速开始](doc/getting-started.md)仅用于了解早期阶段行为。

Drift 目前只接受原生 OpenAI `tool_calls` 中的三个只读工具，没有 `run_command`、shell 或 exec 工具，因此不会执行命令。若模型输出 `<｜｜DSML｜｜ calls>`（或 ASCII 变体）等文本，这不是原生 `tool_calls` 事件，而是模型生成的不兼容伪工具格式。当前兼容性守卫只检查“首轮没有原生 `tool_calls`、`finish_reason` 为 `stop`”时缓存的首轮文本；该范围内会报告错误，不会把文本打印到 stdout，也不会执行它。它不会拒绝伴随原生工具调用的 DSML 文本，也不会检查后续轮次文本。

## 开发

```powershell
go test ./...
go vet ./...
go build ./cmd/drift
```

仅依赖 Go 标准库。测试使用本地模拟服务，无需 API Key。

## 文档

- [当前交付范围与验收标准](spec/current.md)
- [架构草案](doc/architecture.md)
- [M0.2.2 配置、边界与兼容说明](doc/m0.2.2-env-and-native-tools.md)
- [M0.3 Runtime 核心阶段说明](doc/m0.3-runtime-core.md)
- [M0.4 多轮只读探索阶段说明](doc/m0.4-multiturn-read-agent.md)
- [历史 M0 快速开始](doc/getting-started.md)
