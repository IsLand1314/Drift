# M3.6 Permission Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist exact workspace-scoped approval patterns while keeping `ask` as the safe default and preserving existing TUI approval behavior.

**Architecture:** Add a small `internal/app` permission policy store backed by `.drift/permissions.json`. The chat loop loads it once, checks it after process-local memory, and persists a rule only after an approved operation succeeds. Existing tool safety validation remains authoritative; policy matches never bypass it.

**Tech Stack:** Go standard library (`encoding/json`, `os`, `path/filepath`, `sync`, `time`), existing Bubble Tea TUI, existing `agent.PermissionRequest` and approval callback.

**Spec:** `docs/superpowers/specs/2026-10-02-m36-permission-persistence-design.md`

## Global Constraints

- Workspace scope only; no user-global or cross-workspace rules.
- Exact matching only; no glob, prefix, regex, or `allow all`.
- Missing, corrupt, unknown-version, or unreadable policy fails closed to `ask`.
- Policy files use `.drift` directory permissions and atomic `0600` replacement.
- Do not store file contents, command output, answer text, or secrets in policy or audit records.
- Existing path, symlink, hidden-directory, and special-file validation remains unchanged.
- Every implementation task follows RED → GREEN → focused regression test.

---

### Task 1: Add the policy store and exact rule model

**Files:**
- Create: `internal/app/permission_policy.go`
- Test: `internal/app/permission_policy_test.go`

**Interfaces:**
- `permissionPolicy.Load(root string) (*permissionPolicy, error)` loads `.drift/permissions.json` and treats a missing file as an empty policy.
- `(*permissionPolicy).Allows(agent.PermissionRequest) bool` performs exact matching.
- `(*permissionPolicy).Remember(agent.PermissionRequest) error` appends a normalized rule and atomically saves it.
- `(*permissionPolicy).Clear() error` saves an empty rule set.
- `(*permissionPolicy).Summary() []string` returns redacted relative-path or command/cwd summaries.

- [ ] **Step 1: Write failing tests** for missing-file empty policy, exact file match, exact command/cwd match, mismatched fields, duplicate rule de-duplication, corrupt/unknown-version fail-closed load, atomic save permissions, and clear.
- [ ] **Step 2: Run focused tests and verify the expected failures.**

Run: `go test ./internal/app -run PermissionPolicy -count=1`

Expected: FAIL because the policy type and methods do not exist.

- [ ] **Step 3: Implement the minimal JSON model and store.**

Use a versioned document with `version` and `rules`; normalize path/cwd to workspace-relative slash form; serialize through a same-directory temporary file with mode `0600`, then rename; protect in-process saves with one mutex.

- [ ] **Step 4: Run focused tests and verify GREEN.**

Run: `go test ./internal/app -run PermissionPolicy -count=1`

Expected: PASS with no policy rule allowing a non-exact request.

- [ ] **Step 5: Commit.**

```bash
git add internal/app/permission_policy.go internal/app/permission_policy_test.go
git commit -m "增加工作区权限策略存储"
```

### Task 2: Load policy and check it in the chat approval path

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_tui.go`
- Modify: `internal/app/permission_memory.go`
- Test: `internal/app/permission_policy_test.go`
- Test: `internal/app/chat_test.go`

**Interfaces:**
- Chat startup creates one policy object for the resolved workspace.
- TTY and scanner approval callbacks check process memory first, then persistent policy.
- A persistent hit returns `Allow=true` with reason `persistent_pattern_approved`.

- [ ] **Step 1: Write failing tests** for persistent approval bypassing the prompt after restart-equivalent policy reload, and for a changed path/command still invoking approval.
- [ ] **Step 2: Run the focused tests and verify RED.**

Run: `go test ./internal/app -run 'Persistent|Permission' -count=1`

Expected: FAIL because approval callbacks only know process-local memory.

- [ ] **Step 3: Add the policy lookup after `permissionMemory.Allow`.**

Keep the existing current-chat memory semantics and do not alter read-only tool behavior or preview validation.

- [ ] **Step 4: Run focused tests and verify GREEN.**

Run: `go test ./internal/app -run 'Persistent|Permission' -count=1`

Expected: PASS; exact same request bypasses approval, any changed match field does not.

- [ ] **Step 5: Commit.**

```bash
git add internal/app/chat.go internal/app/chat_tui.go internal/app/permission_memory.go internal/app/chat_test.go internal/app/permission_policy_test.go
git commit -m "接入工作区持久化权限匹配"
```

### Task 3: Persist only after successful remembered approval

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_tui.go`
- Modify: `internal/app/approval_input.go`
- Test: `internal/app/chat_test.go`
- Test: `internal/app/chat_tui_test.go`

