# M3.5 命令取消与进程树终止 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让 `chat` 中取消或超时的 `run_command` 结束整个命令进程树，并让当前 chat 轮次安全结束后继续可用。

**Architecture:** 保留 M3.4 的 `ExecuteCommand`、审批和结果格式，在 `internal/tool` 增加平台专用的进程树控制。使用 Go `exec.Cmd.Cancel` 和 `WaitDelay` 接入取消清理；Windows 使用 Job Object，Unix 使用独立 process group。Agent 和 TUI 只消费统一的 `cancelled/timeout/failed` 状态，不感知平台细节。

**Tech Stack:** Go 1.26 标准库 `os/exec`、Windows Job Object API、Unix process group/signals、现有 Bubble Tea TUI 和 Go testing。

**Spec:** `docs/superpowers/specs/2026-10-02-m35-command-cancellation-design.md`

## Global Constraints

- `run_command` 仍只在 `chat` 注册；`-p`、`audit` 和 session 管理保持只读。
- 不改变 M3.4 的 `ask / allow / deny` 和精确命令匹配语义。
- 取消、超时、非零退出和拒绝均不得报告为成功。
- 审计、session 和 changes 不保存完整命令输出或回答正文。
- 不引入第三方进程树库，不增加后台任务、并发命令或永久权限配置。

---

### Task 1: 为命令进程增加平台生命周期控制

**Files:**
- Create: `internal/tool/command_process.go`
- Create: `internal/tool/command_process_windows.go`
- Create: `internal/tool/command_process_unix.go`
- Modify: `internal/tool/command.go:137-176`
- Test: `internal/tool/command_process_test.go`
- Test: `internal/tool/command_process_windows_test.go`
- Test: `internal/tool/command_process_unix_test.go`

**Interfaces:**
- `command.go` consumes `newCommandProcess(ctx, command) *commandProcessHandle`.
- `commandProcessHandle` contains `cmd *exec.Cmd`, `afterStart func() error`, `cancel func() error`, and `close func() error`; `ExecuteCommand` owns calling them in order.
- Platform files provide `newPlatformCommandProcess(cmd *exec.Cmd) *commandProcessHandle` and do not expose OS handles outside `internal/tool`.
- Tests consume the existing `ExecuteCommand` API and do not depend on platform-private handles.

- [ ] **Step 1: Write failing process-tree tests**

Add `TestExecuteCommandCancellationReturnsCancelled` using a command that blocks until Context cancellation; assert the result contains `status=cancelled` and `exit_code=-1`.

