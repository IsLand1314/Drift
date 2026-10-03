# M4.1 Single Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Run one child Agent in a task Worktree with reliable completion, failure, cancellation, timeout, cleanup, and audit results.

**Architecture:** Add a small child-agent manager above `agent.Runner`. The manager owns one lifecycle at a time, receives a task ID and existing Worktree, creates a child Runner with a fresh registry, and returns a typed result. The parent chat remains the owner of user interaction and permissions; the child cannot approve its own mutations.

**Tech Stack:** Go standard library, existing `internal/agent`, `internal/tool`, `internal/session`, and task/worktree state.

**Spec:** `spec/m4.1-single-agent.md`

## Global Constraints

- One child Agent at a time in M4.1.
- No automatic approval by the child Agent.
- Never force-delete or reset a Worktree.
- Parent chat must remain usable after child failure, cancellation, or timeout.
- LLM acceptance is read-only and independent from implementation context.

### Task 1: Lifecycle model

**Files:** `internal/agent/subagent.go`, `internal/agent/subagent_test.go`

- [ ] Add failing tests for pending/running/completed, failed, cancelled, and timeout results.
- [ ] Add typed lifecycle state and result structures with timestamps, task ID, worktree, error category, and output summary.
- [x] Add a manager that guarantees one active child and rejects a second start.
- [ ] Run targeted tests.

### Task 2: Child Runner execution

**Files:** `internal/agent/subagent.go`, `internal/agent/subagent_test.go`

- [x] Add a child Runner factory using a fresh chat registry and the task Worktree as root.
- [ ] Execute with a child context and propagate cancellation/timeout distinctly.
- [ ] Verify parent context remains usable after each terminal state.
- [ ] Run success, tool failure, cancellation, timeout, and missing Worktree tests.

### Task 3: Audit and task state integration

**Files:** `internal/agent/subagent.go`, `internal/app/chat.go`, `internal/tool/task.go`, tests

- [x] Emit start, terminal result, and cleanup audit events with task ID and outcome.
- [x] Update task status to `in_progress`, `completed`, `failed`, `cancelled`, or `timeout`.
- [x] Ensure cleanup only releases child resources and never deletes the Worktree.
- [x] Test persistence of terminal state.

### Task 4: Control surface and documentation

**Files:** `internal/tool/task.go`, `internal/tool/registry.go`, `spec/current.md`, `spec/m4.1-single-agent.md`

- [x] Expose the smallest control surface needed by the parent: start/status/cancel.
- [ ] Keep the child control surface from recursively starting another child.
- [ ] Document explicit confirmation and failure semantics.

### Task 5: Verification gates

- [ ] Run `go test ./... -count=1`, integration lifecycle tests, `go vet ./...`, `go build ./cmd/drift`, and `git diff --check`.
- [ ] Run an independent DeepSeek read-only acceptance with a new context; record `PASS`, `FAIL`, or `BLOCKED` without exposing credentials.
- [ ] Commit M4.1, fast-forward `master`, and rerun deterministic verification on `master`.
