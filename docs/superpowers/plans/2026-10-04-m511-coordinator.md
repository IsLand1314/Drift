# M5.11 Coordinator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace sequential `PlanExecute` orchestration with a deterministic Coordinator that schedules ready tasks, enforces concurrency, persists lifecycle state, handles cancellation/retry/blocking, and coordinates worktree merges without bypassing existing safety layers.

**Architecture:** Add a small Coordinator control-plane package around the existing `TaskState`, `TaskRunner`, `TaskMerger`, and `TaskHandle` interfaces. The Coordinator owns queueing and lifecycle transitions; existing tools remain the public control surface, with `PlanExecute` delegating to it. No new LLM tool is required until the final status integration.

**Tech Stack:** Go standard library, existing task/agent/git packages, existing conversation persistence and audit events.

**Spec:** `spec/m5.11-coordinator-design.md`

## Global Constraints

- Coordinator never directly reads/writes files, executes Bash, or calls MCP.
- Existing permission, sandbox, approval, audit, and worktree boundaries remain authoritative.
- Default maximum concurrency is 2; accepted range is 1–4.
- Only transient failures are retryable; permission, sandbox, parameter, dependency, and merge-conflict failures are not automatically retried.
- Do not auto-run `reset`, `stash`, `clean`, or delete user changes.
- Every implementation step follows red-green TDD and ends with a focused test.

## 当前执行状态（2026-10-04）

- M5.11-A～D：已实现并通过确定性 TDD、全量测试和 race 验收。
- M5.11-E：`PlanExecute` 已委托 Coordinator，`CoordinatorStatus` 已按需加载并汇总状态；使用 DeepSeek `deepseek-chat` 的真实 TTY 原生工具链已完成 `PlanUpdate → ExitPlanMode → PlanExecute → CoordinatorStatus → TaskGet`，门禁 PASS。

---

### Task 1: Define Coordinator state and ready queue

**Files:**
- Create: `internal/coordinator/coordinator.go`
- Create: `internal/coordinator/coordinator_test.go`
- Modify: `internal/tool/task.go` only if a small exported adapter is required

**Interfaces:**
- `Coordinator` consumes `[]tool.TaskState`, `tool.TaskRunner`, and `tool.TaskMerger`.
- `Coordinator.Run(ctx, root, tasks) (RunResult, error)` schedules one plan.
- `Coordinator.Status() []tool.TaskState` returns a sorted snapshot.

- [ ] Write failing tests for one ready task, a dependency-blocked task, and deterministic queue ordering.
- [ ] Run `go test ./internal/coordinator -run 'TestReady|TestDependency' -count=1`; expect failure because the package/API does not exist.
- [ ] Implement a mutex-protected ready queue, dependency checks, and terminal state snapshots using standard library only.
- [ ] Run the focused tests and confirm PASS.
- [ ] Commit `feat: add deterministic coordinator queue`.

### Task 2: Add bounded concurrent execution

**Files:**
- Modify: `internal/coordinator/coordinator.go`
- Modify: `internal/coordinator/coordinator_test.go`

**Interfaces:**
- `CoordinatorOptions{MaxConcurrency int; MaxRetries int; RetryBackoff time.Duration}`.
- Coordinator starts at most `MaxConcurrency` runners and waits for handles without holding the state lock.

- [ ] Write a failing test with three fake handles proving only two runners start concurrently.
- [ ] Run the focused test and verify it fails before implementation.
- [ ] Implement dispatch, completion collection, and release of concurrency slots.
- [ ] Add tests proving independent tasks continue after one task fails.
- [ ] Run `go test ./internal/coordinator -count=1`; confirm PASS.
- [ ] Commit `feat: run coordinator tasks with bounded concurrency`.

### Task 3: Cancellation, timeout, and lifecycle persistence

**Files:**
- Modify: `internal/coordinator/coordinator.go`
- Modify: `internal/coordinator/coordinator_test.go`
- Modify: `internal/conversation/store.go` only where the existing task snapshot is extended

