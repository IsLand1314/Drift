# M0.2.2 Environment and Native Tool Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make local configuration safer and make the read-only Agent reject pseudo-tool text instead of printing model-specific DSML as if it were a supported tool call.

**Architecture:** Load an optional project-local `.env` file at startup, with process environment values taking precedence and CLI flags taking precedence over both. Keep the Agent's only advertised capability as the native `read_file` function, add a system instruction that forbids pseudo-tool syntax, and fail clearly when a known DSML marker appears in first-turn text.

**Tech Stack:** Go 1.26+, standard library only, existing `llm.Client`, `httptest` and scripted Agent clients.

**Spec:** `spec/current.md` (current delivery scope) and the user-confirmed M0.2.2 requirements in this plan.

## Global Constraints

- Keep the runtime read-only: no write, delete, edit, shell, command execution or new tool is introduced.
- Preserve OpenAI-compatible Chat Completions SSE and the existing maximum of two provider requests, four `read_file` calls and 512 KiB aggregate successful reads.
- Configuration precedence is CLI flags > process environment > project-local `.env` > built-in defaults.
- A missing `.env` is valid; malformed `.env` lines fail before a provider request and must not print secret values.
- `.env` is ignored; `.env.example` contains empty key material and is safe to commit.
- The workspace reader refuses `.env` and every `.env.*` file so local credentials cannot enter model context.
- First-turn text is buffered; only final native-tool-free text is emitted. Known DSML/pseudo-tool markers are an incompatibility error, not stdout content.
- Commits use Chinese messages with `island` as author and committer.

---

### Task 1: Add optional dotenv configuration with explicit precedence

**Files:**

- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `.gitignore`
- Create: `.env.example`

**Interfaces:**

- `config.LoadDotEnv(path string) (map[string]string, error)` reads blank lines, full-line `#` comments and `KEY=VALUE` entries; it accepts matching single or double quotes around the complete value, trims surrounding whitespace, rejects empty keys and malformed lines, and never expands variables.
- `config.MergeLookup(dotenv map[string]string, getenv func(string) string, key string) string` returns the process value when non-empty, otherwise the dotenv value.
- `app.Run` keeps its existing signature and uses `.env` from the current working directory; `-model` and `-base-url` continue to override all environment sources.

- [ ] **Step 1: Write failing parser and precedence tests**

Test these exact behaviors: comments/blanks and quoted values parse; malformed `NO_EQUALS` returns an error naming the line number without including the value; missing file returns an empty map; a non-empty process value wins over `.env`, while an empty process value falls back to `.env`.

- [ ] **Step 2: Run focused tests and verify they fail**

Run: `go test ./internal/config ./internal/app -run 'Test(LoadDotEnv|MergeLookup|RunLoadsDotEnv)' -count=1`

Expected: fail because the config package and dotenv loading do not exist.

- [ ] **Step 3: Implement the minimal loader and app wiring**

Use only `os.Open`, `bufio.Scanner` and `strings`. Keep the scanner bounded to a practical line size and return a clear configuration error if scanning fails. In `app.Run`, load `.env` after flag-set creation but before resolving defaults; a missing file is ignored, any other read/parse error returns exit code 2. Resolve `OPENAI_API_KEY`, `OPENAI_MODEL` and `OPENAI_BASE_URL` through process-over-dotenv lookup. Keep the existing built-in base URL default and never print key values.

- [ ] **Step 4: Add the safe template and verify**

Add `.env.example` with exactly empty API key material and DeepSeek-compatible example defaults:

```dotenv
OPENAI_API_KEY=
OPENAI_BASE_URL=https://api.deepseek.com
OPENAI_MODEL=deepseek-v4-flash
```

Keep `/.env` ignored and add `/.env.*` only if the example is explicitly re-included with `!.env.example`; do not make real secrets trackable.

Run: `gofmt -w internal/config/*.go internal/app/*.go; go test ./internal/config ./internal/app -count=1; git diff --check`.

