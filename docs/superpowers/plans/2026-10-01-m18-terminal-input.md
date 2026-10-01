# M1.8 Terminal Input Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give interactive `drift chat` sessions a controlled single-line input area with placeholder text, cursor rendering, and safe cancellation feedback while preserving the existing non-TTY protocol.

**Architecture:** Keep `runChatLoopWithPersistence` as the owner of commands, Agent turns, persistence, and signals. Add a small `chatInput` boundary that selects a Bubble Tea single-line editor only when stdin/stdout are TTYs, and otherwise uses the existing scanner path. The editor returns submitted text or an idle-cancel signal; it does not know about Provider, Agent, sessions, or workspace.

**Tech Stack:** Go 1.26; Bubble Tea and Bubbles textarea for TTY line editing; existing ANSI helpers; standard `bufio.Scanner` fallback and `os.File.Stat` TTY detection.

**Spec:** `docs/superpowers/specs/2026-10-01-m18-terminal-input-design.md`

## Global Constraints

- Only real TTY sessions use the editor; redirected input/output remains ANSI-free and scanner-compatible.
- The editor is single-line only: no Alt Screen, mouse, history, multiline input, queue, or viewport.
- `Ctrl+C` during an active turn keeps the chat alive; `Ctrl+C` while idle exits with code 130.
- Existing `/status`, `/clear`, `/compact`, `--resume`, `--no-session`, audit redaction, and M1.7 rollback behavior remain unchanged.
- Do not expose raw `context canceled` to the user; render the safe cancellation message.

---

### Task 1: Add the terminal input dependency and boundary

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/app/chat_input.go`
- Test: `internal/app/chat_input_test.go`

**Interfaces:**
- Produces `type chatInput interface { Read(context.Context) (string, error) }`.
- Produces `newChatInput(in io.Reader, out io.Writer) chatInput`, selecting TTY editor or scanner fallback.
- Produces sentinel `errChatInputCancelled` for idle Ctrl+C.

- [ ] **Step 1: Add the smallest dependency set**

Run:

```powershell
go get github.com/charmbracelet/bubbles@latest github.com/charmbracelet/bubbletea@latest
```

Keep only the direct modules needed by `textarea` and the Tea program; do not add a styling framework or a full application shell.

- [ ] **Step 2: Write fallback tests first**

Add tests proving `newChatInput(strings.NewReader("hello\n"), &out)` returns `hello`, trims the line ending, and that an empty line is returned as an empty string. Use a non-file writer so the test cannot accidentally select the TTY path.

- [ ] **Step 3: Implement the scanner fallback**

Move the current scanner setup into a small `scannerChatInput` with a 1 MiB buffer. Preserve scanner errors and EOF exactly so existing chat tests keep their exit behavior.

- [ ] **Step 4: Implement TTY detection without changing command semantics**

Select the editor only when both input and output are `*os.File` character devices. All injected readers, redirected output, and test buffers must use the scanner fallback.

- [ ] **Step 5: Run the focused tests**

Run:

```powershell
go test ./internal/app -run 'TestChatInput' -count=1 -v
```

Expected: PASS; no ANSI bytes are produced by fallback tests.

- [ ] **Step 6: Commit the boundary**

```powershell
git add go.mod go.sum internal/app/chat_input.go internal/app/chat_input_test.go
git commit -m "功能：增加终端输入层边界"
```

### Task 2: Implement the one-line TTY editor

**Files:**
- Modify: `internal/app/chat_input.go`
- Test: `internal/app/chat_input_test.go`

**Interfaces:**
- `ttyChatInput.Read(ctx)` returns one submitted line, `errChatInputCancelled` for idle Ctrl+C, and context errors for shutdown.
- The editor renders `❯ Send a message...` between two dim separator lines when empty.

- [ ] **Step 1: Write model-level tests**

Test the pure update logic with Tea key messages: printable text replaces the placeholder, `enter` returns the text, and `ctrl+c` returns `errChatInputCancelled`. Assert that the rendered view contains `Send a message...` only while the value is empty.

- [ ] **Step 2: Configure a single-line Bubble Tea model**

Use `textarea.New()` with `Placeholder = "Send a message..."`, `Prompt = "❯ "`, `SetHeight(1)`, line numbers disabled, and no alternate screen. Apply existing Drift ANSI colors through the component styles: cyan prompt, dim placeholder/separators, default input text.

- [ ] **Step 3: Add separator rendering and cleanup**

Print one dim separator before starting the Tea program and one after it exits. Restore the terminal state on every return path. Do not write separator/control bytes in the scanner fallback.

- [ ] **Step 4: Make context cancellation safe**

Run the Tea program with a context-aware command. If the chat context is canceled, stop the program and return the context error; if the user presses Ctrl+C while idle, return `errChatInputCancelled` without converting it into an Agent turn.

- [ ] **Step 5: Run focused editor tests**

Run:

```powershell
go test ./internal/app -run 'TestTTYChatInput|TestChatInput' -count=1 -v
```

Expected: PASS; model-level tests do not require a real terminal.

- [ ] **Step 6: Commit the editor**

```powershell
git add internal/app/chat_input.go internal/app/chat_input_test.go
git commit -m "功能：实现终端单行输入框"
```

### Task 3: Integrate input selection into the chat loop

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`

