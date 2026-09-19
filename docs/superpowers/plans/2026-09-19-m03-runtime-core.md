# M0.3 Runtime Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不扩大只读能力边界的前提下，为 Drift 增加统一 Runtime Event、JSONL 会话审计和可注入的 Tool Registry。

**Architecture:** `app` 创建 Registry、Session Writer 和 Event Fanout；`agent` 只依赖 provider-independent 的 EventSink 与 Registry；CLI 输出层只消费文本事件。当前默认 Registry 只注册 `read_file`，Run 保留兼容包装器，`RunEvents` 负责新的事件流。

**Tech Stack:** Go 1.26+、标准库 `encoding/json`、`os`、`io`、`time`、现有 `httptest` 和 `testing`；不增加第三方依赖。

**Spec:** `doc/m0.3-runtime-core.md`；当前总范围以 `spec/current.md` 为准。

## Global Constraints

- 保持 OpenAI Compatible Chat Completions SSE，不新增 Provider。
- 保持只读 workspace：不写文件、不删除文件、不执行 shell/命令。
- 保持每次运行最多两次模型请求、最多四个 `read_file` 调用、单文件 128 KiB、总量 512 KiB。
- API Key 只来自现有配置来源，不写入 stdout、stderr、事件或 JSONL。
- `.env` 与 `.env.*` 仍不可由 `read_file` 读取。
- JSONL 会话只记录脱敏审计事件，不记录 Authorization header、API Key 或本地绝对路径。
- 提交信息使用中文，作者和提交者使用 `island`。

## Review Focus

- 直接回答路径不能因为事件改造而多发请求或提前输出首轮文本；由 Task 1 的 Agent 兼容测试覆盖。
- 工具失败结果必须脱敏且仍进入第二轮；由 Task 1 的工具结果事件测试覆盖。
- Tool Registry 重复注册和未知工具必须返回稳定错误；由 Task 2 的 Registry 单元测试覆盖。
- JSONL 单行写入中途失败不能静默丢失；由 Task 3 的 Writer 测试覆盖。
- 会话事件不能泄露 API Key 或绝对路径；由 Task 3 的安全序列化测试覆盖。

---

### Task 1: 为 Agent 增加统一 Runtime Event

**Files:**

- Create: `internal/agent/event.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**

- Consumes: 现有 `llm.Client`、`llm.Completion` 和当前 `tool.Read`。
- Produces: `EventType`、`Event`、`EventSink`、`RunEvents`；现有 `Run` 保留原签名并包装 `RunEvents`。

- [ ] **Step 1: 写失败测试，固定 Event 契约**

新增一个脚本客户端测试，调用 `RunEvents`，首轮返回一个 `read_file`，第二轮返回两段文本。断言事件顺序为：

```
text
run_started -> tool_call -> tool_result -> text_delta -> text_delta -> run_finished
```

同时断言工具调用事件包含 ID、名称和原始 JSON 参数，工具结果不包含绝对路径；`Run` 兼容包装器仍只收到两段最终文本。

- [ ] **Step 2: 运行 Agent 定向测试确认失败**

```
powershell
go test ./internal/agent -run 'TestRunEvents|TestRunCompatibilityWrapper' -count=1
```

预期：因为 `RunEvents`、事件类型和事件发射逻辑尚不存在而失败。

- [ ] **Step 3: 实现最小事件流**

在 `internal/agent/event.go` 定义：

```
go
type EventType string
const (
    EventRunStarted EventType = "run_started"
    EventTextDelta EventType = "text_delta"
    EventToolCall EventType = "tool_call"
    EventToolResult EventType = "tool_result"
    EventError EventType = "error"
    EventRunFinished EventType = "run_finished"
)

type Event struct {
    Type EventType
    Text string
    ToolCallID string
    ToolName string
    Arguments string
    Result string
    Error string
}

