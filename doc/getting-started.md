# M0.2.1：运行受限多文件只读 Agent

Drift 是一个本地只读 Coding Agent Runtime。它使用 OpenAI Compatible Chat Completions SSE 连接模型，并只提供一个受限的 `read_file` 工具。

## 1. PowerShell 启动

需要 Go 1.26+ 和支持 Chat Completions SSE 的模型服务：

```powershell
$env:OPENAI_API_KEY = "你的 API Key"
$env:OPENAI_BASE_URL = "https://api.deepseek.com"
$env:OPENAI_MODEL = "deepseek-v4-flash"
go run ./cmd/drift -p "解释 README.md 的项目作用"
```

API 地址是根地址，不含 `/chat/completions`。`-model` 和 `-base-url` 可以覆盖环境变量，API Key 只从 `OPENAI_API_KEY` 读取，切勿写入仓库或终端日志。

程序把启动目录作为 workspace。M0.2 的单文件解释行为保持不变；M0.2.1 中模型可按需请求读取最多四个文件，再输出最终解释或摘要。首轮直接回答会在收到 `stop` 后输出；读取路径则流式输出第二轮文字，首轮文本、推理和工具过程不会写入 stdout。成功时会补一个结尾换行；若第二轮失败，已写入的部分回答会保留在 stdout，错误信息写入 stderr 且以 1 退出。

## 2. 明确边界

一次命令最多执行两次模型请求：第一轮只能选择直接回答，或调用最多四次 `read_file`；调用后把所有结果放进同一个第二轮请求，且第二轮没有任何工具。直接回答只需要第一轮。

`read_file` 只能读取 workspace 内的相对常规文件，每个文件最多 128 KiB，单次任务成功读取内容合计最多 512 KiB。它拒绝绝对路径、`..`、符号链接逃逸、目录和非常规文件。宽泛问题（例如要求介绍整个项目）只有在模型把读取控制在四个文件内时才会继续生成摘要；需要更多文件会得到明确的受限上限错误。Drift 没有写入、删除、重命名或修改文件的工具，也不能执行 shell 命令或运行程序。

## 3. 参数、取消和退出码

```powershell
go build -o drift.exe ./cmd/drift
./drift.exe -p "解释一下 Go 的 context" -model "你的模型名称"
./drift.exe -h
```

地址默认 `https://api.openai.com/v1`。缺少 Key、模型、提示词或命令参数错误时不会发出请求。Ctrl+C 取消正在进行的请求；每次模型请求最多五分钟。

无法解析读取参数或找不到可读取文件时，`read_file` 会把脱敏失败结果交给第二轮模型；若第二轮正常完成，命令仍以 0 退出。网络、SSE 协议、无效 Agent 状态或 stdout 错误才会终止运行。

| 情况 | 退出码 |
| --- | ---: |
| 正常完成 | 0 |
| 网络、协议、无效 Agent 状态或 stdout 错误 | 1 |
| 参数或配置错误 | 2 |
| Ctrl+C / context 取消 | 130 |

## 4. 开发检查

```powershell
go test ./...
go vet ./...
go build ./cmd/drift
```

测试使用本地模拟 SSE 服务，不需要 API Key，也不调用外部模型。当前调用链是 `cmd/drift → app → agent → llm/openai`；app 负责命令行、模型配置、workspace 和 stdout/stderr，agent 负责受限的两轮、最多四文件只读循环。