- [ ] **Step 5: Commit**

```powershell
git add internal/config internal/app/app.go internal/app/app_test.go .gitignore .env.example
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：支持项目本地环境配置"
```

---

### Task 2: Enforce native read_file-only Agent responses

**Files:**

- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**

- Keep `agent.Run` signature unchanged.
- Add a stable first-turn system message whose content states that Drift is read-only, only the supplied native `read_file` tool is allowed, `run_command`/shell/exec are unavailable, and XML/DSML/pseudo-tool syntax must never be emitted.
- Add an internal incompatibility error containing `模型返回了不兼容的伪工具调用格式` when first-turn text contains `<｜｜DSML｜｜` or `<|DSML|>`.

- [ ] **Step 1: Write failing Agent tests**

Update existing request assertions to include the system message. Add a test where the first stream contains `<｜｜DSML｜｜ calls>` and completes with `stop`; assert `Run` returns the incompatibility error, emits no text, and makes only one request. Add a test that inspects the first system message and asserts it names `read_file` while forbidding `run_command` and DSML/pseudo-tool syntax.

- [ ] **Step 2: Run focused tests and verify they fail**

Run: `go test ./internal/agent -run 'TestRun(RejectsDSMLText|UsesNativeToolSystemInstruction|DirectStopBuffersFirstTurnText|ReadRoundTrip)$' -count=1`

Expected: fail because no system message or DSML guard exists.

- [ ] **Step 3: Implement the smallest guard**

Prepend the system message only to the first request. Continue buffering first-turn text. Before accepting a zero-tool `stop`, detect only the two known DSML markers and return the incompatibility error; do not scan ordinary text for generic words such as `run_command`. Keep all native multi-read behavior and stdout rules unchanged.

- [ ] **Step 4: Verify and commit**

Run: `gofmt -w internal/agent/agent.go internal/agent/agent_test.go; go test ./internal/agent -count=1; go vet ./internal/agent; git diff --check`.

```powershell
git add internal/agent/agent.go internal/agent/agent_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "修复：拒绝伪工具调用输出"
```

---

### Task 3: Synchronize current scope documentation and perform acceptance

**Files:**

- Modify: `README.md`
- Modify: `spec/current.md`
- Create: `doc/m0.2.2-env-and-native-tools.md`

Do not overwrite or rename historical stage records such as `getting-started.md`, `m0.2-read-agent.md`, or `m0.2.1-read-agent.md`.

- [ ] **Step 1: Document configuration and the observed DSML symptom**

Explain `.env` setup with `Copy-Item .env.example .env`, precedence, secret handling, and the fact that `run_command` is not currently available. Explain that `<｜｜DSML｜｜ calls>` is model-emitted text rather than a native OpenAI `tool_calls` event, so Drift reports an incompatibility instead of executing it. Preserve the read-only four-file limits and two-request flow.

- [ ] **Step 2: Verify all automated gates**

Run:

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

Also run a local CLI check with a temporary `.env` and an `httptest`/test provider if available; do not claim a real DeepSeek call when `OPENAI_API_KEY` is unset. Confirm no test output contains an API key.

- [ ] **Step 3: Commit**

```powershell
git add README.md spec/current.md doc/m0.2.2-env-and-native-tools.md
git -c user.name=island -c user.email=island0920@163.com commit -m "文档：补充环境配置与工具兼容说明"
```

## Acceptance Checklist

- [ ] `.env` is optional, ignored, never logged, and `.env.example` is tracked with no secret.
- [ ] Process environment overrides `.env`; flags override both.
- [ ] First request includes the native-only system instruction and `read_file` schema.
- [ ] DSML marker text fails clearly without being printed or executed.
- [ ] No `run_command` tool is added; read-only limits remain unchanged.
- [ ] `go test ./...`, `go vet ./...`, `go build ./cmd/drift` and `git diff --check` pass.