**Interfaces:**
- `runChatLoopWithPersistence` obtains one `chatInput` before the loop and calls `Read(ctx)` for each prompt.
- The existing command dispatch and `interruptCoordinator` remain in `chat.go`.

- [ ] **Step 1: Add regression tests for the scanner path**

Keep the existing `strings.NewReader("/status\nexit\n")` tests and add assertions that they still produce no ANSI control bytes and still avoid Provider calls.

- [ ] **Step 2: Replace per-iteration scanner setup**

Construct `input := newChatInput(in, out)` once. Replace `scanner.Scan()` and `scanner.Text()` with `prompt, err := input.Read(ctx)`, mapping `io.EOF` to normal exit, `errChatInputCancelled` to exit code 130, and other errors to the existing stderr path.

- [ ] **Step 3: Preserve the existing prompt/command flow**

Do not move `/status`, `/clear`, `/compact`, ordinary text commands, persistence, or Agent calls into the input component. Empty strings continue to be ignored; trimming remains in the chat loop.

- [ ] **Step 4: Run the complete app tests**

Run:

```powershell
go test ./internal/app -count=1 -v
```

Expected: PASS, including persistence, status, compact, resume, and cancellation tests.

- [ ] **Step 5: Commit integration**

```powershell
git add internal/app/chat.go internal/app/chat_test.go
git commit -m "功能：接入交互式终端输入"
```

### Task 4: Normalize cancellation feedback and visual documentation

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`
- Modify: `doc/Process/面板视觉设计.md`
- Modify: `spec/current.md`
- Create: `doc/m1.8-terminal-input.md`
- Test: `artifacts/verification/m1.8/`

**Interfaces:**
- Active-turn cancellation prints `✖ 当前轮已取消；会话仍可继续` with the existing terminal error color and plain text fallback.
- Idle cancellation remains exit code 130 and does not create an Agent error event.

- [ ] **Step 1: Add cancellation-output assertions**

Extend the existing cancellation test to assert the safe message is present and `context canceled` is absent from user-facing output. Assert the loop accepts a following prompt after cancellation.

- [ ] **Step 2: Render the safe message**

Add a small `chatCancelMessage(out)` helper using red text and a purple `✖` only when color is enabled. Keep the current audit event stage `agent_cancelled` unchanged.

- [ ] **Step 3: Update stage documentation**

Add M1.8 to `spec/current.md` with scope and acceptance criteria. Add `doc/m1.8-terminal-input.md` with manual Windows Terminal checks. Mark the input placeholder/cursor and cancellation rules in `doc/Process/面板视觉设计.md` as implemented rather than planned.

- [ ] **Step 4: Run verification and capture evidence**

Run:

```powershell
go test ./... -count=1
go vet ./...
go build -o .codex-temp\drift-m18.exe ./cmd/drift
git diff --check
```

Save the command output under `artifacts/verification/m1.8/full-check.txt`. Manually run `\.codex-temp\drift-m18.exe chat -w .` and verify placeholder, cursor, Enter, `/status`, ordinary response, active Ctrl+C, idle Ctrl+C, and redirected input.

- [ ] **Step 5: Commit the completed stage**

```powershell
git add internal/app/chat.go internal/app/chat_test.go doc/Process/面板视觉设计.md spec/current.md doc/m1.8-terminal-input.md artifacts/verification/m1.8/full-check.txt
git commit -m "功能：完成 M1.8 终端输入与取消提示"
```

## Self-review checklist

- All design requirements map to Tasks 1–4: placeholder/cursor (Tasks 1–2), TTY fallback (Tasks 1 and 3), active/idle cancellation (Tasks 3–4), status compatibility and documentation (Task 4).
- No task adds history, multiline editing, Alt Screen, Memory, or a new Provider.
- Every introduced interface is named before use: `chatInput`, `newChatInput`, `errChatInputCancelled`, and `ttyChatInput.Read`.
- Non-TTY behavior remains testable using `strings.Reader` and `bytes.Buffer`.
