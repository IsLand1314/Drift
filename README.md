# Drift

使用 Go 构建的本地只读 Coding Agent Runtime。当前完成 M0.2：模型可以在当前工作目录内读取一个文件，再基于内容给出解释。

它最多提供一次 `read_file` 工具调用，并且一次命令最多发起两次模型请求：首轮选择直接回答或读取文件，读取后第二轮生成最终回答。没有写文件、删除文件、执行命令或运行程序的能力。

## 快速开始

需要 Go 1.26+，在 PowerShell 中配置支持 OpenAI Chat Completions SSE 的模型服务：

```powershell
$env:OPENAI_API_KEY = "你的 API Key"
$env:OPENAI_BASE_URL = "https://你的服务地址/v1"
$env:OPENAI_MODEL = "你的模型名称"
go run ./cmd/drift -p "解释 README.md 的项目作用"
```

该示例会让模型按需读取当前目录的 `README.md`，然后只在 stdout 输出最终解释。API 地址不含 `/chat/completions`；模型和地址也可通过 `-model`、`-base-url` 指定，`-h` 查看帮助。完整配置、构建、只读边界和退出码说明见 [快速开始](doc/getting-started.md)。

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
