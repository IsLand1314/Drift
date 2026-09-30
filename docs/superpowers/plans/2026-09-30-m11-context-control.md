# M1.1 Context Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `drift chat` 增加可预测的上下文硬上限和 `/clear`，避免进程内消息无限增长，同时保持 M1.0 的只读工具、审计和单次模式兼容。

**Architecture:** `agent.Runner` 继续拥有进程内消息历史，新增上下文字节估算与显式重置能力。每次 Provider 请求前检查 system 指令、消息、工具 schema 的估算大小；超限产生 `agent_context_limit` 错误事件但不发请求。`chat` 将该错误视为可恢复的当前轮次错误，继续等待 `/clear` 或下一条输入；其他 Provider/工具错误保持 M1.0 的终止行为。

**Tech Stack:** Go 标准库、现有 `internal/agent`、`internal/app`、`internal/llm`、`internal/session`；不新增依赖。

**Spec:** `docs/superpowers/specs/2026-09-30-m11-context-control-design.md`

## Global Constraints

- 上下文硬上限初始为 1 MiB，按 UTF-8 字节估算，不宣称等同于 token 数。
- 只保留 `list_files`、`search_text`、`read_file` 三个只读工具。
- 每轮继续使用最多 4 次模型请求、6 次工具调用、512 KiB 累计工具结果和 128 KiB 单文件限制。
- `/clear` 只清理当前 Runner 内存消息，不清理 workspace、focus、Provider、工具注册表或 JSONL 文件。
- 不实现自动截断、模型摘要、Session 恢复、TUI、写文件、Shell、exec 或新的 Provider。
- 既有 `drift -p`、M1.0 `drift chat`、退出码和 Session 脱敏规则保持兼容。

---

### Task 1: Runner 上下文估算与重置

**Files:**
- Modify: `internal/agent/agent.go`
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Add `const MaxConversationBytes = 1 << 20`。
- Add `func (r *Runner) ContextBytes() int`，返回当前消息和首轮 system/tool schema 估算字节数。
- Add `func (r *Runner) ResetContext()`，清空 `r.messages`，保留 client、root、focus、registry。
- Add `var ErrContextLimit = errors.New("agent: context limit exceeded")`，供 App 判断可恢复的上下文错误。

- [x] **Step 1: Write failing tests for context accounting**

在 `internal/agent/agent_test.go` 增加测试，先执行一轮回答，再断言 `ContextBytes() > 0`；调用 `ResetContext()` 后断言消息历史被清空但 system/tool overhead 仍保留。再用包含 tool call、tool result 和 reasoning content 的消息验证估算值随内容增加，并验证 reset 不改变 tool definitions。

- [x] **Step 2: Run focused tests and confirm RED**

Run: `go test ./internal/agent -run 'TestRunnerContextCanReset|TestRunnerContextBytes' -count=1`

Expected: FAIL because `ContextBytes`, `ResetContext`, `MaxConversationBytes` and `ErrContextLimit` do not exist.

- [x] **Step 3: Implement minimal accounting and reset**

在 `agent.go` 增加只使用标准库的消息估算函数，累加：

```text
len(role) + len(content) + len(tool_call_id) + len(reasoning_content)
+ 每个 tool call 的 id、type、name、arguments
+ len(systemInstruction(focus))
+ 每个工具 definition 的 Type 和 Function 原始 JSON 字节
```

`ContextBytes()` 返回该估算值；`ResetContext()` 使用 `r.messages = nil`。不要把审计 JSONL、提示词脱敏副本或工具结果重新读取进来。

- [x] **Step 4: Run Agent tests**

Run: `go test ./internal/agent -count=1`

Expected: PASS，既有多轮和 reasoning_content 测试保持通过。

### Task 2: Provider 请求前的上下文限制

**Files:**
- Modify: `internal/agent/agent.go`
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- `RunEvents` 在每次 `r.client.Stream` 前检查上下文估算值。
- 超限返回 `ErrContextLimit`，并通过 EventSink 发送 `EventError{Stage: "agent_context_limit"}`。
- 超限检查必须发生在 Provider 调用前，既有 `run_started` 保留，后续不发送 `run_finished`。

- [x] **Step 1: Write a failing no-request test**

构造一个返回超大 user prompt 或超大历史消息的 Runner，调用 `RunEvents`，断言 `errors.Is(err, ErrContextLimit)`、Provider 请求计数为 0、最后一个事件的 `Stage` 为 `agent_context_limit`，且错误事件不包含完整 prompt 或文件正文。

- [x] **Step 2: Run the focused test and confirm RED**

