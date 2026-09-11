# Drift

使用 Go 构建的本地只读 Coding Agent Runtime。当前完成 M0.2.2：模型可以在当前工作目录内读取一个小型文件集，再基于内容给出解释；本地配置和原生工具兼容性也有明确边界。

M0.2 的单文件行为保持不变；M0.2.1 允许模型在首轮最多调用四次 `read_file`。每个文件最大 128 KiB、成功读取内容合计最大 512 KiB，且一次命令仍最多发起两次模型请求：首轮选择直接回答或读取文件，读取后第二轮生成最终回答。宽泛问题可在模型把读取控制在这四个文件内时得到项目摘要；需要更多文件时会收到明确的受限上限错误。没有写文件、删除文件、执行命令或运行程序的能力。

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

配置优先级为：命令行 `-model`/`-base-url` > 进程环境变量 > 当前目录 `.env` > 内置默认值。API Key 没有命令行参数，只从环境变量或 `.env` 读取；缺少 Key 或模型时，请求不会发出。

该示例会让模型按需读取当前目录的 `README.md`，然后只在 stdout 输出最终解释。模型也可在四文件、单文件 128 KiB、合计 512 KiB 的受限范围内读取更多上下文；一次命令最多发起两次模型请求。API 地址不含 `/chat/completions`；模型和地址也可通过 `-model`、`-base-url` 指定，`-h` 查看帮助。当前配置、只读边界和兼容性说明见 [M0.2.2 阶段说明](doc/m0.2.2-env-and-native-tools.md)；[历史快速开始](doc/getting-started.md) 仅供了解早期阶段行为。

Drift 目前只提供原生 OpenAI `tool_calls` 中的 `read_file`，没有 `run_command`、shell 或 exec 工具，因此不会执行命令。若模型输出 `<｜｜DSML｜｜ calls>`（或 ASCII 变体）等文本，这不是原生 `tool_calls` 事件，而是模型生成的不兼容伪工具格式；Drift 会报告兼容性错误，不会把它打印到 stdout，也不会执行它。

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
- [运行与开发说明](doc/getting-started.md)
