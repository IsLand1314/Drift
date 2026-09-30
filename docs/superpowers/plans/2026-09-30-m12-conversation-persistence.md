# M1.2 Conversation Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `drift chat` 默认保存可恢复的本地完整上下文，并提供恢复、临时模式和安全的会话管理命令。

**Architecture:** 新建 `internal/conversation`，以 workspace 内 `.drift/conversations/<id>.json` 保存单一、原子替换的当前消息快照；它与既有的脱敏 `internal/session` 审计严格分离。`agent.Runner` 提供消息快照/恢复 API，`internal/app` 负责 CLI 参数、chat 生命周期和元数据查看，不让完整消息正文进入 list/show 或审计输出。

**Tech Stack:** Go 标准库、现有 `internal/agent`、`internal/app`、`internal/llm`、`internal/session`；不新增依赖。

**Spec:** `docs/superpowers/specs/2026-09-30-m12-conversation-persistence-design.md`

## Global Constraints

- 默认 `drift chat` 创建完整本地会话；`--no-session` 不写完整快照，但继续写既有脱敏审计。
- `.drift/sessions/` 只做脱敏审计，永远不能作为恢复来源。
- `.drift/conversations/` 可含提示词、回答和工具结果，必须 Git 忽略、仅本机使用、创建目录/文件时请求 `0700`/`0600` 权限。
- 恢复只允许当前 workspace；不记录 workspace 绝对路径、Provider URL、模型名、API Key 或 Authorization 配置字段。
- `/clear` 在持久会话中必须先保存空快照，写入成功后才清空 Runner；临时模式不写完整快照。
- 继续保留 M1.1 的 1 MiB 上下文限制、只读工具边界、JSONL 脱敏审计、`/clear` 可见确认和现有退出码语义。
- 不实现分支、fork、树状历史、自动摘要、加密、云同步、跨 workspace 恢复、正文搜索或导出。

---

### Task 1: Runner 消息快照与恢复 API

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**
- Add `func NewRunnerWithMessages(client llm.Client, root, focus string, registry tool.Registry, messages []llm.Message) *Runner`.
- Add `func (r *Runner) Messages() []llm.Message`.
- Both APIs must deep-copy `[]llm.Message` and every nested `ToolCalls` slice.

- [ ] **Step 1: Write failing snapshot isolation tests**

Add tests proving the constructor copies supplied messages and `Messages()` returns a copy:

```go
func TestRunnerRestoresCopiedMessages(t *testing.T) {
	original := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read_file"}}}}
	runner := NewRunnerWithMessages(nil, t.TempDir(), "", tool.NewDefaultRegistry(), original)
	original[0].ToolCalls[0].Name = "mutated"
	if got := runner.Messages()[0].ToolCalls[0].Name; got != "read_file" {
		t.Fatalf("restored tool name = %q", got)
	}
}
```

Add a second test that mutates the slice returned by `Messages()` and proves a later `Messages()` call retains the original content.

- [ ] **Step 2: Run test to verify RED**

Run: `go test ./internal/agent -run 'TestRunner(RestoresCopiedMessages|MessagesReturnsCopy)' -count=1 -v`

Expected: FAIL because `NewRunnerWithMessages` and `Messages` do not exist.

- [ ] **Step 3: Write minimal implementation**

In `agent.go`, add:

```go
func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	copy(cloned, messages)
	for i := range cloned {
		cloned[i].ToolCalls = append([]llm.ToolCall(nil), messages[i].ToolCalls...)
	}
	return cloned
}

func NewRunnerWithMessages(client llm.Client, root, focus string, registry tool.Registry, messages []llm.Message) *Runner {
	runner := NewRunner(client, root, focus, registry)
	runner.messages = cloneMessages(messages)
	return runner
}

func (r *Runner) Messages() []llm.Message { return cloneMessages(r.messages) }
```

- [ ] **Step 4: Run Agent regression tests**

Run: `go test ./internal/agent -count=1`

Expected: PASS; M1.1 context accounting and `/clear` behavior remain unchanged.

- [ ] **Step 5: Commit**

