# Drift

使用 Go 构建的本地只读 Coding Agent Runtime。当前完成 M0.2.1：模型可以在当前工作目录内读取一个小型文件集，再基于内容给出解释。

M0.2 的单文件行为保持不变；M0.2.1 允许模型在首轮最多调用四次 `read_file`。每个文件最大 128 KiB、成功读取内容合计最大 512 KiB，且一次命令仍最多发起两次模型请求：首轮选择直接回答或读取文件，读取后第二轮生成最终回答。宽泛问题可在模型把读取控制在这四个文件内时得到项目摘要；需要更多文件时会收到明确的受限上限错误。没有写文件、删除文件、执行命令或运行程序的能力。

## 快速开始

需要 Go 1.26+，在 PowerShell 中配置支持 OpenAI Chat Completions SSE 的模型服务：

```powershell
$env:OPENAI_API_KEY = "你的 API Key"
$env:OPENAI_BASE_URL = "https://你的服务地址/v1"
$env:OPENAI_MODEL = "你的模型名称"
go run ./cmd/drift -p "解释 README.md 的项目作用"
```

该示例会让模型按需读取当前目录的 `README.md`，然后只在 stdout 输出最终解释。模型也可在四文件、单文件 128 KiB、合计 512 KiB 的受限范围内读取更多上下文；API 地址不含 `/chat/completions`；模型和地址也可通过 `-model`、`-base-url` 指定，`-h` 查看帮助。完整配置、构建、只读边界和退出码说明见 [快速开始](doc/getting-started.md)。

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
