# M1.9 Anthropic Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in Anthropic Messages SSE provider that normalizes text, tool calls, tool results, and usage into the existing `llm.Client` contract.

**Architecture:** Add a self-contained `internal/llm/anthropic` adapter. The application composition root selects either the existing OpenAI-compatible adapter or Anthropic from `-provider`; Agent, tools, persistence, chat, and UI continue to receive only `llm.Client` and provider-neutral messages.

**Tech Stack:** Go 1.26; `net/http`, `encoding/json`, `bufio`, `httptest`; no Anthropic SDK and no new runtime dependency.

**Spec:** `docs/superpowers/specs/2026-10-01-m19-anthropic-provider-design.md`

## Global Constraints

- Default provider remains `openai`; existing OpenAI-compatible commands and environment variables remain valid.
- Anthropic uses `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, and `ANTHROPIC_MODEL`; the key never appears in errors, trace, Session, or conversation snapshots.
- Anthropic requests use `POST /messages`, `x-api-key`, `anthropic-version: 2023-06-01`, and streaming SSE.
- Only the existing three read-only tools are exposed; no server tools, thinking, beta headers, retries, writes, shell, or MCP.
- Non-2xx bodies, malformed SSE, invalid tool JSON, oversize events, timeouts, and disconnects use safe existing error stages.
- Every task must preserve `go test ./...`, `go vet ./...`, and non-ANSI CLI behavior.

---

### Task 1: Build the Anthropic wire adapter

**Files:**
- Create: `internal/llm/anthropic/client.go`
- Create: `internal/llm/anthropic/client_test.go`

**Interfaces:**
- Produces `func New(baseURL, key string) (*Client, error)`.
- Produces `func (c *Client) Stream(context.Context, llm.Request, func(llm.StreamEvent) error) (llm.Completion, error)`.
- Keeps all Anthropic wire structs private to this package.

- [ ] **Step 1: Write request and message mapping tests**

Use an `httptest.Server` handler that decodes the request and asserts:

```json
{
  "model": "claude-test",
  "max_tokens": 4096,
  "system": "read-only system",
  "messages": [{"role":"user","content":[{"type":"text","text":"hello"}]}],
  "tools": [{"name":"read_file","input_schema":{"type":"object"}}],
  "stream": true
}
```

Assert headers `x-api-key`, `anthropic-version`, `content-type`, and `accept`; assert that the key is absent from any returned error string.

- [ ] **Step 2: Run the new tests to verify the adapter is absent**

Run:

```powershell
go test ./internal/llm/anthropic -count=1 -v
```

Expected: FAIL because `anthropic.New` and `anthropic.Client.Stream` do not exist yet.

- [ ] **Step 3: Implement URL validation and request encoding**

Match the OpenAI adapter's validation: accept only HTTP(S) URLs without userinfo, query, or fragment, trim trailing `/`, append `/messages`, and use an `http.Client` timeout of five minutes with redirects disabled. Extract each `role=system` message into the top-level `system` string; encode user and assistant text as text blocks; convert `llm.ToolDefinition.Function` from `{name,description,parameters}` to `{name,description,input_schema}`.

- [ ] **Step 4: Implement assistant tool calls and tool results**

Encode an assistant message's `ToolCalls` as ordered `tool_use` blocks with decoded JSON input. Encode each `role=tool` message as a user message containing one `tool_result` block with `tool_use_id` and text content. Return a safe protocol error when a tool argument is not valid JSON or a tool result has no ID.

- [ ] **Step 5: Implement the SSE state machine**

Parse blank-line-delimited `event:`/`data:` records. Handle `message_start` input usage, `content_block_start` tool ID/name/index, `content_block_delta` text and partial JSON arguments, `message_delta` stop reason/output usage, and `message_stop`. Emit `llm.StreamEvent.Text` and `llm.StreamEvent.ToolCallDelta` incrementally, sort completed tool calls by index, and return `llm.Completion` only after `message_stop`.

- [ ] **Step 6: Reuse safe Provider stages and limits**

Use the existing five-minute context-aware HTTP request, 1 MiB scanner line/event limits, `llm.ProviderError`, and stages `provider_timeout`, `provider_transport`, `provider_http`, `provider_non_sse`, `provider_sse_invalid_json`, `provider_sse_server_error`, `provider_sse_event_too_large`, `provider_sse_line_too_large`, `provider_sse_read`, and `provider_sse_disconnected`.

- [ ] **Step 7: Verify the adapter tests**

Run:

```powershell
go test ./internal/llm/anthropic -count=1 -v
```

Expected: PASS for headers, mapping, text deltas, tool deltas, usage, HTTP errors, malformed JSON, oversized records, timeout cancellation, and early disconnect.

- [ ] **Step 8: Commit the adapter**

```powershell
git add internal/llm/anthropic/client.go internal/llm/anthropic/client_test.go
git commit -m "功能：增加 Anthropic Messages Provider"
```

### Task 2: Add provider selection and configuration

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- CLI accepts `-provider openai|anthropic`, defaulting to `openai`.
- Environment lookup uses `DRIFT_PROVIDER` only when `-provider` is omitted.
- Provider construction returns `(llm.Client, model string, error)` through one local switch in the composition root.

- [ ] **Step 1: Add failing app tests**

Add tests that invoke `RunWithInput` with a local Anthropic SSE server and assert `-provider anthropic -base-url <server> -model claude-test` reaches that server. Add tests for missing `ANTHROPIC_API_KEY`, missing `ANTHROPIC_MODEL`, and unknown provider returning code 2 without a request.

- [ ] **Step 2: Run the provider-selection tests**

Run:

```powershell
go test ./internal/app -run 'Test.*Provider|Test.*Anthropic' -count=1 -v
```

Expected: FAIL because the flag and provider switch are not present.

- [ ] **Step 3: Parse provider-specific defaults after flags**

Add `-provider` with default `config.MergeLookup(dotenv, getenv, "DRIFT_PROVIDER")`, add `-model` and `-base-url` with empty defaults, then after `flags.Parse` select defaults by provider:

```text
openai:    OPENAI_MODEL,    OPENAI_BASE_URL (fallback https://api.openai.com/v1), OPENAI_API_KEY
anthropic: ANTHROPIC_MODEL, ANTHROPIC_BASE_URL (fallback https://api.anthropic.com/v1), ANTHROPIC_API_KEY
```

Explicit `-model` and `-base-url` values remain highest priority.

- [ ] **Step 4: Construct the selected client**

Switch only on the validated provider name, call `openai.New` or `anthropic.New`, wrap the result in the existing `modelClient`, and keep all later Agent setup unchanged. Never pass an unrelated provider key to another client.

- [ ] **Step 5: Verify configuration behavior**

Run:

```powershell
go test ./internal/app ./internal/config -count=1 -v
```

Expected: PASS for default OpenAI compatibility, Anthropic selection, command-line overrides, dotenv/environment precedence, and pre-request validation.

- [ ] **Step 6: Commit provider selection**

```powershell
git add internal/app/app.go internal/app/app_test.go internal/config/config_test.go
git commit -m "功能：增加 Provider 选择与 Anthropic 配置"
```

### Task 3: Prove Agent and chat compatibility

**Files:**
- Modify: `internal/agent/agent_test.go`
- Modify: `internal/app/chat_test.go`
- Create: `artifacts/verification/m1.9/provider-test.txt`

**Interfaces:**
- Agent tests continue to use only `llm.Client`; no Anthropic package is imported by `internal/agent`.

- [ ] **Step 1: Add an end-to-end fake Anthropic stream test**

Use a local server that returns one `tool_use` stream, then one `message_stop` final answer after the tool result. Assert the existing `read_file` tool executes once, the second request contains an Anthropic `tool_result`, and stdout contains only the final answer.

- [ ] **Step 2: Add usage and status coverage**

Return `input_tokens` in `message_start` and `output_tokens` in `message_delta`; assert `/status` reports the same `Tokens: <in> in / <out> out` values and the JSONL audit stores only numeric usage fields.

- [ ] **Step 3: Run focused integration tests**

Run:

```powershell
go test ./internal/agent ./internal/app -run 'Test.*Anthropic|Test.*Provider|Test.*Usage' -count=1 -v
```

Expected: PASS with no OpenAI-specific fields in Agent code.

- [ ] **Step 4: Capture focused evidence**

Record the focused test command and PASS output in `artifacts/verification/m1.9/provider-test.txt`; do not include API keys, prompts, file contents, or absolute workspace paths.

- [ ] **Step 5: Commit compatibility coverage**

```powershell
git add internal/agent/agent_test.go internal/app/chat_test.go artifacts/verification/m1.9/provider-test.txt
git commit -m "测试：覆盖 Anthropic Agent 与用量兼容性"
```

### Task 4: Synchronize version documents and complete verification

**Files:**
- Modify: `README.md`
- Modify: `README.en.md` only if the file exists; otherwise do not recreate it
- Modify: `spec/current.md`
- Modify: `doc/architecture.md`
- Create: `doc/m1.9-anthropic-provider.md`
- Create: `artifacts/verification/m1.9/full-check.txt`

- [ ] **Step 1: Update the current-version statements**

Change README and `spec/current.md` to M1.9. Document `-provider`, the two provider-specific environment sets, and the fact that DSML protection covers all final text paths. Change the dependency statement to include Bubble Tea/Bubbles used by the TTY input layer.

- [ ] **Step 2: Align architecture and M1.5 history**

Document `internal/llm/anthropic` as a provider adapter and explicitly state that Agent remains Provider-neutral. Keep `doc/m1.5-runtime-hygiene.md` limited to M1.5; do not mix M1.4 or M1.8 visual acceptance text into it.

- [ ] **Step 3: Write M1.9 manual acceptance**

In `doc/m1.9-anthropic-provider.md`, include commands for OpenAI compatibility, Anthropic fake-server tests, missing-key validation, `/status` usage, and redaction inspection. State that real Anthropic validation requires the user’s own key and must not be captured in artifacts.

- [ ] **Step 4: Run the complete verification**

Run:

```powershell
go test ./... -count=1
go vet ./...
go build -o .codex-temp\drift-m19.exe ./cmd/drift
git diff --check
```

Expected: all commands exit 0; the build artifact stays ignored; existing M1.8 TTY behavior remains unchanged.

- [ ] **Step 5: Commit the completed milestone**

```powershell
git add README.md spec/current.md doc/architecture.md doc/m1.9-anthropic-provider.md artifacts/verification/m1.9/full-check.txt
git commit -m "功能：完成 M1.9 Anthropic Provider"
```

## Self-review checklist

- Task 1 covers every Anthropic protocol item in the spec: request headers, system split, content blocks, tool schema mapping, tool calls, tool results, SSE, usage, limits, and errors.
- Task 2 covers CLI/env precedence, default OpenAI compatibility, and request-before-validation.
- Task 3 proves Agent and chat stay Provider-neutral and preserves M1.6 usage behavior.
- Task 4 updates all current-version and architecture references without recreating the deleted English README.
- No task introduces retries, automatic fallback, server tools, writes, Shell, MCP, or a second Agent loop.