Add a Windows-only test in `command_process_windows_test.go` that starts a child process from `cmd.exe`, cancels the Context, waits up to one second, and asserts the child PID is no longer running. Add a Unix-only test in `command_process_unix_test.go` that starts a shell child group, cancels it, and asserts the group is gone. Each test must defer cleanup and skip when the required native utility is unavailable.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/tool -run 'Command.*Cancel|Command.*ProcessTree' -count=1`

Expected: FAIL because `ExecuteCommand` still uses the default `exec.CommandContext` kill behavior.

- [ ] **Step 3: Add the common process hook**

Create `newCommandProcess(ctx, command) *commandProcessHandle` in `command_process.go`. It must select `cmd.exe /d /s /c` on Windows and `/bin/sh -c` elsewhere, call `newPlatformCommandProcess`, set `cmd.Cancel` to the handle's `cancel`, and set `cmd.WaitDelay = 2 * time.Second`.

- [ ] **Step 4: Implement Windows Job Object cleanup**

In `command_process_windows.go`, set the process creation flags required for a dedicated process group, create a Job Object with kill-on-close behavior in `newPlatformCommandProcess`, assign the started process in `afterStart`, and close/terminate the Job Object in `cancel`/`close`. The callbacks must be idempotent and return native errors without replacing the Context cancellation cause.

- [ ] **Step 5: Implement Unix process-group cleanup**

In `command_process_unix.go`, set `SysProcAttr.Setpgid = true` before starting the command. The `cancel` callback sends `SIGTERM` to `-cmd.Process.Pid`, waits through `WaitDelay`, and escalates to `SIGKILL` if the group remains alive. The `close` callback must be idempotent.

- [ ] **Step 6: Run focused tests and commit**

Run: `go test ./internal/tool -run 'Command.*Cancel|Command.*ProcessTree' -count=1`

Expected: PASS on the current platform; platform-specific tests are skipped only when their platform tags do not match.

Commit: `git add internal/tool/command.go internal/tool/command_process*.go && git commit -m "实现命令进程树取消"`

### Task 2: Preserve command result status and bounded pipe cleanup

**Files:**
- Modify: `internal/tool/command.go:136-170`
- Test: `internal/tool/command_test.go`

**Interfaces:**
- `ExecuteCommand` consumes the process hook from Task 1 and continues returning the existing result string.
- `commandStatus` in `internal/agent/agent.go` continues parsing the first result line without platform-specific fields.

- [ ] **Step 1: Add status regression tests**

Add tests for Context cancellation, timeout, and non-zero exit. Assert respectively `status=cancelled`, `status=timeout`, and `status=failed`; assert none contains `status=success`.

- [ ] **Step 2: Wire Start/Wait cleanup**

Change `ExecuteCommand` to start the command, attach the platform cleanup handle, call `Wait`, and close the handle on every return path. Keep stdout/stderr limits and byte counts unchanged. Use `cmd.WaitDelay` so leaked pipes cannot hold the chat indefinitely.

- [ ] **Step 3: Run tests and commit**

Run: `go test ./internal/tool -count=1`

Expected: PASS with existing workspace, cwd, timeout, truncation, cancellation, and non-zero-exit coverage.

Commit: `git add internal/tool/command.go internal/tool/command_test.go && git commit -m "保持命令取消状态与管道清理"`

### Task 3: Normalize Agent and TUI cancellation feedback

**Files:**
- Modify: `internal/agent/agent.go:350-390`
- Modify: `internal/app/chat.go:430-460`
- Modify: `internal/app/chat_tui.go:315-335`
- Test: `internal/agent/agent_test.go`
- Test: `internal/app/chat_test.go`
- Test: `internal/app/chat_tui_test.go`

**Interfaces:**
- Agent continues returning `context.Canceled` for a cancelled turn and emits `agent_cancelled` through the existing event path.
- Non-TTY chat keeps the existing `当前轮已取消；会话仍可继续` message.
- TUI maps a cancelled turn to the same safe message instead of rendering raw `context canceled`; idle Ctrl+C remains exit code 130.

- [ ] **Step 1: Add failing TUI cancellation assertion**

Extend the TUI turn-finish test to pass `context.Canceled` and assert the transcript contains `当前轮已取消；会话仍可继续` and does not contain the raw string `context canceled`.

- [ ] **Step 2: Normalize `finishTurn`**

In `ttyChatModel.finishTurn`, detect `errors.Is(err, context.Canceled)` and append the existing styled cancellation message. Keep other errors unchanged. Do not save a failed partial turn as a successful result.

- [ ] **Step 3: Verify current-turn rollback and continuation**

Run: `go test ./internal/agent -run 'Cancel|Context' -count=1` and `go test ./internal/app -run 'Interrupt|Cancel|Chat' -count=1`.

Expected: active-turn Ctrl+C cancels only that turn, the next input executes, and idle Ctrl+C still exits.

- [ ] **Step 4: Commit**

Commit: `git add internal/agent/agent.go internal/app/chat.go internal/app/chat_tui.go internal/agent/agent_test.go internal/app/chat_test.go internal/app/chat_tui_test.go && git commit -m "统一命令取消的聊天反馈"`

### Task 4: Add M3.5 evidence and update current specification

**Files:**
- Modify: `spec/current.md`
- Create: `doc/m3.5-command-cancellation.md`
- Create: `artifacts/verification/m3.5/command-cancel-tests.txt`
- Create: `artifacts/verification/m3.5/manual-acceptance.md`
- Create: `artifacts/verification/m3.5/audit-check.txt`
- Create: `artifacts/verification/m3.5/final-check.txt`
- Create: `artifacts/verification/m3.5/manifest.json`

**Interfaces:**
- Documentation records M3.5 as the current delivery scope and links to the design spec.
- Evidence files reference exact commands and exit codes; no API key, response body, or full command output is stored.

- [ ] **Step 1: Replace the current-scope table**

Update `spec/current.md` from M3.4 to M3.5 and add AC-M35-001 through AC-M35-004 exactly as defined in the design spec. Keep M3.4 history in `docs/spec-history.md`.

- [ ] **Step 2: Write the user-facing stage document**

Document Windows/Unix cancellation behavior, Ctrl+C distinction between active and idle chat, result statuses, limits, and manual commands for a child-process cancellation check.

- [ ] **Step 3: Capture focused evidence**

Run `go test ./internal/tool -run 'Command.*Cancel|Command.*ProcessTree' -count=1`, the Agent/App cancellation tests, and the manual chat scenario. Store only compact pass/fail summaries and safe metadata.

- [ ] **Step 4: Commit documentation and evidence**

Commit: `git add spec/current.md doc/m3.5-command-cancellation.md artifacts/verification/m3.5 && git commit -m "补充 M3.5 取消验收证据"`

### Task 5: Full verification and delivery

**Files:**
- No source changes expected; only verification outputs in `artifacts/verification/m3.5/`.

- [ ] **Step 1: Run the full regression**

Run: `go test ./... -count=1`, `go vet ./...`, `go build ./cmd/drift`, and `git diff --check`.

Expected: all commands exit with code 0.

- [ ] **Step 2: Run the current-spec audit**

Run the project audit against the explicit M3.5 specification and changed source files. Historical `.drift` sessions and old worktrees must not be treated as current source files.

- [ ] **Step 3: Verify repository state**

Run `git status --short --branch` and `git ls-remote --heads origin master`; confirm the working tree is clean and the delivery commit is pushed.

- [ ] **Step 4: Record final evidence and commit**

Update `artifacts/verification/m3.5/final-check.txt` with the four exit codes, then commit and push the final M3.5 delivery.

## Self-review

- Every M3.5 requirement maps to Tasks 1–5: process-tree cleanup, status preservation, TUI feedback, evidence, and regression.
- No task changes approval semantics, session schema, or unrelated TUI layout.
- Platform-specific behavior is isolated behind `internal/tool` helpers; Agent and App consume existing Context/error boundaries.
- No placeholders or unbounded “later” steps are present.
