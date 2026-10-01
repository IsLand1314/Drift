# M1.9：Anthropic Provider 设计

## 目标

增加第二个可选 Provider：Anthropic Messages API，同时保持 Agent、工具、会话、取消和 `/status` 不感知具体 Provider。M1.9 的价值是验证 `internal/llm.Client` 的协议归一化边界，而不是增加新的 Agent 能力。

协议依据：

- [Anthropic Messages API](https://docs.anthropic.com/en/api/messages)
- [Anthropic Tool Use：Define tools](https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools)
- [Anthropic Streaming Messages](https://platform.claude.com/docs/en/build-with-claude/streaming)

Anthropic 的工具定义使用 `name`、`description`、`input_schema`；工具调用以 `tool_use` content block 返回，工具结果以 `tool_result` content block 回传。请求使用 `x-api-key`、`anthropic-version` 和 Messages SSE；这些 Provider 专有字段只存在于 `internal/llm/anthropic`。

## 配置与 CLI

新增：

```text
-provider openai|anthropic
```

默认值为 `openai`，保持现有命令完全兼容。

配置优先级仍为命令行 > 进程环境 > `.env` > 默认值：

| Provider | API Key | Base URL | Model |
| --- | --- | --- | --- |
| `openai` | `OPENAI_API_KEY` | `OPENAI_BASE_URL` | `OPENAI_MODEL` |
| `anthropic` | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL` | `ANTHROPIC_MODEL` |

Anthropic 默认 Base URL 为 `https://api.anthropic.com/v1`。API Key 不增加命令行参数，不写入错误、Trace、Session 或完整会话快照。

`-base-url`、`-model` 仍覆盖对应 Provider 的环境配置；其他 Provider 的 Key 不参与请求。未知 Provider、缺少对应 Key 或模型在 HTTP 请求前返回退出码 2。

## 分层设计

### `internal/llm/anthropic`

负责：

- 构造 `POST /messages` 请求；
- 把统一 `llm.Message` 转成 Anthropic content blocks；
- 把统一 `llm.ToolDefinition` 的 OpenAI 函数包装解包为 Anthropic `input_schema`；
- 解析 `text_delta`、`input_json_delta`、`tool_use`、`message_delta` 和 usage；
- 将 Provider 错误映射为现有 `llm.ProviderError` stage。

不负责：Agent Loop、工具执行、workspace、Session、TUI 或重试。

### `internal/app`

只负责根据 `-provider` 选择 Provider、读取对应配置和把实例注入现有 `modelClient`。Agent 继续只依赖 `llm.Client`。

### `internal/llm`

仅在确有必要时扩展 Provider 无关字段。Anthropic 所需的 `max_tokens` 使用适配器内的固定安全上限，不把 Anthropic 专有参数泄露到 Agent Request。

## 消息映射

统一消息到 Anthropic 的映射如下：

| Drift 消息 | Anthropic 消息 |
| --- | --- |
| `system` | 顶层 `system` 字符串 |
| `user` | `role=user`，text content block |
| `assistant` 文本 | `role=assistant`，text block |
| `assistant.ToolCalls` | `role=assistant`，`tool_use` blocks，参数 JSON 解码为 object |
| `tool` | `role=user`，`tool_result` block，`tool_use_id` 使用 `ToolCallID` |

Provider 适配器必须保留工具调用顺序和 ID；无效工具参数 JSON、缺失 tool ID 或不支持的消息形状都返回受控 Provider/协议错误，不猜测修复。

## SSE 与 usage

适配器使用 `bufio.Scanner`，沿用 OpenAI Provider 的单行 1 MiB 和事件 1 MiB 上限。事件状态机只接受合法顺序；收到 `message_stop` 后才成功结束，提前断流继续使用 `provider_sse_disconnected`。

- `content_block_delta` 的 `text_delta` 转成 `llm.StreamEvent.Text`；
- `input_json_delta` 按 content block index 聚合为 `ToolCallDelta.Arguments`；
- `content_block_start` 的 `tool_use` 提供 ID、名称和 index；
- `message_start.message.usage.input_tokens` 与 `message_delta.usage.output_tokens` 汇总为 `llm.Usage`；
- 缺失 usage 时返回成功 Completion 但 usage 为 nil，沿用 M1.6 的 `unavailable/partial` 语义。

## 安全与错误

- 请求只发送 `x-api-key`、固定 `anthropic-version: 2023-06-01`、`content-type` 和 `accept: text/event-stream`；
- HTTP 非 2xx 不回显响应正文；
- SSE 错误、无效 JSON、超长事件、断流和超时复用现有稳定 stage；
- 不支持 Anthropic server tools、computer use、thinking、prompt caching、beta headers 或自动重试；
- Provider 切换不改变三个只读工具和 workspace 路径边界。

## 测试策略

- `internal/llm/anthropic`：用 `httptest.Server` 验证请求头、`max_tokens`、system 拆分、工具 schema 映射、消息/工具结果映射、SSE 文本/工具/usage 聚合和错误 stage；
- `internal/app`：验证 provider flag、环境变量优先级、缺 Key/模型和未知 Provider 在请求前失败；
- `internal/agent`：使用 fake `llm.Client` 验证 Agent 不依赖 Provider 类型，现有工具循环不回归；
- 全量执行 `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check`。

## 非目标

不实现 Provider 自动选择、故障转移、费用统计、模型列表、OAuth、Bedrock/Vertex 认证、第二套工具协议或新的 Agent Loop。
