## 单文件只读 Read Agent

> 状态：已完成

### 1. 目标

在单次流式对话之上，完成一个最小但真实的只读 Agent 闭环：模型可以请求 read_file，Drift 在当前工作目录内读取文件，把结果作为 Tool Result 回传给模型，模型再输出最终回答。

验收命令：

~~~powershell
go run ./cmd/drift -p "解释 README.md 的项目作用"
~~~

模型应请求 read_file({"path":"README.md"})，随后根据本地 README 输出解释。

#### 1.1 阶段范围

本阶段只包含：

- 一个原生 OpenAI Compatible Function Tool：read_file；
- 每个任务最多一次工具调用、最多两次模型请求；
- 当前 workspace 内的普通文件读取；
- Tool Call、Tool Result、最终文本的流式协议；
- DeepSeek reasoning_content 的回放。

明确不包含：search、write、edit、exec、工具注册表、权限交互、会话持久化、并发工具、多 Agent、TUI、MCP 和多 Provider。

如果读取多文件结果如下：

```powershell
PS F:\code\drift> go run ./cmd/drift -p "谈谈当前的整个项目"
错误： agent: expected exactly one tool call
exit status 1
```

- 因为这个问题太宽泛，模型可能同时想读取：README.md、doc/architecture.md、doc/getting-started.md、spec/current.md
- 但当前 M0.2 的限制是：一次命令最多一次 read_file 工具调用、一次命令最多两次模型请求
- 所以模型返回了多个 tool call，Agent 检测到：`agent: expected exactly one tool call `然后主动终止。
- 这不是 Provider 请求失败，而是当前 Agent 按设计拒绝了“多文件读取”。

#### 1.2 调用流程

~~~mermaid
sequenceDiagram
    participant U as 用户
    participant A as agent.Run
    participant P as OpenAI Compatible Provider
    participant R as read_file

    U->>A: prompt
    A->>P: user message + read_file schema
    alt 模型直接回答
        P-->>A: text + stop
        A-->>U: 首轮文本
    else 模型请求读取
        P-->>A: assistant tool_calls
        A->>R: 校验路径并读取
        R-->>A: Tool Result 或脱敏错误
        A->>P: user + assistant tool call + tool result
        P-->>A: 最终文本 + stop
        A-->>U: 第二轮流式文本
    end
~~~

第一轮文本先缓冲：如果模型选择工具调用，首轮文字不会泄露到 stdout；如果模型直接以 stop 完成，缓冲文本才会输出。第二轮文本实时输出。

### 2. Provider 协议

llm.Client.Stream 通过同步事件回调输出文本、推理内容和工具调用片段，同时返回完整的 Assistant Message。

OpenAI 适配器负责：

- 将 ToolCall 编码为 function.name 和 function.arguments；
- 按 tool_calls[].index 聚合参数分片；
- 聚合文本、reasoning_content 和 finish_reason；
- 要求 SSE 收到 [DONE]；
- 保留 1 MiB 事件限制和服务端错误脱敏。

DeepSeek 思考模式下，首轮 Assistant 的 reasoning_content 必须原样带入第二轮请求，否则 Provider 可能返回 400。

### 3. read_file 边界

工具只接收一个相对路径，并严格拒绝：

- 空路径、绝对路径和任何 .. 路径段；
- 指向 workspace 外部的符号链接；
- 目录、设备等非普通文件；
- 不存在的文件；
- 大于 128 KiB 的文件。

实际读取使用 Go 1.26 的 os.OpenRoot/Root.Open 锚定 workspace，再对已打开的文件句柄检查类型和大小，避免检查路径后再次打开造成边界竞态。

模型提供的 JSON 参数仍必须由 Drift 自己解析：未知字段、非法 JSON 和缺失 path 都会变成安全的工具错误，不会把原始错误内容直接输出给用户。

### 4. Agent 决策规则

1. 第一轮发送 user message 和一个 read_file schema。
2. 第一轮 stop 且没有工具调用：输出缓冲文本并成功结束。
3. 第一轮存在工具调用：只接受恰好一个名为 read_file 的调用。
4. 读取成功或失败都追加 role: tool 消息，再发起唯一的第二轮请求。
5. 第二轮不发送 tools；只有 stop 且没有工具调用才算成功。
6. 取消、Provider 错误、SSE 协议错误、非法 Agent 状态和 stdout 写入错误都会终止任务。

### 5. 如何验收

#### 5.1 自动化验收

在项目根目录执行：

~~~powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
~~~

通过标准：