**Interfaces:**
- `Coordinator.Cancel()` stops dispatch, cancels active handles, and marks pending/ready/running tasks according to their terminal state.
- `Coordinator.Restore([]tool.TaskState) error` restores non-terminal state safely.

- [ ] Write failing tests for Coordinator cancellation, child timeout, and restoration of running tasks as pending.
- [ ] Run focused tests and confirm failure.
- [ ] Implement cancellation using existing `TaskHandle.Cancel` and context cancellation; never cancel completed tasks.
- [ ] Persist retry count, last error, and lifecycle timestamps through the existing task snapshot format.
- [ ] Run focused and conversation tests; confirm PASS.
- [ ] Commit `feat: add coordinator cancellation and persistence`.

### Task 4: Retry classification and dependency blocking

**Files:**
- Modify: `internal/coordinator/coordinator.go`
- Modify: `internal/coordinator/coordinator_test.go`

**Interfaces:**
- `RetryPolicy` classifies only timeout, transient provider, and child-start failures as retryable.
- Retry exhaustion sets `failed`; dependents become `blocked` with `dependency <id> failed`.

- [ ] Write failing tests for one retryable timeout, one non-retryable permission error, retry exhaustion, and transitive dependent blocking.
- [ ] Run focused tests and verify failure.
- [ ] Implement bounded retry count and backoff; avoid retrying user or safety decisions.
- [ ] Implement transitive blocking for all pending/ready dependents.
- [ ] Run focused tests and confirm PASS.
- [ ] Commit `feat: add coordinator retry and failure propagation`.

### Task 5: Worktree merge coordination

**Files:**
- Modify: `internal/coordinator/coordinator.go`
- Modify: `internal/coordinator/coordinator_test.go`
- Reuse: `internal/git/merge_queue.go`, `internal/git/worktree.go`

**Interfaces:**
- Completed tasks with a worktree call the existing `TaskMerger`.
- Successful merge sets `merged`; conflict sets `conflict_waiting`; merge failure sets `failed` without changing the main worktree unexpectedly.

- [ ] Write failing tests for successful merge, conflict queueing, explicit retry, and preservation of main-worktree state.
- [ ] Run focused tests and confirm failure.
- [ ] Implement merge callbacks without adding direct Git commands to Coordinator.
- [ ] Run `go test ./internal/coordinator ./internal/git -count=1`; confirm PASS.
- [ ] Commit `feat: coordinate worktree merges`.

### Task 6: Delegate PlanExecute to Coordinator

**Files:**
- Modify: `internal/tool/plan.go`
- Modify: `internal/tool/registry.go`
- Modify: `internal/tool/plan_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Registry exposes an internal `SetCoordinator` adapter; no new LLM tool is registered.
- `PlanExecute` validates plan phase and delegates the current plan to Coordinator.
- Existing `TaskStatus`, `TaskCancel`, and `TaskMerge` read the same state store.

- [ ] Write a failing integration test proving PlanExecute dispatches two ready tasks concurrently through the Coordinator.
- [ ] Run the test and confirm failure with the current sequential implementation.
- [ ] Wire one Coordinator instance per chat registry and preserve existing runner/merger callbacks.
- [ ] Keep current safety checks and task descriptions intact.
- [ ] Run `go test ./internal/tool ./internal/app -count=1`; confirm PASS.
- [ ] Commit `feat: route plan execution through coordinator`.

### Task 7: Final acceptance and documentation

**Files:**
- Modify: `spec/current.md` to mark M5.11 only after all gates pass
- Create: `spec/m5.11-coordinator-acceptance.md`
- Create: `artifacts/verification/m5.11/acceptance.md`

- [ ] Run `go test ./... -count=1`, `go vet ./...`, `go build ./cmd/drift`, and `git diff --check` for changed files.
- [ ] Run deterministic matrix: success, parallel, dependency order, failure/blocking, cancel, timeout, retry, conflict, restore.
- [ ] Run real DeepSeek: two independent tasks and one dependency chain; record redacted tool/status output.
- [ ] Verify no API key appears in logs or artifacts.
- [ ] Clean temporary worktrees and acceptance directories.
- [ ] Commit acceptance evidence only after all results are authoritative.
