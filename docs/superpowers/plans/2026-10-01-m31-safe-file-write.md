# M3.1 Safe File Write Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a confirmed `write_file` tool that can create or overwrite files inside the workspace while recording local change artifacts under `.drift/changes/`.

**Architecture:** Keep `-p` read-only by registering the existing read-only registry only for single-run mode. `chat` receives a registry containing `write_file` and a permission callback. The tool validates and previews a write; the app asks the user; only an approved operation atomically replaces the target and records a local manifest/diff. Existing `sessions/` and `audits/` responsibilities remain unchanged.

**Tech Stack:** Go 1.26+, standard library, existing `internal/tool`, `internal/agent`, `internal/app`, `internal/session`, and Bubble Tea terminal input.

**Spec:** `docs/superpowers/specs/2026-10-01-m31-safe-file-write-design.md`

## Global Constraints

- `drift -p` never registers `write_file` and remains non-interactive/read-only.
- `drift chat` registers `write_file` with default permission mode `ask`.
- `path` is workspace-relative; reject absolute paths, `..`, symlink escape, and protected `.git`, `.drift`, `.codex-temp`, `.worktrees` paths.
- The target parent directory must already exist; M3.1 does not create directories.
- Both new-file creation and existing-file overwrite require confirmation.
- `.drift/changes/YYYY/MM/DD/` is local process state and is not a session-recovery or audit source.
- Do not add `edit_file`, `delete_file`, `run_command`, `shell`, `exec`, OS sandbox, or Git rollback in M3.1.

---

### Task 1: Define permission and write-preview contracts

**Files:**
- Modify: `internal/agent/event.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/tool/tool.go`
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Add `agent.PermissionRequest` with `ToolName`, `Operation`, `Path`, `OldBytes`, `NewBytes`, and `Diff`.
- Add `agent.PermissionDecision` with `Allow` and `Reason`.
- Add `agent.PermissionPrompt func(context.Context, PermissionRequest) (PermissionDecision, error)`.
- Add `Runner.SetPermissionPrompt(PermissionPrompt)`; nil means deny write-capable requests rather than silently allowing them.
- Add optional `tool.Previewable` with `Preview(context.Context, string, string) (tool.Preview, error)` and `ExecutePreview(context.Context, string, string, tool.Preview) (string, error)`; read-only tools remain unchanged.

- [ ] **Step 1: Write failing contract tests**

Add tests proving a Runner with a write tool emits a permission request before execution, and that a nil/deny decision does not change the target file. Add a tool fixture that returns a preview containing create/overwrite metadata and a bounded diff.

- [ ] **Step 2: Run the focused tests and verify failure**

Run:

```powershell
go test ./internal/agent ./internal/tool -run 'Test(Permission|WritePreview)' -count=1
```

Expected: compile or assertion failures because the contracts do not exist yet.

- [ ] **Step 3: Add the minimal types and Runner hook**

Add the request/decision types and one callback field to `Runner`. Do not change read-only tools or permit an unconfigured write.

- [ ] **Step 4: Run the focused tests**

Run the same command and require PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/agent/event.go internal/agent/agent.go internal/agent/agent_test.go internal/tool/tool.go internal/tool/write_test.go
git commit -m "重构：增加工具权限确认契约"
```

### Task 2: Implement workspace-safe `write_file` and change artifacts

**Files:**
- Create: `internal/tool/write.go`
- Create: `internal/tool/write_test.go`
- Modify: `internal/layout/layout.go`
- Modify: `internal/layout/layout_test.go`

**Interfaces:**
- `tool.WritePreview` contains operation (`create_file`/`overwrite_file`), cleaned relative path, old/new byte counts, and unified diff text.
- `tool.Write(root, rawArguments string) (WritePreview, error)` validates JSON and computes a preview without changing the target.
- `tool.CommitWrite(context.Context, root string, preview WritePreview) (string, error)` writes through a same-directory temporary file and atomic rename.
- `layout.Layout.Changes` is `.drift/changes`; `layout.Layout.Prepare` creates it with mode `0700`.
- `layout.DateDir(layout.Changes, startedAt)` and `change-<timestamp>-<id>` are used for each operation.

- [ ] **Step 1: Write failing path and preview tests**

Cover: create in an existing directory, overwrite an existing regular text file, blank/unknown JSON fields, absolute path, drive-letter path, `..`, protected directory, missing parent, symlink component, directory target, binary old content, and content over the write limit. Assert preview does not change target bytes.

- [ ] **Step 2: Run the focused tests and verify failure**

```powershell
go test ./internal/tool ./internal/layout -run 'Test(Write|LayoutChanges)' -count=1
```

Expected: FAIL because `Write`, `CommitWrite`, and `Layout.Changes` are not implemented.

- [ ] **Step 3: Implement validation and preview**

Reuse the existing relative-path and symlink checks in `internal/tool`; add only protected-directory and write-specific regular-file checks. Use a fixed M3.1 content limit of `128 << 10` bytes. Generate a bounded unified diff for overwrites and a create summary for new files.

- [ ] **Step 4: Implement atomic commit and dated change directory**

Create `.drift/changes/YYYY/MM/DD/change-<timestamp>-<id>/work/`, write proposed content to a temporary sibling file, flush and close it, then rename it over the target. Never truncate the target before the temporary file is ready. Return a stable error when rename fails.

- [ ] **Step 5: Run the focused tests**

Run the same focused command and require PASS.

- [ ] **Step 6: Commit**

```powershell
git add internal/tool/write.go internal/tool/write_test.go internal/layout/layout.go internal/layout/layout_test.go
git commit -m "功能：增加 workspace 安全文件写入"
```

### Task 3: Add write-capable registry only to chat

**Files:**
- Modify: `internal/tool/registry.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**
- Keep `tool.NewDefaultRegistry()` read-only for `-p` and existing callers.
- Add `tool.NewChatRegistry()` returning the three read-only tools plus `write_file` in stable order.
- The app chooses `NewDefaultRegistry` for single mode and `NewChatRegistry` for `chat`.
- Agent dispatch detects `tool.Previewable`, calls the permission prompt, and calls `ExecutePreview` only for an allowed decision.

