# M1.6 Token Usage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Parse optional real Token usage from OpenAI-compatible streaming responses, carry it through Agent events, persist safe counters, and display it in `/status` without estimating or fabricating values.

**Architecture:** The OpenAI adapter requests `stream_options.include_usage=true` and parses usage from the final SSE chunk. The provider-neutral `llm.Completion` exposes an optional `Usage`; the Agent emits a safe usage event for every completed provider request. The chat loop accumulates counters and persists them with the existing conversation snapshot while the audit JSONL stores only numeric usage metadata.

**Tech Stack:** Go standard library, existing OpenAI-compatible SSE client, existing `agent.Event`, JSONL audit writer, JSON conversation snapshots, Go `testing`.

**Spec:** `docs/superpowers/specs/2026-10-01-m16-token-usage-design.md`

## Global Constraints

- Keep Context byte estimation and Provider Token usage as separate metrics.
- Never estimate Token counts from bytes, text length, or reasoning content.
- Missing Provider usage is not an error; display `unavailable`, or `(partial)` when only some requests report usage.
- Usage events and audit entries contain counters only; never include prompts, answers, tool results, API keys, Provider URLs, or absolute paths.
- Preserve old conversation snapshots with absent usage fields.
- Do not add a retry when a Provider rejects `stream_options`.
- Keep the current read-only tool boundary and standard-library-only dependency policy.

---

### Task 1: Add provider-neutral usage and parse OpenAI SSE usage

**Files:**
- Modify: `internal/llm/client.go`
- Modify: `internal/llm/openai/client.go`
- Test: `internal/llm/openai/client_test.go`

**Interfaces:**
- Produce `llm.Usage` with `InputTokens`, `OutputTokens`, and `TotalTokens`.
- Extend `llm.Completion` with `Usage *Usage`; nil means the Provider did not report usage.
- Keep `llm.Client.Stream` signature unchanged.

- [ ] **Step 1: Write the failing tests**

Add a streaming fixture whose final chunk has empty `choices` and:

```json
{"usage":{"prompt_tokens":17,"completion_tokens":9,"total_tokens":26}}
```

Assert that the request JSON contains:

```json
"stream_options":{"include_usage":true}
```

and that the returned completion has `Usage` values `17`, `9`, and `26`. Add a second fixture without `usage` and assert `Usage == nil`.

- [ ] **Step 2: Run the focused tests and verify failure**

Run:

```powershell
go test ./internal/llm/openai -run 'Test(Stream|ReadStream).*Usage' -count=1 -v
```

Expected: compile or assertion failure because `Usage` and `stream_options` are not implemented.

- [ ] **Step 3: Implement the minimal model and wire format**

In `internal/llm/client.go`, add:

```go
type Usage struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int
}
```

Add `Usage *Usage` to `Completion`. In `internal/llm/openai/client.go`, add `StreamOptions` to the request payload and set `IncludeUsage: true`. Extend the decoded SSE chunk with a nullable usage object. Accept usage-only chunks with empty `choices`; reject negative or non-integer values as `provider_sse_invalid_json`.

- [ ] **Step 4: Run focused tests and verify pass**

Run the same command from Step 2. Expected: all usage parsing and missing-usage tests pass.

- [ ] **Step 5: Commit the provider slice**

```powershell
git add internal/llm/client.go internal/llm/openai/client.go internal/llm/openai/client_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：解析流式 Token 用量" -m "参与人：island"
```

### Task 2: Emit safe usage events from Agent requests

**Files:**
- Modify: `internal/agent/event.go`
- Modify: `internal/agent/agent.go`
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Add `EventModelUsage`.
- Add `InputTokens`, `OutputTokens`, `TotalTokens`, and `UsageAvailable` to `agent.Event`.
- Emit exactly one usage event after every successful `client.Stream` completion, including a completion without usage (`UsageAvailable=false`).

- [ ] **Step 1: Write the failing tests**

Use the existing `scriptedClient` to run one direct answer and one tool-call-plus-final-answer flow. Assert that each successful Provider request produces one usage event, that reported values are copied, and that a missing usage event is marked unavailable. Assert no prompt or answer content is stored in the usage event.

- [ ] **Step 2: Run the focused Agent tests and verify failure**

```powershell
go test ./internal/agent -run 'Test.*Usage' -count=1 -v
```

Expected: failure because no usage event exists.

- [ ] **Step 3: Emit the event at the shared completion boundary**

Immediately after `client.Stream` returns successfully in `Runner.RunEvents`, emit the usage event before handling tool calls or final text. Copy only numeric values from `completion.Usage`; use `UsageAvailable=false` when it is nil. If the sink rejects the event, return that sink error.

- [ ] **Step 4: Run focused tests and verify pass**

Run the command from Step 2 and then `go test ./internal/agent -count=1`. Expected: usage event tests and existing Agent tests pass.

- [ ] **Step 5: Commit the Agent slice**

```powershell
git add internal/agent/event.go internal/agent/agent.go internal/agent/agent_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：增加 Agent Token 用量事件" -m "参与人：island"
```

### Task 3: Persist safe usage counters in conversation snapshots

**Files:**
- Modify: `internal/conversation/store.go`
- Modify: `internal/conversation/store_test.go`
- Modify: `internal/app/chat.go`
- Modify: `internal/app/app.go`
- Test: `internal/app/chat_test.go`

**Interfaces:**
- Add snapshot counters: `InputTokens`, `OutputTokens`, `ReportedRequests`, and `UnreportedRequests`.
- Keep snapshot version `1`; absent JSON fields decode as zero for backward compatibility.
- Add a chat usage accumulator that merges one Agent usage event at a time.