type EventSink func(Event) error
```

先保持现有 `Run` 的参数和工具执行方式，新增精确签名 `RunEvents(ctx, client, root, prompt string, sink EventSink) error`。Run 只转发 `EventTextDelta`，其他事件不写 stdout。`RunEvents` 在开始、完整工具调用、脱敏工具结果、最终文本增量和正常结束时发事件；错误先发 `EventError` 再返回。取消和 Provider 错误保持现有错误语义。Task 2 再把工具执行参数扩展为 Registry 注入，避免一次任务同时改变两个边界。

- [ ] **Step 4: 运行 Agent 与全量测试**

```
powershell
gofmt -w internal/agent/event.go internal/agent/agent.go internal/agent/agent_test.go
go test ./internal/agent -count=1
go test ./... -count=1
```

- [ ] **Step 5: 提交 Agent 事件改造**

```
powershell
git add internal/agent/event.go internal/agent/agent.go internal/agent/agent_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "重构：增加 Agent Runtime 事件流"
```

### Task 2: 抽象 Tool Registry 并接入 read_file

**Files:**

- Create: `internal/tool/tool.go`
- Create: `internal/tool/registry.go`
- Modify: `internal/tool/read.go`
- Modify: `internal/tool/read_test.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**

- Consumes: Task 1 的 `RunEvents` 和现有 `llm.ToolDefinition`。
- Produces: `tool.Tool`、`tool.Registry`、`tool.NewDefaultRegistry()`；Agent 通过 Registry 查找工具，并保留一个默认 Registry 的兼容入口。

- [ ] **Step 1: 写失败测试，固定 Registry 行为**

测试以下行为：

1. 默认 Registry 只暴露一个名为 `read_file` 的 schema；
2. `Lookup("read_file")` 成功，执行结果与现有 `Read` 一致；
3. 重复注册同名工具返回错误；
4. 未知工具返回 `unsupported tool`，且只发起首轮请求；
5. fake tool 可以被注册并被 Agent 调用，不需要修改 Agent 分派逻辑。

- [ ] **Step 2: 运行定向测试确认失败**

```
powershell
go test ./internal/tool ./internal/agent -run 'Test.*Registry|TestRunWithRegistry' -count=1
```

预期：接口和默认 Registry 尚不存在而失败。

- [ ] **Step 3: 实现 Registry 和 read_file 适配器**

定义：

```
go
type Tool interface {
    Name() string
    Definition() llm.ToolDefinition
    Execute(ctx context.Context, root string, rawArguments string) (string, error)
}

type Registry interface {
    Definitions() []llm.ToolDefinition
    Lookup(name string) (Tool, bool)
}
```

`read_file` 实现 `Tool` 接口；原有 `Read(root, rawArguments)` 作为薄包装保留。将 Agent 新增精确签名 `RunEventsWithRegistry(ctx, client, root, prompt string, registry Registry, sink EventSink) error`，`RunEvents` 调用默认 Registry。Registry 构造时拒绝空名称和重复名称，`Definitions` 返回稳定注册顺序的 schema 副本。Agent 的首轮 schema 来自 Registry，工具调用通过 `Lookup` 执行，现有四文件、大小、路径和 dotenv 限制保持不变。

- [ ] **Step 4: 运行全量验证**

```
powershell
gofmt -w internal/tool/tool.go internal/tool/registry.go internal/tool/read.go internal/tool/read_test.go internal/agent/agent.go internal/agent/agent_test.go
go test ./... -count=1
go vet ./...
```

- [ ] **Step 5: 提交 Tool Registry**

```
powershell
git add internal/tool internal/agent/agent.go internal/agent/agent_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "重构：通过注册表管理只读工具"
```

### Task 3: 实现 JSONL 会话审计 Writer

**Files:**

- Create: `internal/session/session.go`
- Create: `internal/session/jsonl.go`
- Create: `internal/session/jsonl_test.go`

**Interfaces:**

- Consumes: Task 1 的 `agent.Event`。
- Produces: `session.Writer`、`session.Entry`、`session.NewJSONLWriter(path string)`，供 app 做事件扇出。

- [ ] **Step 1: 写失败测试，固定 JSONL 格式和安全边界**

测试：

1. `Append` 写入一行 `version=1`、UTC 时间和事件字段；
2. 多次追加后每行都能独立 `json.Unmarshal`；
3. 工具调用参数中的本地绝对路径被拒绝或脱敏；
4. `OPENAI_API_KEY` 等敏感值不会出现在序列化结果；
5. Writer 创建父目录，并在 `Close` 后拒绝继续追加。