- [ ] **Step 1: Write failing mode and dispatch tests**

Add an app test that captures provider requests: `Run(... -p ...)` must expose exactly the three read-only schemas, while `RunWithInput(... chat ...)` must expose `write_file`. Add an Agent test proving a denied preview never calls the commit method.

- [ ] **Step 2: Run the focused tests and verify failure**

```powershell
go test ./internal/app ./internal/agent -run 'Test(.*WriteFile|.*Registry|.*Permission)' -count=1
```

Expected: FAIL because chat and single mode currently share `NewDefaultRegistry`, and dispatch has no preview branch.

- [ ] **Step 3: Implement registry split and dispatch**

Use existing registry composition. Preserve read-only behavior and error handling. Emit a permission event before waiting and a safe tool result/error after the decision; never include full content or raw arguments in the event payload.

- [ ] **Step 4: Run the focused tests**

Run the same command and require PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/tool/registry.go internal/app/app.go internal/app/app_test.go internal/agent/agent.go internal/agent/agent_test.go
git commit -m "功能：仅在 chat 启用写入工具"
```

### Task 4: Add terminal confirmation and safe change manifest

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_input.go`
- Modify: `internal/app/chat_test.go`
- Create: `internal/changes/manifest.go`
- Create: `internal/changes/manifest_test.go`
- Modify: `internal/session/jsonl.go`
- Modify: `internal/session/read.go`

**Interfaces:**
- `app.confirmWrite(ctx, input, out, request) (agent.PermissionDecision, error)` reads one explicit `y/yes` or `n/no` response and treats Ctrl+C/EOF as deny.
- `changes.Manifest` records operation ID, UTC time, relative path, operation, old/new bytes, decision, and error stage without file content.
- `changes.WriteManifest(root, startedAt, id, manifest) (string, error)` writes `.drift/changes/.../manifest.json` with mode `0600`.
- Add audit event types for permission requested/decided and write result; sanitize path, decision, byte counts, and stage only.

- [ ] **Step 1: Write failing confirmation and manifest tests**

Cover `y`, `yes`, `n`, `no`, blank input, Ctrl+C/EOF, invalid responses, manifest path/date layout, and assertions that prompt/content/API key text is absent from manifest and audit JSONL.

- [ ] **Step 2: Run focused tests and verify failure**

```powershell
go test ./internal/app ./internal/changes ./internal/session -run 'Test(Confirm|Manifest|WriteAudit)' -count=1
```

Expected: FAIL because the confirmation helper, manifest package, and write audit events do not exist.

- [ ] **Step 3: Implement confirmation and manifest writing**

Use the existing chat input stream and output writer. Print the relative target, create/overwrite operation, byte sizes, and diff; never print full new content by default. Any response other than explicit yes is deny.

- [ ] **Step 4: Wire the callback into chat**

Construct the chat Runner with the confirmation callback. In scanner mode, read the next input line for the decision. Keep `-p` without a callback because it has no write tool.

- [ ] **Step 5: Run focused tests**

Run the same command and require PASS.

- [ ] **Step 6: Commit**

```powershell
git add internal/app/chat.go internal/app/chat_input.go internal/app/chat_test.go internal/changes internal/session/jsonl.go internal/session/read.go
git commit -m "功能：增加写入确认与修改记录"
```

### Task 5: Integrate documentation and verification evidence

**Files:**
- Create: `doc/m3.1-safe-file-write.md`
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/architecture.md`
- Modify: `doc/Process/代码理解.md`
- Create: `artifacts/verification/m3.1/full-check.txt`
- Create: `artifacts/verification/m3.1/manual-acceptance.txt`

- [ ] **Step 1: Add M3.1 milestone documentation**

Document the workspace target path, `.drift/changes` process records, chat-only write registration, confirmation semantics, and explicit non-goals.

- [ ] **Step 2: Add automated verification evidence**

Run and record:

```powershell
go test ./... -count=1
go vet ./...
go build -o .codex-temp\drift-m31.exe ./cmd/drift
git diff --check
```

- [ ] **Step 3: Perform manual acceptance**

In `chat`, ask to create `tmp/m31-created.txt` after creating the `tmp` directory manually; deny once and confirm the file is absent, then approve and confirm the file exists. Ask to overwrite it, inspect the displayed diff, deny once and confirm bytes are unchanged, then approve and inspect `.drift/changes/YYYY/MM/DD/change-*/manifest.json` and `diff.patch`. Run the same prompt with `-p` and confirm no `write_file` schema is offered and no file changes.

- [ ] **Step 4: Review documentation boundaries**

Ensure README, `spec/current.md`, architecture, and process notes all say `-p` is read-only, `chat` is confirmation-gated, `.drift/changes` is not recovery/audit storage, and parent directories are not auto-created.

- [ ] **Step 5: Run full verification**

```powershell
go test ./... -count=1
go vet ./...
go build -o .codex-temp\drift-m31.exe ./cmd/drift
git diff --check
```

- [ ] **Step 6: Commit the milestone**

```powershell
git add README.md spec/current.md doc/architecture.md doc/Process/代码理解.md doc/m3.1-safe-file-write.md artifacts/verification/m3.1
git commit -m "功能：完成 M3.1 安全文件写入"
```