- 所有 Go 测试通过；
- go vet 无诊断；
- CLI 构建成功；
- 没有空白错误。

#### 5.2 Agent Loop 验收

~~~powershell
go test ./internal/agent -run 'TestRun(ReadRoundTrip|FirstStopWithToolCallUsesReadRoundTrip|ReadFailuresStillReachSecondTurn|PreservesReasoningContentForSecondRequest|RejectsSecondStopWithToolCall)$' -count=1
go test ./internal/app -run 'TestRun(ReadRoundTrip|DirectAnswer)$' -count=1
~~~

重点检查：

- 直接回答只发一次请求；
- 读取文件恰好发两次请求；
- 第二次请求包含原始 assistant tool call 和同 ID 的 tool result；
- 第二次请求不包含 tools；
- 首轮工具调用的文字不会出现在 stdout；
- 第二轮失败时，已经流出的部分文本可以保留，但进程返回错误。

#### 5.3 文件安全验收

~~~powershell
go test ./internal/tool -run 'TestRead|TestReadDefinition' -count=1
~~~

测试必须覆盖：

- 成功读取 README.md；
- 非法 JSON、缺少 path、未知字段和空路径；
- 绝对路径、../outside.txt 和 workspace 内部的 unused/../README.md；
- 目录、超出 128 KiB 的文件和外部符号链接。

#### 5.4 Provider 协议验收

~~~powershell
go test ./internal/llm/openai -run 'TestStreamAggregatesToolCall|TestReadStreamProtocolRegressions' -count=1
~~~

测试必须确认：

- 工具参数跨多个 SSE 片段后仍能还原完整 JSON 字符串；
- 不同 index 的工具调用不会串参数；
- reasoning_content 被保留；
- [DONE]、回调错误、断流、超长事件和错误 finish reason 可预测处理。

#### 5.5 真实模型验收

只在本地环境设置密钥，不要把密钥写入仓库或命令历史：

~~~powershell
$env:OPENAI_BASE_URL = "https://api.deepseek.com"
$env:OPENAI_MODEL = "deepseek-v4-flash"
$env:OPENAI_API_KEY = "你的密钥"
go run ./cmd/drift -p "解释 README.md 的项目作用"
~~~

结果如下：

```powershell
PS F:\code\drift> go run ./cmd/drift -p "解释 README.md 的项目作用"
## Drift 项目作用

**一句话概括**：Drift 是一个用 Go 编写的**本地只读 Coding Agent Runtime**（编码智能体运行时），目前处于 M0.2 阶段，能力极其克制。

### 它现在能做什么

最小可用的"读文件 + 解释"闭环：

1. 接收一个提示（`-p "..."`），例如"解释 README.md 的项目作用"
2. 模型首轮决定：**直接回答**，还是**调用一次 `read_file` 工具**读取当前工作目录内的某个文件
3. 若读取了文件，第二轮把文件内容交给模型，生成最终回答
4. 最终结果只输出到 stdout

### 它明确不能做什么

这是这个项目的设计重点——**能力边界是刻意收紧的**：

- ❌ 写文件、删除文件
- ❌ 执行命令、运行程序
- ❌ 越出当前工作目录读取（受工作目录限制）
- ❌ `read_file` 最多调用一次，一次命令最多发起两次模型请求

所以它不是一个通用 Agent 框架，而是一个**"只读、单文件、两轮"的受控运行时**。

### 定位理解

从约束方式看，Drift 更像是在为一个完整的 Coding Agent 打地基：先把"工具调用循环 + 模型交互"这条主链路走通，但把危险的副作用（写、删、执行）全部关掉，只留读取，从而保证安全且易于验证。M0.2 这个版本号也印证了它是渐进式开发中的早期里程碑。

### 工程特点

- 仅依赖 **Go 标准库**，Go 1.26+
- 通过 OpenAI Chat Completions SSE 协议对接任意兼容的模型服务（`OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL`，或 `-model` / `-base-url` 参数）
- 测试使用**本地模拟服务**，跑 `go test ./...` 不需要 API Key

### 延伸阅读

README 指向三份更详细的文档：`spec/current.md`（交付范围与验收标准）、`doc/architecture.md`（架构草案）、`doc/getting-started.md`（配置、构建、只读边界、退出码）。如果你想了解具体退出码或只读边界的技术实现，我可以继续读这几份文件。
```

成功表现：

- 终端只看到最终解释，不看到工具调用 JSON 或 API 调试信息；
- 最终内容确实基于当前目录的 README.md；
- 程序不会修改 README 或其他仓库文件；
- 不会执行 shell 命令。

