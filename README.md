# Drift

使用 Go 构建的本地 Coding Agent Runtime。

当前完成 M0 第一步：最小 CLI + OpenAI Compatible 单次流式文本对话，支持错误提示和 Ctrl+C 取消。文件工具、Agent Loop、多轮会话、MCP 和 TUI 尚未实现。

## 快速开始

需要 Go 1.26+，在 PowerShell 中配置模型服务：

```powershell
$env:OPENAI_API_KEY = "你的 API Key"
$env:OPENAI_BASE_URL = "https://你的服务地址/v1"
$env:OPENAI_MODEL = "你的模型名称"
go run ./cmd/drift -p "你好，介绍一下自己"
```

API 地址不含 `/chat/completions`。模型和地址也可通过 `-model`、`-base-url` 指定；`-h` 查看帮助。完整配置、构建和退出码说明见 [快速开始](doc/getting-started.md)。

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