```powershell
git add internal/agent/agent.go internal/agent/agent_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "基础：支持对话消息快照恢复" -m "参与人：island"
```

### Task 2: 完整会话快照存储

**Files:**
- Create: `internal/conversation/store.go`
- Create: `internal/conversation/store_test.go`

**Interfaces:**
- Add `type Snapshot struct { Version int; ID string; CreatedAt time.Time; UpdatedAt time.Time; Focus string; ContextBytes int; Messages []llm.Message }`.
- Add `type Metadata struct { Version int; ID string; CreatedAt time.Time; UpdatedAt time.Time; Focus string; MessageCount int; ContextBytes int }`.
- Add `func NewStore(workspace string) *Store` and methods `Create(focus string) (Snapshot, error)`, `Save(snapshot Snapshot) error`, `Load(id string) (Snapshot, error)`, `Latest() (Snapshot, error)`, `List() ([]Metadata, error)`, `Delete(id string) error`.
- Export `ErrNotFound`, `ErrInvalidID`, and `ErrInvalidSnapshot`.

- [ ] **Step 1: Write failing Store tests**

Create `store_test.go` and start with an end-to-end save/load test:

```go
func TestStoreSaveAndLoadPreservesToolMessages(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("README.md")
	if err != nil { t.Fatal(err) }
	snapshot.Messages = []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		{Role: "tool", ToolCallID: "call-1", Content: "# Drift"},
	}
	if err := store.Save(snapshot); err != nil { t.Fatal(err) }
	loaded, err := store.Load(snapshot.ID)
	if err != nil || loaded.Messages[1].Content != "# Drift" { t.Fatalf("loaded=%+v err=%v", loaded, err) }
}
```

Add separate tests for invalid IDs (`../x`, `x/y`, blank), malformed JSON returning `ErrInvalidSnapshot`, latest ordering by `UpdatedAt`, metadata not containing message content, and exact-ID deletion.

- [ ] **Step 2: Run test to verify RED**

Run: `go test ./internal/conversation -count=1 -v`

Expected: FAIL because package `internal/conversation` does not exist.

- [ ] **Step 3: Write minimal storage implementation**

Implement `Store` around `filepath.Join(workspace, ".drift", "conversations")` with these exact rules:

```go
var idPattern = regexp.MustCompile(`^conv-[a-z0-9-]{8,128}$`)

func (s *Store) pathFor(id string) (string, error) {
	if !idPattern.MatchString(id) { return "", ErrInvalidID }
	return filepath.Join(s.root, id+".json"), nil
}
```

- Generate IDs from `crypto/rand` bytes encoded as lowercase hexadecimal and prefixed `conv-`.
- Create the directory with `os.MkdirAll(dir, 0o700)` and new files with `0o600`.
- Marshal a persistence-only JSON representation with lower-case field names; never add Provider configuration fields.
- Write to `os.CreateTemp(dir, ".snapshot-*")`, `Chmod(0o600)`, encode, `Sync`, close, then `os.Rename` to the validated final path. Remove temporary files on every error path.
- Reject symlinks, non-regular files, unknown versions, invalid IDs, absolute/`..` focus paths, and malformed message data.
- `List` returns only metadata, sorted by `UpdatedAt` descending then ID descending; a missing conversations directory yields an empty list.

- [ ] **Step 4: Run test to verify GREEN**

Run: `go test ./internal/conversation -count=1 -v`

Expected: PASS; no test reads credentials or prints message bodies.

- [ ] **Step 5: Commit**

```powershell
git add internal/conversation/store.go internal/conversation/store_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：新增本地对话快照存储" -m "参与人：island"
```

### Task 3: 完整会话管理命令