读取不存在的文件时，模型可能先收到工具错误，再给出解释；只要第二轮正常完成，程序可以正常退出。网络错误、鉴权错误、SSE 错误和非法 Agent 状态则应写入 stderr 并返回非零退出码。

**宽泛问题的阶段边界**

在 M0.2 单文件版本中执行：

~~~powershell
PS F:\code\drift> go run ./cmd/drift -p "谈谈当前的整个项目"
错误： agent: expected exactly one tool call
exit status 1
~~~

这是因为模型尝试同时读取多个文件，而 M0.2 只允许一次 read_file 调用。该限制促成了后续的 M0.2.1 多文件读取阶段。

### 6. 代码边界

~~~text
internal/llm/client.go             共享 Provider 协议
internal/llm/openai/client.go      OpenAI Compatible JSON/SSE 适配
internal/tool/read.go              受限文件读取
internal/agent/agent.go            两轮 Agent 控制流
internal/app/app.go                CLI、workspace 和退出码接线
~~~

internal/ui/print 已删除，因为输出现在由 Agent 的 emitText 回调直接接入 CLI。


## 受限多文件只读 Agent

> 状态：已完成

### 1. 目标

在单次流式对话之上，完成一个最小但真实的只读 Agent 闭环：模型可以在首轮请求一小组 `read_file` 调用，Drift 在当前工作目录内读取文件，把全部 Tool Result 回传给模型，模型再输出最终回答。M0.2 的单文件行为作为 M0.2.1 的兼容子集保留。

验收命令：

~~~powershell
go run ./cmd/drift -p "解释 README.md 的项目作用"
~~~

模型应请求 read_file({"path":"README.md"})，随后根据本地 README 输出解释。

M0.2 的单文件读取路径保留。M0.2.1 仅扩大同一个 `read_file` 工具在首轮可调用的次数：最多四个文件，每个文件最多 128 KiB，成功读取内容合计最多 512 KiB。一次命令仍最多两次 Provider 请求；首轮的所有读取结果按工具调用顺序组成一个后续请求，第二轮仍没有工具。

#### 1.1 本阶段范围

本阶段只包含：

- 一个原生 OpenAI Compatible Function Tool：read_file；
- 每个任务首轮最多四次 `read_file` 调用、最多两次模型请求；
- 当前 workspace 内的普通文件读取；
- Tool Call、Tool Result、最终文本的流式协议；
- DeepSeek reasoning_content 的回放。

明确不包含：search、write、edit、exec、工具注册表、权限交互、会话持久化、并发工具、多 Agent、TUI、MCP 和多 Provider。

#### 1.2 调用流程

~~~mermaid
sequenceDiagram
    participant U as 用户
    participant A as agent.Run
    participant P as OpenAI Compatible Provider
    participant R as read_file

    U->>A: prompt
    A->>P: user message + read_file schema
    alt 模型直接回答
        P-->>A: text + stop
        A-->>U: 首轮文本
    else 模型请求读取
        P-->>A: assistant tool_calls（最多四个）
        A->>R: 按调用顺序校验路径并读取
        R-->>A: 每个调用的 Tool Result 或脱敏错误
        A->>P: user + assistant tool_calls + 全部 tool result
        P-->>A: 最终文本 + stop
        A-->>U: 第二轮流式文本
    end
~~~

第一轮文本先缓冲：如果模型选择工具调用，首轮文字不会泄露到 stdout；如果模型直接以 stop 完成，缓冲文本才会输出。第二轮文本实时输出。

### 2. Provider 协议

llm.Client.Stream 通过同步事件回调输出文本、推理内容和工具调用片段，同时返回完整的 Assistant Message。

OpenAI 适配器负责：

- 将 ToolCall 编码为 function.name 和 function.arguments；
- 按 tool_calls[].index 聚合参数分片；
- 聚合文本、reasoning_content 和 finish_reason；
- 要求 SSE 收到 [DONE]；
- 保留 1 MiB 事件限制和服务端错误脱敏。

DeepSeek 思考模式下，首轮 Assistant 的 reasoning_content 必须原样带入第二轮请求，否则 Provider 可能返回 400。

### 3. read_file 边界

每次 `read_file` 调用只接收一个相对路径；首轮最多四次调用。每个文件最多 128 KiB，成功读取内容合计最多 512 KiB，并严格拒绝：

- 空路径、绝对路径和任何 .. 路径段；
- 指向 workspace 外部的符号链接；
- 目录、设备等非普通文件；
- 不存在的文件；
- 大于 128 KiB 的文件。

