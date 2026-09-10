# M0 第一步：运行流式对话

当前已实现单次文本对话；工具调用、目录读取、多轮历史、JSONL 会话与 TUI 留待后续步骤。正式范围与验收标准见 [spec/current.md](../spec/current.md)。

## PowerShell 启动

需要 Go 1.26+ 和支持 Chat Completions SSE 的模型服务：

```powershell
$env:OPENAI_API_KEY = "你的 API Key"
$env:OPENAI_BASE_URL = "https://你的服务地址/v1"
$env:OPENAI_MODEL = "你的模型名称"
go run ./cmd/drift -p "你好，介绍一下自己"
```

地址默认 `https://api.openai.com/v1`，应填 API 根地址，不含 `/chat/completions`。密钥只从环境变量读取，不要提交到仓库。

```powershell
go build -o drift.exe ./cmd/drift
./drift.exe -p "解释一下 Go 的 context" -model "你的模型名称"
./drift.exe -h
```

`-model` 和 `-base-url` 覆盖环境变量。stdout 输出回复，stderr 输出帮助和错误。Ctrl+C 取消，单次请求最多五分钟。退出码：0 成功、1 请求或输出失败、2 配置或参数错误、130 取消。失败时保留已输出的部分回复。

## 开发检查

```powershell
go test ./...
go vet ./...
go build ./cmd/drift
```

测试使用本地模拟服务，不需要密钥，不调用外部模型。只依赖标准库，因此没有 `go.sum`。

当前调用链：`cmd/drift → app → llm/openai + ui/print`。print 只依赖统一的 `llm.Client`；实现采用同步增量回调，输出失败和取消可以沿调用链直接返回。

协议参考：[Chat Completions API](https://developers.openai.com/api/reference/resources/chat)。当前处理文本增量与 `[DONE]` 标记，非正常结束、服务端错误及提前断流均返回错误。
