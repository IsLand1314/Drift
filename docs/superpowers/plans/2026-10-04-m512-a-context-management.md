# M5.12-A Context Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden Drift context budgeting, compaction, and Session restoration without introducing long-term Memory or external storage.

**Architecture:** Reuse `internal/agent.Runner` as the in-memory Context owner and `internal/session` JSONL as the durable event source. Add a small structured context snapshot/restore path around the existing compactor; keep summaries atomic and keep security redaction at the existing session boundary.

**Tech Stack:** Go standard library, existing `internal/agent`, `internal/session`, `internal/llm`, and current test helpers.

**Spec:** `docs/superpowers/specs/2026-10-04-m512-context-management-design.md`

## Global Constraints

- Session remains JSONL and Context remains in memory.
- UTF-8 byte budget is conservative and must not be described as token accounting.
- No API Key, full command output, or unsanitized absolute path may enter summaries or audit metadata.
- No new dependency, vector database, Memory store, Skill lifecycle, or permission/sandbox change.

---

### Task 1: Lock the context invariants with failing tests

**Files:**
- Modify: `internal/agent/agent_test.go`
- Modify: `internal/session/jsonl_test.go`

**Interfaces:**
- Consumes: existing `Runner`, `Compact`, `RestoreMessages`, `PlanState`, and session event writer.
- Produces: regression tests that define preserved plan/task metadata, atomic failure, and redaction expectations.

- [ ] **Step 1: Write failing tests**

Add tests for: compaction preserving the newest complete tool-call/result pair; failed/empty/pseudo-tool summaries leaving `Runner.Messages()` byte-for-byte unchanged; and compaction events containing counters but not summary text or secrets.

- [ ] **Step 2: Run the focused tests**

Run: `go test ./internal/agent ./internal/session -run 'Test.*Compaction|Test.*Context' -count=1`

Expected: at least one new test fails before implementation changes.

- [ ] **Step 3: Commit the tests**

```bash
git add internal/agent/agent_test.go internal/session/jsonl_test.go
git commit -m "test: define M5.12 context invariants"
```

### Task 2: Make compaction preserve a valid continuation boundary

**Files:**
- Modify: `internal/agent/agent.go`
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `Runner.Compact(ctx) (CompactResult, error)`.
- Produces: atomic compaction that never starts the retained slice with an orphan tool result or assistant tool call.

- [ ] **Step 1: Implement the smallest boundary helper**

Add an unexported helper that walks backward from the retention index until the retained messages form a valid continuation boundary. Preserve the existing `compactKeepMessages` policy and return `ErrCompactionInsufficient` only when no safe boundary exists.

- [ ] **Step 2: Keep replacement atomic**

Build the summary and retained messages in locals; assign `r.messages` only after non-empty, non-pseudo-tool validation and boundary validation succeed.

- [ ] **Step 3: Run focused tests**

Run: `go test ./internal/agent -run 'Test.*Compaction|Test.*Context' -count=1`

Expected: PASS, including the failure-injection tests from Task 1.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "fix: preserve valid context boundary during compaction"
```

### Task 3: Rebuild Context from Session events without leaking audit-only data

**Files:**
- Modify: `internal/session/read.go`
- Modify: `internal/session/session.go`
- Modify: `internal/app/chat_resume.go`
- Test: `internal/session/read_test.go`
- Test: `internal/app/chat_resume_test.go`

**Interfaces:**
- Consumes: `session.ReadEntries` and existing chat resume flow.
- Produces: a resume projection that restores messages and plan/task state through the same Context budget path.

- [ ] **Step 1: Write failing resume tests**

Create a temporary JSONL session containing user/assistant/tool events, compaction counters, plan state, and a sensitive command. Assert the reconstructed projection excludes audit-only sensitive fields and retains plan/task state.

- [ ] **Step 2: Implement a narrow projection**

Add an internal projection function that converts only supported conversation and state events into `[]llm.Message` plus plan metadata. Do not concatenate arbitrary audit `Text`, `Result`, `Command`, or `Arguments` fields into the prompt.

- [ ] **Step 3: Route restored messages through existing Runner constructors**

Use `NewRunnerWithMessagesAndSystemContext` and the existing budget checks; do not create a second context builder.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/session ./internal/app -run 'Test.*Resume|Test.*Context' -count=1`

Expected: PASS; sensitive fields absent and plan/task state preserved.

- [ ] **Step 5: Commit**

```bash
git add internal/session/read.go internal/session/session.go internal/app/chat_resume.go internal/session/read_test.go internal/app/chat_resume_test.go
git commit -m "feat: rebuild bounded context from session events"
```

### Task 4: Add automatic pre-request compaction and observability

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/app/trace.go`
- Test: `internal/agent/agent_test.go`
- Test: `internal/app/trace_test.go`

**Interfaces:**
- Consumes: `Runner.NeedsCompaction`, `Runner.Compact`, and existing compaction events.
- Produces: automatic compaction before a request crosses the configured threshold, with failure treated as recoverable and no partial state.

- [ ] **Step 1: Write the threshold test**

Use a fake client with an oversized message list; assert the client receives a compacted request before the normal model request and the emitted event contains only counters.

- [ ] **Step 2: Implement the pre-request guard**

Before each model request, call the existing budget calculation. If the next request would enter the trigger window, call `Compact`; on failure emit `EventCompactionError`, restore the original messages, and return the existing recoverable context error.

- [ ] **Step 3: Verify trace output**

Keep trace output to event type and counters; never print summary content.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/agent ./internal/app -run 'Test.*Compaction|Test.*Context' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/app/trace.go internal/agent/agent_test.go internal/app/trace_test.go
git commit -m "feat: compact context before request budget overflow"
```

### Task 5: Run the M5.12-A acceptance matrix and update evidence

**Files:**
- Create: `spec/m5.12-context-management.md`
- Create: `spec/m5.12-context-acceptance.md`
- Create: `artifacts/verification/m5.12-a/acceptance.md`
- Modify: `spec/current.md`

**Interfaces:**
- Consumes: all implementation and test behavior from Tasks 1–4.
- Produces: auditable acceptance evidence and current-version synchronization.

- [ ] **Step 1: Run deterministic checks**

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o .codex-temp\drift-m512-a.exe ./cmd/drift
git diff --check
```

- [ ] **Step 2: Run temporary workspace integration checks**

Verify long-message compaction, failed-summary rollback, Session resume, plan/task preservation, and sensitive-field redaction in a temporary workspace; delete all generated files afterward.

- [ ] **Step 3: Run one real LLM acceptance**

Use a Provider with native Tool Calls to create a long read-only conversation, trigger compaction, resume the Session, and verify the next answer still sees the selected workspace and unfinished task. Record only counters and sanitized outcomes.

- [ ] **Step 4: Update acceptance evidence**

Record PASS/FAIL per acceptance item, exact commands, and cleanup result. Do not copy model secrets or full transcript into the document.

- [ ] **Step 5: Commit**

```bash
git add spec/m5.12-context-management.md spec/m5.12-context-acceptance.md artifacts/verification/m5.12-a/acceptance.md spec/current.md
git commit -m "docs: record M5.12-A context acceptance"
```