实际读取使用 Go 1.26 的 os.OpenRoot/Root.Open 锚定 workspace，再对已打开的文件句柄检查类型和大小，避免检查路径后再次打开造成边界竞态。

模型提供的 JSON 参数仍必须由 Drift 自己解析：未知字段、非法 JSON 和缺失 path 都会变成安全的工具错误，不会把原始错误内容直接输出给用户。

### 4. Agent 决策规则

1. 第一轮发送 user message 和一个 read_file schema。
2. 第一轮 stop 且没有工具调用：输出缓冲文本并成功结束。
3. 第一轮存在工具调用：只接受一至四个、且全部名为 `read_file` 的调用；超过四个调用返回清晰的受限上限错误。
4. 每次读取成功或失败都按调用顺序追加对应的 role: tool 消息，再发起唯一的第二轮请求。
5. 第二轮不发送 tools；只有 stop 且没有工具调用才算成功。
6. 取消、Provider 错误、SSE 协议错误、非法 Agent 状态和 stdout 写入错误都会终止任务。

### 5. 如何验收



#### 5.1 自动化验收

在项目根目录执行：

~~~powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
~~~

通过标准：

- 所有 Go 测试通过；
- go vet 无诊断；
- CLI 构建成功；
- 没有空白错误。

#### 5.2 Agent Loop 验收

~~~powershell
go test ./internal/agent -run 'TestRun(ReadRoundTrip|FirstStopWithToolCallUsesReadRoundTrip|ReadFailuresStillReachSecondTurn|PreservesReasoningContentForSecondRequest|RejectsSecondStopWithToolCall)$' -count=1
go test ./internal/app -run 'TestRun(ReadRoundTrip|ReadRoundTripMultipleFiles|DirectAnswer)$' -count=1
~~~

重点检查：

- 直接回答只发一次请求；
- 单文件或最多四文件读取恰好发两次请求；
- 第二次请求包含原始 assistant tool calls 和每个同 ID、同顺序的 tool result；
- 第二次请求不包含 tools；
- 首轮工具调用的文字不会出现在 stdout；
- 第二轮失败时，已经流出的部分文本可以保留，但进程返回错误。

#### 5.3 文件安全验收

~~~powershell
go test ./internal/tool -run 'TestRead|TestReadDefinition' -count=1
~~~

测试必须覆盖：

- 成功读取 README.md；
- 非法 JSON、缺少 path、未知字段和空路径；
- 绝对路径、../outside.txt 和 workspace 内部的 unused/../README.md；
- 目录、超出 128 KiB 的文件和外部符号链接。

#### 5.4 Provider 协议验收

~~~powershell
go test ./internal/llm/openai -run 'TestStreamAggregatesToolCall|TestReadStreamProtocolRegressions' -count=1
~~~

测试必须确认：

- 工具参数跨多个 SSE 片段后仍能还原完整 JSON 字符串；
- 不同 index 的工具调用不会串参数；
- reasoning_content 被保留；
- [DONE]、回调错误、断流、超长事件和错误 finish reason 可预测处理。

#### 5.5 真实模型验收

只在本地环境设置密钥，不要把密钥写入仓库或命令历史：

~~~powershell
$env:OPENAI_BASE_URL = "https://api.deepseek.com"
$env:OPENAI_MODEL = "deepseek-v4-flash"
$env:OPENAI_API_KEY = "你的密钥"
go run ./cmd/drift -p "谈谈当前的整个项目"
~~~

预期模型读取一小组项目文件，在一个后续请求中收到全部 tool result，并输出项目摘要。摘要的具体措辞取决于模型；确定的验收标准是恰好不超过两次 Provider 请求、最多四次工具调用、每文件 128 KiB、总量 512 KiB、所有工具结果都在同一次后续请求中，以及没有文件变更。

如果模型认为需要超过四个文件，Drift 会给出明确的受限上限错误，而不是继续发起 Provider 请求或放宽边界。M0.2.1 仍不提供写入、删除、重命名、修改文件、执行 shell 命令或运行程序的能力。

### 6. 代码边界

~~~text
internal/llm/client.go             共享 Provider 协议
internal/llm/openai/client.go      OpenAI Compatible JSON/SSE 适配
internal/tool/read.go              受限文件读取
internal/agent/agent.go            两轮 Agent 控制流
internal/app/app.go                CLI、workspace 和退出码接线
~~~

internal/ui/print 已删除，因为输出现在由 Agent 的 emitText 回调直接接入 CLI。

