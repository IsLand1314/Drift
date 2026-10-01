# M3.2 审批选择器与运行反馈 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 M3.1 的写入确认改为方向键选择的 TTY 审批面板，并统一读写工具的紧凑进度、耗时和安全失败提示。

**Architecture:** 保留 `agent.PermissionPrompt` 和现有 `chatInput` 边界，在 `internal/app` 增加独立的审批选择输入模型；授权结果仍由 Runner 决定，TTY 只负责渲染和收集选择。工具事件增加安全错误摘要与开始时间，TTY 消费者将 tool call/result 配对渲染为短状态；非 TTY 和 `--trace` 继续使用稳定文本/完整事件。

**Tech Stack:** Go 1.26+、标准库、Bubble Tea/Bubbles/Lipgloss、现有 Agent Event 与 session audit。

**Spec:** `docs/superpowers/specs/2026-10-01-m32-approval-tui-design.md`

## Global Constraints

- `drift -p` 继续只注册 `list_files`、`search_text`、`read_file`。
- `chat` 的写入仍必须经过 Preview → PermissionPrompt → CommitWrite。
- 目标路径仍限制在 workspace 内，父目录不自动创建。
- “允许当前模式”只保存在当前 chat 进程内，不写入会话、审计或 `.drift/changes`。
- 非 TTY 输出不得包含 ANSI 控制字符。
- 不增加删除、命令执行、测试运行、OS 沙箱、Git 回滚或完整全屏 TUI。

---

### Task 1: 建立审批选择器模型

**Files:**
- Create: `internal/app/approval_input.go`
- Create: `internal/app/approval_input_test.go`

**Interfaces:**
- Consumes: `agent.PermissionRequest` and the existing `chatInput`/TTY detection helpers.
- Produces: `approvalChoice` values `approveOnce`, `approvePattern`, `deny`; `readApprovalChoice(context.Context, io.Reader, io.Writer, agent.PermissionRequest) (approvalChoice, error)`.

- [ ] **Step 1: Write failing tests** for initial selection, arrow movement, numeric shortcuts, Enter approval, Esc/Ctrl+C denial, and non-TTY default denial.
- [ ] **Step 2: Run `go test ./internal/app -run Approval -count=1` and verify the new tests fail because the model/functions do not exist.
- [ ] **Step 3: Implement the smallest Bubble Tea model with three fixed options and a single selected index. Render the command title, relative path, muted approval copy, and selected option with the existing cyan prompt style.
- [ ] **Step 4: Run the focused tests and verify all choices and cancellation pass.
- [ ] **Step 5: Commit `界面：增加写入审批选择器`.

### Task 2: Add session-scoped approval memory

**Files:**
- Modify: `internal/app/chat.go`
- Create: `internal/app/permission_memory.go`
- Create: `internal/app/permission_memory_test.go`

**Interfaces:**
- Consumes: `approvalChoice` from Task 1 and `agent.PermissionRequest`.
- Produces: `permissionMemory.Allow(request) bool` and `permissionMemory.Remember(request)`, matching tool name + operation + normalized relative path pattern only in process memory.

- [ ] **Step 1: Write failing tests** proving once approvals are not remembered, pattern approvals skip only the same operation/path pattern, unrelated paths still prompt, and a new memory starts empty.
- [ ] **Step 2: Run `go test ./internal/app -run PermissionMemory -count=1` and verify failure.
- [ ] **Step 3: Implement exact-path matching first; do not add glob syntax or persistence. Keep the key derived from sanitized relative path and operation.
- [ ] **Step 4: Run focused tests and verify the memory cannot match absolute paths or protected paths.
- [ ] **Step 5: Commit `权限：增加当前会话审批记忆`.