- [ ] **Step 1: Write failing storage and chat tests**

Add tests that save/load counters through `conversation.Store`, load an old snapshot without counters as zero, and run chat with two Provider responses to assert cumulative counters. Add a resume test to assert counters continue from the snapshot. Add a `--no-session` test to assert counters remain process-local and no full snapshot is created.

- [ ] **Step 2: Run focused tests and verify failure**

```powershell
go test ./internal/conversation ./internal/app -run '(Usage|Token)' -count=1 -v
```

Expected: compile or assertion failure because snapshots and chat accumulation do not yet have usage fields.

- [ ] **Step 3: Implement backward-compatible persistence**

Add `omitempty` JSON fields to `persistedSnapshot`, map them in `toPersisted` and `fromPersisted`, and update `chatPersistence.saveRunner`/`clearRunner` so usage counters are preserved or reset consistently. In the chat event sink, merge usage events before saving the completed turn. Do not append usage counters to user-visible conversation messages.

- [ ] **Step 4: Run focused tests and verify pass**

Run the command from Step 2, then `go test ./internal/conversation ./internal/app -count=1`. Expected: new persistence/resume tests and existing chat tests pass.

- [ ] **Step 5: Commit the persistence slice**

```powershell
git add internal/conversation/store.go internal/conversation/store_test.go internal/app/chat.go internal/app/app.go internal/app/chat_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：持久化会话 Token 计数" -m "参与人：island"
```

### Task 4: Record safe usage in audit and render `/status`

**Files:**
- Modify: `internal/session/jsonl.go`
- Modify: `internal/session/jsonl_test.go`
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`
- Modify: `doc/m1.4-context-compaction.md`
- Modify: `spec/current.md`
- Modify: `README.md`
- Modify: `doc/Process/面板视觉设计.md`

**Interfaces:**
- Extend the safe JSONL entry with numeric usage fields and the stable event type; never copy event text.
- `/status` reads the accumulator and renders `in / out`, `unavailable`, or `(partial)` using the existing aligned/colorized panel.

- [ ] **Step 1: Write failing audit and status tests**

Assert that a usage event produces a JSONL line containing only event type and numeric counters. Assert that `/status` displays:

```text
Tokens:      17 in / 9 out
```

for complete usage, `Tokens: unavailable` when all requests lack usage, and `Tokens: 17 in / 9 out (partial)` for mixed requests. Assert prompts, answers, API keys, and paths are absent.

- [ ] **Step 2: Run focused tests and verify failure**

```powershell
go test ./internal/session ./internal/app -run '(Usage|Token|Status)' -count=1 -v
```

Expected: failure because audit fields and status rendering do not yet consume usage counters.

- [ ] **Step 3: Implement safe audit and presentation**

Map only numeric usage fields in `entryFromEvent`. Keep the existing two-space indentation, fixed label column, ANSI label/value colors, Workspace color, and non-terminal plain-text fallback. Replace only the `Tokens` row source; do not alter Context byte calculations.

- [ ] **Step 4: Run focused tests and verify pass**

Run the command from Step 2, then `go test ./internal/session ./internal/app -count=1`.

- [ ] **Step 5: Commit the audit and UI slice**

```powershell
git add internal/session/jsonl.go internal/session/jsonl_test.go internal/app/chat.go internal/app/chat_test.go doc/m1.4-context-compaction.md spec/current.md README.md doc/Process/面板视觉设计.md
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：在状态面板显示真实 Token 用量" -m "参与人：island"
```

### Task 5: Full verification and evidence

**Files:**
- Create: `artifacts/verification/m1.6/provider-usage-test.txt`
- Create: `artifacts/verification/m1.6/chat-usage-test.txt`
- Create: `artifacts/verification/m1.6/storage-test.txt`
- Create: `artifacts/verification/m1.6/full-check.txt`

- [ ] **Step 1: Run the complete automated suite**

```powershell
go test ./... -count=1
go vet ./...
go build -o .codex-temp\drift-m16.exe ./cmd/drift
git diff --check
```

Capture each command as UTF-8 evidence without API keys or conversation text.

- [ ] **Step 2: Run the local manual usage check**

With a local mock SSE server returning usage, run `drift chat`, complete two prompts, run `/compact`, then `/status`. Verify the cumulative input/output counters increase only after Provider requests and survive `--resume`. Run the same sequence with missing usage and verify `unavailable`/`partial` behavior.

- [ ] **Step 3: Inspect persisted data**

Check `.drift/conversations/*.json` and `.drift/sessions/*.jsonl`. Confirm only numeric usage fields are added and no prompts, answers, API keys, Provider URLs, or absolute workspace paths appear.

- [ ] **Step 4: Commit evidence and documentation**

```powershell
git add artifacts/verification/m1.6 docs/superpowers/specs/2026-10-01-m16-token-usage-design.md docs/superpowers/plans/2026-10-01-m16-token-usage.md
git -c user.name=island -c user.email=island0920@163.com commit -m "验证：补充 M1.6 Token 用量证据" -m "参与人：island"
```

## Self-review

- Design requirements map to Tasks 1–4; verification and evidence map to Task 5.
- Missing usage, partial usage, persistence, resume, compact, audit redaction, and `/status` rendering each have explicit tests.
- No task changes the read-only tool set, Context byte limit, or Provider retry policy.
- The plan intentionally does not add price calculation, Memory, a second Provider, or a TUI.