- [ ] **Step 2: 运行定向测试确认失败**

```
powershell
go test ./internal/session -count=1
```

预期：`internal/session` 尚不存在而失败。

- [ ] **Step 3: 实现 JSONL Writer**

定义：

```
go
type Entry struct {
    Version int
    Type string
    Time time.Time
    Text string
    ToolCallID string
    Tool string
    Arguments string
    Result string
    Error string
}

type Writer interface {
    Append(event agent.Event) error
    Close() error
}
```

Writer 使用 `os.OpenFile` 的 append 模式、`bufio.Writer` 和 `json.Encoder`，每次追加后 Flush；app 负责创建 `.drift/sessions/<run-id>.jsonl` 并调用 `NewJSONLWriter(path)`。序列化前拒绝或清理绝对路径和明显的 API Key 字段，失败返回明确错误而不静默丢弃。

- [ ] **Step 4: 运行 Writer 全量测试**

```
powershell
gofmt -w internal/session/session.go internal/session/jsonl.go internal/session/jsonl_test.go
go test ./internal/session -count=1
go test ./... -count=1
go vet ./...
```

- [ ] **Step 5: 提交会话 Writer**

```
powershell
git add internal/session
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：增加 JSONL 会话审计记录"
```

### Task 4: 在 app 中扇出事件到 stdout 和会话

**Files:**

- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `README.md`
- Modify: `spec/current.md`

**Interfaces:**

- Consumes: `agent.RunEvents`、`session.NewJSONLWriter`、默认 Tool Registry。
- Produces: 现有 CLI 输出不变，并在 `.drift/sessions` 生成脱敏审计文件。

- [ ] **Step 1: 写失败集成测试**

扩展现有 `httptest.Server`：运行一次 `-p` 读文件流程，读取生成的 JSONL，断言包含 `run_started`、`tool_call`、`tool_result`、`text_delta`、`run_finished`，stdout 仍为最终答案加一个换行，且会话内容不含测试 API Key、工作区绝对路径或 `.env` 内容。

- [ ] **Step 2: 运行集成测试确认失败**

```
powershell
go test ./internal/app -run 'TestRunWritesSessionAudit' -count=1
```

预期：app 尚未创建 Session Writer 或调用 `RunEvents`，断言失败。

- [ ] **Step 3: 实现事件扇出**

在 app 组装默认 Registry 和 session Writer，使用一个 EventSink：先追加 JSONL，成功后仅对 EventTextDelta 写 stdout。直接回答和工具路径都保持原输出规则；会话初始化或写入失败返回退出码 1，不能吞掉审计错误。`.drift/` 已忽略，不把本地会话加入 Git。

- [ ] **Step 4: 更新用户文档和验收范围**

在 README 和 `spec/current.md` 中说明：`.drift/sessions` 是本地审计目录、当前不支持恢复；M0.3 仍只有 `read_file`，仍不能写文件或执行命令；运行命令和测试命令保持现有形式。

- [ ] **Step 5: 完整验证**

```
powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

- [ ] **Step 6: 提交 app 集成和文档**

```
powershell
git add internal/app/app.go internal/app/app_test.go README.md spec/current.md
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：接入 JSONL 会话审计"
```

### Task 5: 阶段验收

- [ ] 运行 `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check`。
- [ ] 使用本地模拟 Provider 验证直接回答、单文件读取、多文件读取、工具失败和取消路径。
- [ ] 检查 `.drift/sessions/*.jsonl` 可逐行解析，且不包含 API Key、绝对路径和 `.env` 内容。
- [ ] 检查 `git status --short` 只包含本阶段预期变更，未把主工作区用户改动带入。
- [ ] 完成代码审查后再决定是否合并到本地 `master`，不要自动覆盖主工作区未提交内容。

## Plan self-review

- 所有 M0.3 目标都有独立任务：事件、Registry、JSONL、app 集成和验收。
- 任务间接口顺序一致：Task 1 定义 Event，Task 2 使用 Registry，Task 3 序列化 Event，Task 4 扇出两者。
- 没有引入写、删、编辑、exec、TUI、MCP 或第二 Provider。
- 失败、取消、敏感信息和旧 CLI 输出均有对应测试或验收项。
