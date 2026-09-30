# M1.1：交互上下文管理设计

## 状态

设计阶段，尚未实现代码。

## 目标

在 M1.0 `drift chat` 的基础上，控制进程内对话上下文的增长，避免长时间交互把越来越多的 user、assistant、tool 消息重复发送给 Provider。用户可以显式清空当前上下文，但不引入 Session 恢复、自动摘要或写入能力。

## 方案选择

### 方案 A：硬上限 + `/clear`（推荐）

- Runner 在每次 Provider 请求前估算当前消息上下文的 UTF-8 字节数；
- 超过固定上限时，在发出请求前返回 `agent_context_limit`；
- chat 增加 `/clear`，清空当前进程的消息历史，但保留 workspace、focus、Provider 和工具注册表；
- 用户决定何时清空，避免运行时自动丢失上下文。

优点是实现简单、行为可预测、不会偷偷改变用户语义；代价是达到上限后需要用户主动 `/clear`。

### 方案 B：自动截断旧消息

可以保留最近若干轮并删除更早消息，但模型可能失去关键文件结论，用户也难以知道哪些上下文被丢弃。M1.1 不采用。

### 方案 C：模型摘要压缩

通过额外 Provider 请求生成摘要，再替换旧消息。它会增加费用、失败路径和隐私处理复杂度，也需要明确摘要是否可写入 Session。推迟到有真实长会话数据后再评估。

## 运行时设计

### Runner

现有 `agent.Runner` 继续拥有进程内 `messages`。新增两个行为：

1. `ContextBytes()`：按消息 role、content、tool call arguments、tool call id 和 reasoning content 的 UTF-8 字节估算上下文大小；系统指令和工具 schema 也计入本次请求估算。
2. `ResetContext()`：清空 user、assistant、tool 消息；workspace、focus、client 和 registry 保持不变。

建议初始上限为 **1 MiB**。这是字节预算，不声称等同于模型 token 数；后续可根据真实 Provider 失败数据调整。单轮已有的 4 次模型请求、6 次工具调用、512 KiB 工具结果和 128 KiB 单文件限制继续有效。

### 请求前检查

每次准备调用 Provider 前执行上下文预算检查：

```text
追加本轮 user prompt
  ↓
估算 system + messages + tools
  ↓ 超过 1 MiB
发出 agent_context_limit 错误事件
不发 Provider 请求
  ↓ 未超过
按 M1.0 Agent Loop 正常请求
```

如果一次工具调用后新增结果使下一次请求超限，也在下一次 Provider 请求前失败；已经落盘的工具调用和结果仍然只按 M0.9 规则保存脱敏审计。

### Chat 命令

- `/clear`：清空内存上下文并继续等待下一行输入；不请求 Provider，不新建 Session 文件。
- `exit`、`/exit`、`quit` 和 EOF：保持 M1.0 行为，结束进程。
- 空行：保持 M1.0 行为，忽略。
- `/clear` 不清除 workspace、focus、配置、工具注册表或已有 JSONL；它只影响后续请求携带的消息。

### 错误与审计

- 超限错误使用稳定 `stage=agent_context_limit`，终端显示简短中文错误并提示 `/clear`；该错误只结束当前输入轮次，chat 进程继续等待下一行；
- 错误发生在 Provider 请求前，因此不产生新的 Provider 请求；
- Session 只记录错误事件和安全字节信息，不写入完整上下文、提示词、文件内容或回答；
- M1.0 的 `agent_empty_response`、Provider 阶段错误和退出码保持不变。

## 明确不做

- 不从 `.drift/sessions/*.jsonl` 恢复上下文；
- 不把审计 JSONL 作为模型消息；
- 不自动删除旧消息；
- 不调用模型生成摘要；
- 不增加写文件、编辑文件、Shell、exec 或新的 Provider；
- 不实现 TUI、跨进程历史或 PowerShell 上箭头历史控制。

## 调用流程

```text
chat 启动
  -> 创建 Runner
  -> 输入普通问题
  -> Runner 追加 user 消息并检查上下文预算
  -> 未超限：执行现有只读 Agent Loop
  -> 超限：写 agent_context_limit 审计事件，结束当前轮次并继续等待（用户可输入 /clear）
  -> 输入 /clear：Runner.ResetContext，继续等待
  -> 输入 exit：关闭 Session，退出
```

## 测试与验收

### 自动化测试

- `Runner.ContextBytes` 能稳定计算普通消息、tool call、tool result 和 reasoning content；
- 超过上限时返回 `agent_context_limit`，Provider 请求计数为 0；
- `/clear` 后下一次请求不包含清除前的 user、assistant、tool 消息；
- `/clear` 不改变 workspace、focus、工具 schema 和 Session Writer；
- 单次 `-p` 模式、M1.0 多轮模式、空响应错误和现有工具边界测试保持通过。

### 人工验收

```powershell
go run ./cmd/drift chat -w .
> 读取 README.md
> /clear
> 这个项目的作用是什么？
> exit
```

验收要点：

1. `/clear` 后第二个问题不能依赖第一轮的内存上下文；
2. `/clear` 不产生 Provider 请求和额外 Session 文件；
3. 长对话达到上限时显示明确错误，chat 仍保持运行，输入 `/clear` 后可继续，并在 `session show` 中看到 `agent_context_limit`；
4. 不传 `/clear` 时，M1.0 的多轮行为保持不变；
5. `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## 后续升级条件

只有当真实使用证明“硬上限 + `/clear`”频繁打断工作，才考虑 M1.2 的摘要压缩或显式会话恢复；届时必须重新设计隐私、授权、失败恢复和 Session 数据格式。