Run: `go test ./internal/agent -run TestRunnerContextLimitStopsBeforeProvider -count=1`

Expected: FAIL because the current loop always calls Provider regardless of accumulated context size.

- [x] **Step 3: Add the pre-request guard**

在每个循环轮次构造 `request` 后、调用 `r.client.Stream` 前检查 `r.ContextBytes()`。如果超过 `MaxConversationBytes`，调用现有错误事件路径，但将 stage 固定为 `agent_context_limit`，返回 `ErrContextLimit`。检查应覆盖首轮 system/tool schema 和后续工具结果；不得把上限检查放到 Provider 返回之后。

- [x] **Step 4: Run Agent regression tests**

Run: `go test ./internal/agent -count=1`

Expected: PASS，现有单轮、多轮、工具预算和空响应测试全部通过。

### Task 3: Chat `/clear` 与可恢复超限

**Files:**
- Modify: `internal/app/chat.go`
- Test: `internal/app/chat_test.go`

**Interfaces:**
- `runChatLoop` 识别精确命令 `/clear`，调用 `runner.ResetContext()`，不调用 Provider、不追加 Agent Event、不创建新 Session 文件，然后继续扫描下一行。
- 对 `errors.Is(err, agent.ErrContextLimit)` 的结果打印简短提示并 `continue`；其他错误仍返回 1，取消仍返回 130。

- [x] **Step 1: Write failing chat tests**

增加两个 httptest 场景：

1. 输入 `first\n/clear\nsecond\nexit\n`，确认第二个 Provider 请求不包含 first 的消息，Provider 请求次数为 2；
2. 让 Runner 达到上下文上限，输入 `too-large\n/clear\nsecond\nexit\n`，确认第一次错误后 chat 不退出，`/clear` 后第二次请求成功。

同时断言 `/clear` 本身不产生请求、不增加 `run_started`，审计文件仍只有一次 chat 运行中的事件流。

- [x] **Step 2: Run focused tests and confirm RED**

Run: `go test ./internal/app -run 'TestChatClear|TestChatContextLimit' -count=1`

Expected: FAIL because `/clear` currently会被当作普通用户问题，context limit 也会让 chat 直接返回 1。

- [x] **Step 3: Implement command and recoverable error path**

在空行和退出命令判断附近加入 `/clear` 分支。上下文超限分支输出：

```text
错误：对话上下文已达到上限，请输入 /clear 后继续
```

随后回到下一轮提示符。不要把完整上下文、错误 prompt 或历史回答写入 stderr 或 Session。

- [x] **Step 4: Run App tests**

Run: `go test ./internal/app -run 'TestChat|TestRun' -count=1`

Expected: PASS，M1.0 多轮、退出、EOF、空响应和单次模式保持兼容。

### Task 4: 文档、规格和验收同步

**Files:**
- Modify: `docs/superpowers/specs/2026-09-30-m11-context-control-design.md`
- Create: `doc/m1.1-context-control.md`
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/Process/代码理解.md`

- [x] **Step 1: Add phase documentation**

记录 `1 MiB` 字节估算、`/clear`、可恢复 `agent_context_limit`、不做自动摘要/Session 恢复，以及人工验收命令。

- [x] **Step 2: Update current scope and navigation**

将 `spec/current.md` 当前版本提升为 M1.1，在 M1.0 前增加范围与验收；README 增加 `/clear` 示例和文档链接；代码理解文档补充 Runner reset、请求前预算检查和错误流。

- [x] **Step 3: Run documentation checks**

Run: `git diff --check`

Expected: PASS，文档中的命令、错误 stage 和代码接口名称一致。

### Task 5: Full verification and commit

**Files:**
- Test: all Go packages.

- [x] **Step 1: Run the complete verification suite**

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

Expected: all commands exit 0.

- [x] **Step 2: Manually verify the recovery flow**

使用有效 Provider：

```powershell
go run ./cmd/drift chat -w .
> 读取 README.md
> /clear
> 这个项目的作用是什么？
> exit
```

再用足够长的连续问题触发上限，确认错误后仍可输入 `/clear`；使用 `session show` 验证 `agent_context_limit`，且不出现提示词、回答正文、文件内容或绝对路径。

- [x] **Step 3: Commit only M1.1 implementation files**

不要暂存用户现有的 `doc/read-agent.md` 删除和 `doc/m0.2-read-agent.md` 未跟踪文件。提交格式：

```powershell
git add <明确列出的 M1.1 文件>
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：实现 M1.1 对话上下文管理" -m "参与人：island"
```
