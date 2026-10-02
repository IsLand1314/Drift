# TuiMainScreen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `TuiMainScreen` the default Pi-style linear main-buffer renderer and preserve the fixed-layout renderer as non-default `TuiFullScreen`.

**Architecture:** The existing sequential chat loop becomes the real-TTY main-screen route. Completed content is ordinary append-only terminal output. The current Bubble Tea viewport model is renamed `TuiFullScreen`, owns the alternate screen, and keeps mouse reporting disabled.

**Tech Stack:** Go, Bubble Tea v1.3.10, Bubbles textarea/viewport, Windows Terminal ANSI.

**Spec:** `doc/Process/面板视觉设计.md` §8.9

## Global Constraints

- `TuiMainScreen` is default and must have no alternate screen, mouse reporting, application viewport, fixed footer, or terminal scroll region.
- `TuiFullScreen` is alternate-screen-only and non-default; mouse reporting remains off.
- Preserve sessions, Agent/tool events, approvals, permission modes, Ctrl+C, change sets, and non-TTY behavior.
- Do not stage user-owned M3.6/M3.7 documentation changes.

---

### Task 1: Route TTY to append-only TuiMainScreen

**Files:** `internal/app/chat.go`, `internal/app/chat_input.go`, `internal/app/chat_layout.go`, `internal/app/chat_test.go`, `internal/app/chat_input_test.go`.

**Interfaces:** Produce `newTuiMainScreenInput(in, out, modelName) chatInput` and sequential real-TTY output.

- [ ] Write `TestTuiMainScreenAppendsCompletedTurnToTerminalOutput`. Run one deterministic turn through `runChatLoopWithPersistence`; assert prompt < tool result < assistant result < Done and assert no `\x1b[?1049h` or terminal scroll-region sequence.
- [ ] Run `go test ./internal/app -run TestTuiMainScreenAppendsCompletedTurnToTerminalOutput -count=1`. Expected RED: TTY currently dispatches to the viewport renderer and reserves a scroll region.
- [ ] Remove the early `runTTYChatLoop` dispatch, let real TTY use the existing sequential loop, rename `ttyChatInput` to `tuiMainScreenInput`, and remove `fixedChatFooter.Begin/End` use. Delete `chat_layout.go` only when unused. Keep the one-shot Bubble Tea editor only for the next input; completed messages are append-only writes.
- [ ] Run `go test ./internal/app -run 'TestTuiMainScreenAppendsCompletedTurnToTerminalOutput|TestChatInput' -count=1`. Expected GREEN: full turn is in terminal output with no fixed scroll region.
- [ ] Commit only Task 1 files with `refactor: make main screen append-only`.

### Task 2: Preserve the old renderer as TuiFullScreen

**Files:** `internal/app/chat_tui.go`, `internal/app/chat_tui_test.go`.

**Interfaces:** Produce `runTuiFullScreen`, `tuiFullScreen`, and `newTuiFullScreen`.

- [ ] Write `TestTuiFullScreenOwnsViewportAfterInitialResize`: first resize creates the viewport, second resize preserves a manual offset.
- [ ] Run `go test ./internal/app -run TestTuiFullScreenOwnsViewportAfterInitialResize -count=1`. Expected RED: `TuiFullScreen` identifiers do not exist.
- [ ] Rename `runTTYChatLoop` to `runTuiFullScreen`, `ttyChatModel` to `tuiFullScreen`, and `newTTYChatModel` to `newTuiFullScreen`. `runTuiFullScreen` uses `tea.WithAltScreen()` but not `tea.WithMouseCellMotion()`.
- [ ] Run `go test ./internal/app -run 'TestTuiFullScreen|TestTTYChat' -count=1`. Expected GREEN: resized viewport and last-column tests remain green under renamed names.
- [ ] Commit only Task 2 files with `refactor: name alternate screen renderer`.

### Task 3: Document and verify

**Files:** `doc/Process/面板视觉设计.md`, `artifacts/verification/tui-main-screen/tdd-main-screen.txt`, `artifacts/verification/tui-main-screen/regression.txt`, `artifacts/verification/tui-main-screen/manifest.json`.

- [ ] Mark §8.9 delivered: `TuiMainScreen` is default; `TuiFullScreen` has no public mode selector yet.
- [ ] Record exact RED/GREEN output from Tasks 1 and 2 in `tdd-main-screen.txt`.
- [ ] Run `go test ./... -count=1`, `go vet ./...`, `go build -o .codex-temp\drift-tui-main.exe ./cmd/drift`, and `git diff --check`. Remove the generated executable; capture outputs in `regression.txt` and link SHA-256 values in `manifest.json`.
- [ ] Manual Windows Terminal check: submit a long-answer prompt, use mouse wheel/right scrollbar to find the first prompt, drag-select/copy, right-click paste, and resize narrow → wide → narrow. Expect durable terminal history with no duplicate frame, hidden viewport, or fixed footer.
- [ ] Commit documentation/evidence with `docs: verify main screen renderer` and push `master`.