### Task 3: Wire the selector into write confirmation

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`
- Modify: `internal/agent/agent.go`

**Interfaces:**
- Consumes: Task 1 choices and Task 2 memory.
- Produces: `confirmWrite` returns `Allow` plus a reason; repeated remembered operations bypass only the prompt while still running Preview and CommitWrite.

- [ ] **Step 1: Add failing app tests** for approve-once, approve-pattern followed by a second write without another prompt, deny, and Ctrl+C cancellation.
- [ ] **Step 2: Run `go test ./internal/app -run 'Write|Approval' -count=1` and verify failures.
- [ ] **Step 3: Replace the `y/N` reader in `confirmWrite` with the selector. Keep scanner behavior for non-TTY tests and redirection: only `y`/`yes` remains accepted there for compatibility.
- [ ] **Step 4: Record explicit decision reasons (`user_approved`, `session_pattern_approved`, `user_denied`, `approval_cancelled`) in the existing permission decision event.
- [ ] **Step 5: Run focused tests and verify the existing M3.1 write tests remain green.
- [ ] **Step 6: Commit `交互：接入写入审批选择器`.

### Task 4: Add compact tool progress and safe failure rendering

**Files:**
- Modify: `internal/agent/event.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`
- Modify: `internal/app/trace.go`

**Interfaces:**
- Consumes: existing `EventToolCall` and `EventToolResult`.
- Produces: safe `ErrorSummary` on tool-result events and elapsed duration for TTY-only rendering; trace remains event-level and does not expose file content.

- [ ] **Step 1: Write failing tests** asserting TTY output contains a compact start/completion pair, write output omits diff headers, failed preflight shows a safe reason, and non-TTY output remains ANSI-free.
- [ ] **Step 2: Run `go test ./internal/app ./internal/agent -run 'Tool|Write|Trace' -count=1` and verify failure.
- [ ] **Step 3: Add a sanitized `ErrorSummary` field to events; map known write errors to stable text such as `parent directory does not exist`, without returning absolute workspace paths or content.
- [ ] **Step 4: In the chat event sink, pair tool calls with results and render `● Read ...`, `● Write ...`, then `✓ ... · <seconds>s` or `✖ ...` using existing color helpers. Keep `--trace` unchanged except for the new safe summary fields.
- [ ] **Step 5: Remove raw write diff headers from the TTY approval view; retain full diff only in `.drift/changes`.
- [ ] **Step 6: Run focused tests and verify redirected output contains no ANSI escapes.
- [ ] **Step 7: Commit `界面：增加读写进度与安全失败提示`.

### Task 5: Update docs and acceptance evidence

**Files:**
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/architecture.md`
- Modify: `doc/Process/代码理解.md`
- Modify: `doc/Process/面板视觉设计.md`
- Create: `doc/m3.2-approval-tui.md`
- Create: `artifacts/verification/m3.2/full-check.txt`
- Create: `artifacts/verification/m3.2/manual-acceptance.txt`

**Interfaces:**
- Consumes: the final behavior from Tasks 1–4.
- Produces: synchronized M3.2 scope, limits, manual acceptance steps, and command evidence.

- [ ] **Step 1: Document exact commands:** `go run ./cmd/drift chat -w .`, approval navigation with arrows/Enter, denial with Esc, non-TTY `y` fallback, and `--trace` diagnostics.
- [ ] **Step 2: Add M3.2 acceptance rows for once/pattern/deny, compact progress, safe failure, and no ANSI in redirected output.
- [ ] **Step 3: Run `git diff --check` and inspect docs for stale claims that `write_file` always asks for `y/N`.
- [ ] **Step 4: Commit `文档：补充 M3.2 审批交互与验收`.

### Task 6: Full verification and manual CLI run

**Files:**
- Modify: `artifacts/verification/m3.2/full-check.txt`
- Modify: `artifacts/verification/m3.2/manual-acceptance.txt`

- [ ] **Step 1: Run `go test ./... -count=1`.
- [ ] **Step 2: Run `go vet ./...`.
- [ ] **Step 3: Run `go build -o .codex-temp\drift-m32.exe ./cmd/drift`.
- [ ] **Step 4: Run `git diff --check`.
- [ ] **Step 5: Run `go run ./cmd/drift chat -w .` against a local OpenAI-compatible mock and record: approve once, approve pattern, deny, Ctrl+C/Esc, compact progress, safe failure, and `.drift/changes` evidence; do not record API keys or file contents.
- [ ] **Step 6: Commit `验收：完成 M3.2 自动化与 CLI 验证`.