**Files:**
- Create: `internal/app/conversation_command.go`
- Create: `internal/app/conversation_command_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Add `func runConversationCommand(args []string, out, stderr io.Writer) int`.
- Add top-level CLI dispatch: `drift conversation list|show <id>|delete <id> --yes`.
- Commands use `conversation.NewStore(currentWorkingDirectory)` and do not load `.env` or create a Provider.

- [ ] **Step 1: Write failing command tests**

Use actual temporary conversation files and switch to the temporary workspace in each test:

```go
func TestConversationShowDoesNotPrintMessageBodies(t *testing.T) {
	// Create a snapshot whose user message is "secret prompt".
	// Run `conversation show <id>`.
	// Assert code == 0, output has id/version/message count, and lacks "secret prompt".
}
```

Also cover empty `list` (`暂无完整会话`), sorted metadata, missing show ID (exit 2), delete without `--yes` (exit 2 and not deleted), exact delete with `--yes`, and no API-key requirement.

- [ ] **Step 2: Run test to verify RED**

Run: `go test ./internal/app -run TestConversation -count=1 -v`

Expected: FAIL because `conversation` is not dispatched by `RunWithInput`.

- [ ] **Step 3: Write minimal command implementation**

In `app.go`, dispatch before `.env` loading:

```go
if len(args) > 0 && args[0] == "conversation" {
	return runConversationCommand(args[1:], out, stderr)
}
```

Implement exactly `list`, `show <id>`, and `delete <id> --yes`. `show` may print only `id`、`version`、`created_at`、`updated_at`、`focus`、`messages`、`context_bytes`; it must never print message content, tool arguments, results, or model answers.

- [ ] **Step 4: Run App regression tests**

Run: `go test ./internal/app -run 'TestConversation|TestSession' -count=1 -v`

Expected: PASS; existing `session list/show` behavior is unchanged.

- [ ] **Step 5: Commit**

```powershell
git add internal/app/app.go internal/app/conversation_command.go internal/app/conversation_command_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：提供完整会话管理命令" -m "参与人：island"
```

### Task 4: Chat 生命周期、恢复与临时模式

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`
- Modify: `internal/app/app_test.go`

**Interfaces:**
- Add internal `chatPersistence` holding `*conversation.Store`, current `conversation.Snapshot`, and `persistent bool`; `saveRunner` updates both `Snapshot.Messages` and `Snapshot.ContextBytes` from the Runner.
- Add `func (p *chatPersistence) saveRunner(runner *agent.Runner) error`.
- Add `func (p *chatPersistence) clearRunner(runner *agent.Runner) error`.
- Add unexported parsing for `--no-session`, `--resume`, `--resume <id>`, and `--resume=<id>` before the existing `flag.FlagSet` parses other chat flags.

- [ ] **Step 1: Write failing end-to-end chat tests**

Add local `httptest.Server` Provider tests. The main restoration test must use two independent `RunWithInput` calls:

```go
func TestChatResumeSendsPriorContext(t *testing.T) {
	// First call: `chat -w root`, input "first\nexit\n"; server returns "first answer".
	// Read the created conversation ID from root/.drift/conversations.
	// Second call: `chat --resume <id> -w root`, input "second\nexit\n".
	// Assert the second Provider request includes first user, first assistant, and second user.
}
```

Add independent tests that prove default chat creates one full snapshot and prints a local-full-context warning; `--no-session` creates no snapshot but still creates one audit JSONL; persisted `first → /clear → exit` resumes without `first`; bare `--resume` selects latest; malformed or conflicting flags return 2 before Provider requests; and a save failure reports the exact persistence warning but keeps the process usable.

- [ ] **Step 2: Run test to verify RED**

Run: `go test ./internal/app -run 'TestChat(Resume|NoSession|PersistentClear|Persistence)' -count=1 -v`

Expected: FAIL because current chat neither creates nor restores full conversation snapshots.

- [ ] **Step 3: Write minimal parser and startup implementation**

Implement an unexported parser that removes only these forms before invoking the existing `flag.FlagSet`:

```text
--no-session
--resume
--resume <id>
--resume=<id>
```

Reject duplicate `--resume`, empty `--resume=`, and `--resume` combined with `--no-session`. After workspace resolution: normal chat creates a Store and empty snapshot before any Provider request; `--no-session` skips it; bare `--resume` calls `Store.Latest()`; ID resume calls `Store.Load(id)`; restored chats use `agent.NewRunnerWithMessages`; every create/load validation error returns 2 before Provider use; default startup prints the ID and `注意：此会话会保存完整本地上下文，可能包含用户输入和读取结果；使用 --no-session 可关闭`.

