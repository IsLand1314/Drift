# M1.0 Interactive Chat Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有只读 Agent Loop 之上增加 `drift chat`，让一个进程内的多次用户输入共享内存上下文。

**Architecture:** Agent 新增可复用的内存 Runner，维护当前进程的 user/assistant/tool 消息；每轮仍使用现有模型请求、工具调用和读取预算。CLI 只负责读取 stdin、输出流式文本和生命周期，Session 继续只写安全审计摘要，不作为恢复上下文。

**Tech Stack:** Go 标准库 `bufio`、`context`、`io`、`os`、现有 `internal/agent`、`internal/app`、`internal/session`。

**Spec:** M1.0 设计已在本次对话确认；当前边界记录在 `spec/current.md` 的 M1.0 章节，审计规则沿用 `doc/m0.9-session-audit.md`。

## Global Constraints

- 继续使用 Go 1.26+ 和标准库，不新增依赖。
- 只支持现有 `list_files`、`search_text`、`read_file` 三个只读工具。
- 每一轮沿用 `MaxModelRequests`、`MaxToolCalls`、`MaxTotalReadBytes` 限制。
- 上下文只存在进程内；退出后不恢复、不读取历史 Session JSONL。
- Session JSONL 继续只保存脱敏审计元数据、相对路径和 `finish_reason`。
- 现有 `drift -p` 单轮行为和退出码保持不变。

---

### Task 1: 抽出可复用的内存 Agent Runner

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/event.go`
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Produces `agent.Runner`，构造函数 `NewRunner(client llm.Client, root, focus string, registry tool.Registry) *Runner`。
- Produces `(*Runner).RunEvents(ctx context.Context, prompt string, sink EventSink) error`。
- Existing `Run`, `RunEvents` 和 `RunEventsWithRegistry` continue to work by creating a fresh Runner.

- [x] **Step 1: Write failing multi-turn test**

Use the existing `scriptedClient` with two calls to `runner.RunEvents`; assert the second request contains the first turn's user prompt and assistant answer, while the first request still contains the system instruction and only its own prompt.

- [x] **Step 2: Run focused test and confirm RED**

```powershell
go test ./internal/agent -run TestRunnerPreservesConversationAcrossTurns -count=1
```

Expected: FAIL because `Runner` does not exist.

- [x] **Step 3: Implement the smallest Runner refactor**

Move the current per-run `messages`, request loop, tool budget counters, and finish/error events into `Runner.RunEvents`. Append each turn's user prompt and final assistant message to `Runner.messages`; keep the system instruction out of persisted conversation state and prepend it on every Provider request.

- [x] **Step 4: Run focused and existing Agent tests**

```powershell
go test ./internal/agent -count=1
```

Expected: PASS; single-turn wrappers remain behavior-compatible.

### Task 2: Add injectable CLI input and `chat` loop

**Files:**
- Modify: `internal/app/app.go`
- Create: `internal/app/chat.go`
- Test: `internal/app/chat_test.go`
- Modify: `cmd/drift/main.go` only if the input wrapper requires it (default stdin remains `os.Stdin`).

**Interfaces:**
- Produces `RunWithInput(ctx, args, getenv, in, out, stderr) int` for deterministic chat tests.
- Existing `Run` delegates to `RunWithInput(..., os.Stdin, ...)`.
- `drift chat [-w path] [--trace] [-model model] [-base-url url]` reads one line per turn.

- [x] **Step 1: Write failing chat tests**

Cover: two prompts share the same Runner context; `exit`, `/exit`, and `quit` stop with code 0; EOF stops with code 0; blank lines do not call Provider; missing configuration returns code 2 before reading input. Use an httptest SSE server and `bytes.Buffer` input.

- [x] **Step 2: Run focused tests and confirm RED**

```powershell
go test ./internal/app -run TestChat -count=1
```

Expected: FAIL because `RunWithInput` and `chat` routing do not exist.

- [x] **Step 3: Implement CLI loop**

Parse shared workspace/provider flags once, create one Runner and one Session Writer for the process, then scan stdin. Print `> ` before each input, stream each turn's final text to stdout, append a newline after the turn, and terminate on `exit`, `/exit`, `quit`, EOF, or Ctrl+C. A failed turn prints the existing safe error and ends the process; no resume path is added.

- [x] **Step 4: Run focused App tests**

```powershell
go test ./internal/app -run "TestChat|TestRunDirectAnswer" -count=1
```

Expected: PASS; `drift -p` remains unchanged.

### Task 3: Document M1.0 boundaries and manual acceptance

**Files:**
- Create: `doc/m1.0-interactive-chat.md`
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/Process/代码理解.md`

- [x] **Step 1: Document the lifecycle**

Describe `chat` startup, in-memory message retention, per-turn Agent Loop, audit-only Session writing, exit commands, and the fact that process exit destroys context.

- [x] **Step 2: Add manual acceptance commands**

```powershell
drift chat -w .
> 读取 README.md
> 这个项目的作用是什么？
> exit
```

Acceptance: the second request includes the first turn's context; `.drift/sessions` contains audit events but no prompt, answer, tool result, or raw arguments.

### Task 4: Full verification

**Files:**
- Test: all Go packages.

- [x] **Step 1: Run verification**

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

- [ ] **Step 2: Run manual chat acceptance**

Use a configured compatible Provider, run the two-turn example, inspect `session show`, and verify the process does not read old JSONL when starting a new chat.
