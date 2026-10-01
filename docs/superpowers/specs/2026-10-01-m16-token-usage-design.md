# M1.6：真实 Token Usage 设计

## 目标

让 `drift chat` 的 `/status` 显示 Provider 实际返回的 Token 用量，而不是把 Context 字节估算伪装成 Token：

```text
Tokens: 8,990 in / 121 out
```

当前使用的 DeepSeek Chat Completions SSE 支持 `stream_options.include_usage=true`，最后一个 `data: [DONE]` 之前的 chunk 可携带 `prompt_tokens`、`completion_tokens` 与 `total_tokens`。来源：[DeepSeek Chat Completions API](https://api-docs.deepseek.com/api/create-chat-completion/)。

## 范围

1. OpenAI Compatible 请求在 `stream=true` 时发送：

   ```json
   "stream_options": {"include_usage": true}
   ```

2. SSE 解析器读取 usage chunk；该 chunk 允许 `choices` 为空。
3. Provider 无关的 `llm.Completion` 增加可选 usage：输入、输出、总 Token。
4. Agent 对每次成功完成的模型请求发出安全 usage 事件；它只包含计数，不包含提示词、文件内容、API Key 或 Provider URL。
5. chat 在当前完整会话中累计 usage；持久会话将累计计数保存到快照，`--resume` 后继续累计。
6. `/status` 显示累计 Token：

   ```text
   Tokens: 8,990 in / 121 out
   ```

7. 若成功请求没有 usage，状态必须明确为：

   ```text
   Tokens: unavailable
   ```

   若一段会话中只有部分成功请求返回 usage，则显示：

   ```text
   Tokens: 8,990 in / 121 out (partial)
   ```

## 数据流

```text
OpenAI Compatible SSE usage chunk
        ↓
llm.Completion.Usage
        ↓
agent EventModelUsage
        ↓
chat usage accumulator
        ↓
conversation snapshot / safe Session audit /status
```

`/compact` 同样属于一次 Provider 请求，因此它的 usage 计入会话累计值；`/status`、`/clear`、普通 `status` 等纯本地命令不产生 usage。

## 数据模型

```go
type Usage struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int
}
```

- `Completion.Usage` 为 `nil`：Provider 没有报告 usage；
- 三个值必须为非负整数；
- `TotalTokens` 允许为 0 或与输入/输出之和不同，因为部分 Provider 可能含缓存或推理 Token；展示只使用输入和输出；
- 旧会话快照没有 usage 字段时按“未报告”处理，保持可恢复。

## 持久化与审计

- 完整会话快照可保存累计输入、输出、已报告请求数和未报告请求数；这些是恢复数据，不是脱敏审计。
- `.drift/sessions/*.jsonl` 只记录 usage 数字、事件类型和 stage，不记录任何正文。
- `conversation list/show` 仍默认只展示元数据；是否将累计 Token 加入列表属于后续 UI 决策，不属于 M1.6。

## 错误与兼容性

- 解析到格式错误、负数或非整数 usage 时，按 SSE 无效 JSON/协议错误处理，不使用猜测值。
- Provider 没有 usage 字段不是错误：请求继续成功，状态显示 `unavailable` 或 `(partial)`。
- M1.6 面向支持 OpenAI Chat Completions `stream_options` 的 Provider；不为不支持该字段的老旧服务做自动重试，避免一次用户请求被静默重复发送。
- 不从文本、思维链或字节数估算 Token。

## 验收标准草案

| ID | 验证方法 | 通过阈值 | 失败判定 |
| --- | --- | --- | --- |
| AC-M16-001 | 本地模拟 SSE 返回 usage chunk | 请求含 `stream_options.include_usage=true`，Completion 正确解析三个计数 | 丢失或错误解析 usage |
| AC-M16-002 | Agent 完成含 usage 的直接回答与工具调用回答 | 每个成功 Provider 请求产生一个安全 usage 事件 | 重复计数、漏计或泄露正文 |
| AC-M16-003 | chat 多轮后 `/status` | 显示累计 `in / out`；纯本地命令不增加计数 | 用字节估算冒充 Token |
| AC-M16-004 | 模拟缺失 usage 与部分缺失 usage | 分别显示 `unavailable` 与 `(partial)` | 把未知 usage 显示为 0 或完整值 |
| AC-M16-005 | 新建、恢复、`--no-session` 和 `/compact` | 持久会话恢复累计值；临时会话只在进程内累计；压缩用量计入 | 恢复丢失或跨会话错误混入 |
| AC-M16-006 | Session/Trace/快照检查 | 审计只含数字和事件名，无正文、密钥、Provider URL 或绝对路径 | 日志泄露敏感信息 |

## 不做

- 不实现计费金额、模型价格表或跨 Provider 成本换算；
- 不展示推理链内容；
- 不新增第二个 Provider；
- 不改变上下文 1 MiB 字节上限；Context 与 Tokens 保持两个不同指标；
- 不自动重试不兼容 `stream_options` 的请求。
