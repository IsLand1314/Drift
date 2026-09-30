# M1.4：手动上下文压缩设计

## 1. 目标

M1.4 解决 M1.1 的 1 MiB 上下文上限导致对话只能 `/clear` 的问题。用户可以在 `drift chat` 中查看当前上下文占用，并通过手动 `/compact` 请求模型生成摘要，保留最近几轮消息后继续对话。

本阶段只做显式、可验证的手动压缩，不引入后台任务、自动触发、复杂历史树或外部存储。

## 2. CLI 交互契约

```text
> /stats
上下文：13868 / 1048576 bytes
消息：7
剩余：1034708 bytes

> /compact
正在压缩当前对话上下文...
上下文已压缩：保留最近 4 条消息
```

规则：

- `/stats` 只读取 Runner 内存，不请求 Provider、不新增审计事件、不修改快照。
- `/compact` 只在 chat 中有效；单次 `-p` 不增加压缩参数。
- `/compact` 发起一次**不带工具 schema**的 Provider 请求，摘要请求计入当前 chat 运行的模型请求预算。
- 压缩成功后保留一个摘要消息和最近一组完整消息；压缩失败、取消或输出失败时保留原上下文不变。
- 普通文本 `stats`、`compact` 继续作为普通用户输入；只有带 `/` 的命令才由 Runtime 处理。

## 3. 压缩数据流

```text
/stats
  -> Runner.ContextBytes / Messages
  -> 终端输出安全计数

/compact
  -> 复制当前消息，构造无工具摘要请求
  -> Provider 返回摘要
  -> 校验非空且非伪工具文本
  -> 原子替换 Runner 消息为 [summary, recent messages]
  -> 持久模式保存快照
```

摘要请求的系统约束要求模型：

- 只输出对后续编码分析有用的事实、已读取文件路径、已确认结论和未完成任务；
- 不输出 Markdown 外壳、工具调用或 DSML 伪工具格式；
- 不声称执行过未发生的写入或命令；
- 不主动复述 API Key 等明显凭据。

这不是安全脱敏保证：原始上下文仍会发送给当前 Provider，摘要也可能包含用户输入或文件内容。

## 4. Runner 接口

新增最小接口：

```go
type CompactResult struct {
    Summary      llm.Message
    KeptMessages []llm.Message
}

func (r *Runner) Compact(ctx context.Context) (CompactResult, error)
```

实现要求：

- 当前消息不足以压缩时返回可见提示，不请求 Provider；建议阈值为至少 2 条非 system 消息。
- 摘要请求使用当前 Provider，但 `Tools` 为空；不执行任何工具。
- `Compact` 内部先复制旧消息，只有 Provider 成功、摘要非空且通过格式守卫后才替换 `r.messages`。
- 保留最近两轮完整交互，不能截断 assistant tool call 与对应 tool result 配对；若配对不足，保留完整消息组。
- `ContextBytes()` 在替换后重新计算；持久 chat 保存新的消息快照。
- `ResetContext`、`NewRunnerWithMessages` 和既有 Agent Loop 行为保持兼容。

## 5. 事件、审计与失败处理

- 增加安全事件 `compaction_started`、`compaction_finished` 和 `compaction_error`；审计只记录消息数、字节数、保留消息数、错误 stage，不记录摘要正文。
- `--trace` 可显示压缩开始、完成、前后字节数和错误 stage，但不输出摘要正文。
- `/compact` Provider 超时、HTTP/SSE 错误、空响应、DSML 文本或取消时，chat 继续运行并保留旧上下文；错误写入审计后显示简短提示。
- 持久模式中，只有压缩成功且快照保存成功后才认为上下文完成切换；快照保存失败时恢复旧 Runner 消息，避免内存和磁盘不一致。
- `/clear` 仍然是不可逆的显式清空；`/compact` 不删除旧审计文件，但新的完整快照只保存压缩后消息。

## 6. 安全与边界

- `/stats` 不显示提示词、回答、工具参数、文件内容或绝对路径。
- `/compact` 不增加工具权限，仍不能写文件、删除文件或执行命令。
- 摘要由当前 Provider 生成，不能作为绝对事实或安全擦除证明；敏感内容可能在 Provider 请求和本地完整快照中出现。
- 不实现自动阈值触发、后台压缩、多个摘要版本、摘要编辑、跨 workspace 压缩或云端存储。

## 7. 验收标准

| ID | 验证方法 | 通过阈值 |
| --- | --- | --- |
| AC-M14-001 | chat 输入 `/stats` | 只输出消息数、当前/上限/剩余字节；Provider 请求数为 0 |
| AC-M14-002 | chat 输入 `/compact`，使用本地模拟 Provider | 请求不包含 tools；成功后保留摘要和最近完整消息 |
| AC-M14-003 | 模拟 Provider 超时、空响应、DSML 或取消 | 原上下文不变；chat 可继续；审计只有安全计数和 stage |
| AC-M14-004 | 持久 chat 压缩后退出并 `--resume` | 恢复的是压缩后上下文；快照不含 Provider 配置字段 |
| AC-M14-005 | 压缩后继续请求模型 | 请求上下文包含摘要和最近消息，不包含已被压缩的旧正文 |
| AC-M14-006 | `go test ./... -count=1`、`go vet ./...`、构建、`git diff --check` | 全部退出码为 0 |

## 8. 明确不做

本阶段不实现自动压缩、后台压缩、摘要树、fork、`/undo`、摘要正文编辑、加密、云同步、跨 workspace 恢复和正文搜索。