- [ ] **Step 4: Persist completed turns and clear safely**

Modify `runChatLoop` to accept optional `*chatPersistence`. After only a successful `runner.RunEvents`, call `saveRunner`; on save failure print `错误：会话保存失败，本次上下文只保留在当前进程` and continue the current chat.

For persistent `/clear`, implement this sequence:

```go
if persistence != nil {
	if err := persistence.clearRunner(runner); err != nil {
		fmt.Fprintln(stderr, "错误：会话保存失败，未清空当前对话上下文")
		continue
	}
} else {
	runner.ResetContext()
}
fmt.Fprintln(out, "已清空当前对话上下文")
```

`clearRunner` must save an empty snapshot first and call `runner.ResetContext()` only after save succeeds. It must not append an Agent Event, call Provider, or change JSONL audit behavior.

- [ ] **Step 5: Run test to verify GREEN**

Run: `go test ./internal/app -count=1 -v`

Expected: PASS; M1.0 multi-turn, M1.1 `/clear`, EOF, cancellation, trace, and audit tests remain green.

- [ ] **Step 6: Commit**

```powershell
git add internal/app/app.go internal/app/chat.go internal/app/chat_test.go internal/app/app_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：支持对话保存与恢复" -m "参与人：island"
```

### Task 5: 规格、文档与最终验收

**Files:**
- Create: `doc/m1.2-conversation-persistence.md`
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/Process/代码理解.md`
- Modify: `docs/superpowers/plans/2026-09-30-m12-conversation-persistence.md`

**Interfaces:**
- `spec/current.md` becomes the M1.2 current acceptance authority.
- Documentation must state that conversation snapshots can contain sensitive content and that `session` and `conversation` are separate command groups.

- [ ] **Step 1: Write documentation and acceptance table**

Create `doc/m1.2-conversation-persistence.md` with these runnable examples:

```powershell
go run ./cmd/drift chat -w .
go run ./cmd/drift chat --resume -w .
go run ./cmd/drift chat --no-session -w .
go run ./cmd/drift conversation list
go run ./cmd/drift conversation delete <id> --yes
```

Update `spec/current.md` to M1.2 and add AC-M12-001 through AC-M12-007. Every row must include verification method, pass threshold, evidence type, evidence path, and failure condition. Update README and `doc/Process/代码理解.md` with the create → snapshot → resume flow. Do not describe complete snapshots as redacted or safe to share.

- [ ] **Step 2: Run documentation checks**

Run: `git diff --check`

Expected: PASS; all local Markdown links and command grammar match the implementation.

- [ ] **Step 3: Perform manual acceptance without exposing a key**

Use `.env` or existing process environment and never print the key. Build the executable, start a persistent chat, ask one short question, exit, then copy its printed conversation ID:

```powershell
go build -o .codex-temp\drift-m12.exe ./cmd/drift
& .\.codex-temp\drift-m12.exe chat -w .
& .\.codex-temp\drift-m12.exe conversation list
& .\.codex-temp\drift-m12.exe chat --resume <id> -w .
```

Verify `/clear` removes a temporary secret from the resumed conversation, `conversation show <id>` never prints that secret, `conversation delete <id> --yes` removes only that snapshot, and `.drift/sessions/` remains redacted.

- [ ] **Step 4: Run full verification**

Run: `go test ./... -count=1`; then `go vet ./...`; then `go build -o .codex-temp\drift-m12.exe ./cmd/drift`; then `git diff --check`.

Expected: all four commands exit 0.

- [ ] **Step 5: Commit**

```powershell
git add README.md spec/current.md doc/m1.2-conversation-persistence.md doc/Process/代码理解.md docs/superpowers/plans/2026-09-30-m12-conversation-persistence.md
git -c user.name=island -c user.email=island0920@163.com commit -m "文档：补充 M1.2 对话持久化说明" -m "参与人：island"
```
