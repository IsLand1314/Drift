# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构演进建议见 `doc/architecture.md`。

## M0 第一步：单次流式对话

- 单 Go Module、标准库实现、print CLI。
- `drift -p "你好"` 发送单条用户消息，调用 OpenAI Compatible Chat Completions SSE 接口。
- 配置：`OPENAI_API_KEY`、`OPENAI_MODEL`、`OPENAI_BASE_URL`；模型和地址允许命令行覆盖。
- 文本实时写入 stdout；错误写入 stderr；Ctrl+C 取消；请求总时限为五分钟。
- 本次不包含多轮历史、工具执行、Agent Loop、会话存储或 TUI。因此无法读取真实目录或文件。

## 验收

- 模拟 HTTP 服务验证请求参数、增量到达、错误、断流和取消。
- 缺少配置在请求前报错；`-h` 不需要密钥。
- `go test ./...`、`go vet ./...`、`go build ./cmd/drift` 通过。
- 真实模型联调需要有效的地址、模型与环境变量密钥；模拟测试不能替代真实联调。

实现使用同步增量回调和返回 error，代替架构草案中的双通道，减少第一步的 goroutine 生命周期管理。