**Interfaces:**
- Approval choice 1 returns `user_approved` and never writes policy.
- Approval choice 2 marks the request for persistence, but the policy write occurs only after the associated tool result is successful.
- Failed, denied, or cancelled operations do not create a rule; a policy save failure reports a safe warning and does not retroactively claim persistence.

- [ ] **Step 1: Write failing tests** for first-choice non-persistence, second-choice success persistence, second-choice failed-operation non-persistence, and save failure fallback.
- [ ] **Step 2: Run focused tests and verify RED.**

Run: `go test ./internal/app -run 'Remember|Persist|Approval' -count=1`

Expected: FAIL because the current decision callback has no post-success persistence hook.

- [ ] **Step 3: Implement a minimal pending-remember marker tied to the tool call ID.**

Consume the marker from the successful tool-result path only; do not persist on permission request, because the tool may still fail or be cancelled.

- [ ] **Step 4: Run focused tests and verify GREEN.**

Run: `go test ./internal/app -run 'Remember|Persist|Approval' -count=1`

Expected: PASS; failed and cancelled calls leave the policy unchanged.

- [ ] **Step 5: Commit.**

```bash
git add internal/app/chat.go internal/app/chat_tui.go internal/app/approval_input.go internal/app/chat_test.go internal/app/chat_tui_test.go
git commit -m "仅在操作成功后持久化授权"
```

### Task 4: Add `/permissions` inspection and clearing

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_tui.go`
- Test: `internal/app/chat_test.go`
- Test: `internal/app/chat_tui_test.go`

**Interfaces:**
- `/permissions` prints count and redacted summaries.
- `/permissions clear` atomically clears persisted rules and leaves current process memory unchanged.
- Failures are shown as short safe messages and do not delete the old policy.

- [ ] **Step 1: Write failing tests** for summary output, clear behavior, and the fact that clear does not alter current-chat memory.
- [ ] **Step 2: Run focused tests and verify RED.**

Run: `go test ./internal/app -run 'PermissionsCommand|PermissionClear' -count=1`

Expected: FAIL because the commands are unknown.

- [ ] **Step 3: Add the two command branches to both scanner and TTY command handling.**
- [ ] **Step 4: Run focused tests and verify GREEN.**

Run: `go test ./internal/app -run 'PermissionsCommand|PermissionClear' -count=1`

Expected: PASS with no secret, file-content, or command-output leakage.

- [ ] **Step 5: Commit.**

```bash
git add internal/app/chat.go internal/app/chat_tui.go internal/app/chat_test.go internal/app/chat_tui_test.go
git commit -m "增加权限策略查看和清除命令"
```

### Task 5: Update stage specification and run full acceptance

**Files:**
- Modify: `spec/current.md`
- Create: `doc/m3.6-permission-persistence.md`
- Create: `artifacts/verification/m3.6/permission-policy-tests.txt`
- Create: `artifacts/verification/m3.6/manual-acceptance.md`
- Create: `artifacts/verification/m3.6/final-check.txt`
- Create: `artifacts/verification/m3.6/manifest.json`

- [ ] **Step 1: Run all focused package tests.**

```bash
go test ./internal/app -run 'Permission|Approval|Chat' -count=1
go test ./internal/agent -count=1
go test ./internal/tool -count=1
```

- [ ] **Step 2: Run full verification.**

```bash
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

- [ ] **Step 3: Perform manual acceptance in a disposable workspace.**

Verify first-choice approval does not survive restart; second-choice approval survives restart only for the exact same tool/operation/path or command/cwd; changed fields prompt again; `/permissions` is redacted; `/permissions clear` removes the rule; malformed policy falls back to approval; failed operations do not persist rules.

- [ ] **Step 4: Record hashes and evidence manifest.**

The manifest must list each evidence file with SHA256 and byte size; no API keys, file contents, command output, or answer bodies may appear in evidence.

- [ ] **Step 5: Update `spec/current.md` to M3.6 only after all acceptance criteria pass.**
- [ ] **Step 6: Commit and push the completed stage.**

```bash
git add spec/current.md doc/m3.6-permission-persistence.md artifacts/verification/m3.6
git commit -m "完成 M3.6 权限策略持久化"
git push origin master
```
