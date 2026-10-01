# M3.3 安全文件编辑与变更集合 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task with TDD checkpoints.

**Goal:** 在 `drift chat` 中提供可审批的目录创建、精确编辑、文件删除和每回合聚合 change set，同时保持 `-p` 只读。

**Architecture:** 扩展现有 Previewable 工具契约，让 `write_file`、`edit_file`、`delete_file` 统一先预检再由 app 审批；把 `.drift/changes` 从单操作目录升级为回合级 ChangeSet，工具只追加文件操作条目。TTY 固定底栏作为独立后续 UI 改造，不与文件安全核心混写。

**Tech Stack:** Go 1.26+、标准库 `os.Root`/`filepath`/`encoding/json`，现有 Bubble Tea/Lip Gloss 审批组件，Go `testing`。

**Spec:** `doc/m3.3-safe-file-edit.md`

## Global Constraints

- `-p` 继续只注册三个只读工具；写入工具只存在于 `chat`。
- 所有目标必须是 workspace 内相对路径；拒绝绝对路径、`..`、符号链接逃逸及受保护目录。
- 文件写入只接受 UTF-8 文本；单文件最大 `128 KiB`。
- 审批前不得修改项目文件；拒绝、取消和预检失败不得产生项目副作用。
- session 保存完整上下文，audit 保存脱敏事件，changes 只保存本地变更过程文件。

---

### Task 1: 统一 ChangeSet 存储接口

**Files:**
- Modify: `internal/changes/manifest.go`
- Test: `internal/changes/manifest_test.go`
- Modify: `internal/layout/layout.go` only if an existing date-root helper needs reuse

**Interfaces:**
- Produce `changes.ChangeSet` with `Begin(root, started)`, `RecordFile(operation)`, `Finalize(status)` and `Path()`; preserve a compatibility wrapper for the existing single-write tests until tools migrate.

- [ ] **Step 1: Write failing tests** for one change-set directory containing multiple file entries, one aggregated patch, and `partial` final status.
- [ ] **Step 2: Run** `go test ./internal/changes -run 'ChangeSet|Record' -count=1`; expect failures because the aggregate API is absent.
- [ ] **Step 3: Implement** the smallest append-only manifest structure; each file entry contains operation, relative path, old/new bytes, decision and optional error, while `work/` stores per-file snapshots.
- [ ] **Step 4: Run** the focused tests and then `go test ./internal/changes -count=1`.
- [ ] **Step 5: Commit** `git commit -m "重构变更记录为回合级 change set"`.

### Task 2: Extend `write_file` for parent directory creation

**Files:**
- Modify: `internal/tool/write.go`
- Test: `internal/tool/write_test.go`

**Interfaces:**
- Keep `Write`/`CommitWrite` preview semantics; add preview metadata for missing parent directories and a commit path that creates only checked, non-symlink directories.

- [ ] **Step 1: Add failing tests** for nested missing parents, existing symlink parent rejection, protected directory rejection, and atomic write after approval.
- [ ] **Step 2: Run** `go test ./internal/tool -run 'Write.*Parent|Write.*Symlink' -count=1`; expect failure because missing parents are still rejected.
- [ ] **Step 3: Implement**逐级 `Mkdir`/`MkdirAll` equivalent using `os.Root`, checking each component before creation; do not create the root or protected components.
- [ ] **Step 4: Run** all `internal/tool` tests and inspect that rejected previews leave no directories behind.
- [ ] **Step 5: Commit** `git commit -m "允许受控写入创建缺失父目录"`.

### Task 3: Add exact-match `edit_file`

**Files:**
- Create: `internal/tool/edit.go`
- Test: `internal/tool/edit_test.go`
- Modify: `internal/tool/registry.go`

**Interfaces:**
- Add `EditDefinition`, `Edit(root, raw) (Preview, error)`, and `CommitEdit(ctx, root, preview)` using `path`, `old_text`, `new_text`.

- [ ] **Step 1: Write failing tests** for one match, zero matches, multiple matches, binary target, path traversal, and stale target content between preview and commit.
- [ ] **Step 2: Run** `go test ./internal/tool -run TestEdit -count=1`; expect failure because the tool does not exist.
- [ ] **Step 3: Implement** exact `strings.Count == 1`, UTF-8/NUL validation, re-read-before-commit, atomic temp replacement, and diff generation through existing helpers.
- [ ] **Step 4: Register** the tool only in the chat write registry; verify default read registry remains unchanged.
- [ ] **Step 5: Run** focused and full tool tests; commit `git commit -m "增加唯一匹配文件编辑工具"`.

### Task 4: Add approved `delete_file`

**Files:**
- Create: `internal/tool/delete.go`
- Test: `internal/tool/delete_test.go`
- Modify: `internal/tool/registry.go`

**Interfaces:**
- Add `DeleteDefinition`, `Delete(root, raw) (Preview, error)`, and `CommitDelete(ctx, root, preview)`; preview stores deleted bytes in the current ChangeSet work snapshot.

- [ ] **Step 1: Write failing tests** for approved deletion, denial/no commit, missing target, directory rejection, symlink rejection, protected path rejection, and cancellation.
- [ ] **Step 2: Run** `go test ./internal/tool -run TestDelete -count=1`; expect failure.
- [ ] **Step 3: Implement** regular-file-only validation and atomic/recoverable delete sequence; never recursively remove directories.
- [ ] **Step 4: Register** only in chat and run tool tests.
- [ ] **Step 5: Commit** `git commit -m "增加审批式文件删除工具"`.

### Task 5: Route all mutation tools through one permission and ChangeSet path

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/app/chat.go`
- Modify: `internal/app/permission_memory.go`
- Modify: `internal/app/approval_input.go`
- Test: `internal/app/permission_memory_test.go`, `internal/app/approval_input_test.go`, `internal/app/chat_test.go`

**Interfaces:**
- Generalize `agent.PermissionRequest` and preview execution for `write_file`, `edit_file`, and `delete_file`; pass one ChangeSet per user turn and append file operations to it.

- [ ] **Step 1: Add failing tests** for all three tools using the same selector, exact-path allow memory, per-file decisions, and one aggregated change directory.
- [ ] **Step 2: Run** focused app tests; expect failures because approval text and persistence are write-only.
- [ ] **Step 3: Implement** tool-neutral labels, operation-specific summary bytes, current-session exact-path memory, and `partial` finalization on cancellation/error.
- [ ] **Step 4: Add a system instruction** telling the model to call native mutation tools for explicit create/edit/delete requests and never claim success without a successful tool result.
- [ ] **Step 5: Run** `go test ./internal/agent ./internal/app -count=1`; commit `git commit -m "统一文件变更审批与回合记录"`.

### Task 6: Fixed-bottom TTY chat layout

**Files:**
- Modify: `internal/app/chat_input.go`
- Create or modify: `internal/app/chat_tui.go`
- Test: `internal/app/chat_input_test.go`, `internal/app/chat_tui_test.go`
- Modify: `doc/Process/面板视觉设计.md`

**Interfaces:**
- Replace the per-turn input program with one long-lived Bubble Tea model containing viewport, input, footer and model label; non-TTY scanner path remains unchanged.

- [ ] **Step 1: Write failing layout tests** proving footer remains the final rows after tool/assistant content and resizes with terminal height.
- [ ] **Step 2: Run** focused TTY tests; expect failure because `ttyChatInput.Read` exits after each submission.
- [ ] **Step 3: Implement** fixed layout with viewport auto-scroll, bottom footer, approval overlay handoff and clean redraw on spinner/tool output.
- [ ] **Step 4: Verify** ANSI is absent in scanner mode and fixed footer is visible in a PTY mock run.
- [ ] **Step 5: Commit** `git commit -m "增加固定底栏聊天布局"`.

### Task 7: Documentation and acceptance evidence

**Files:**
- Modify: `spec/current.md` to make M3.3 current only after implementation is complete
- Modify: `README.md`, `doc/m3.1-safe-file-write.md`, `doc/m3.2-approval-tui.md`
- Create: `artifacts/verification/m3.3/full-check.txt`
- Create: `artifacts/verification/m3.3/manual-acceptance.txt`

- [ ] **Step 1:** Document the final tool set, change-set layout, limits, and explicit non-goal of command execution.
- [ ] **Step 2:** Run `go test ./... -count=1`, `go vet ./...`, `go build -o .codex-temp\drift-m33.exe ./cmd/drift`, and `git diff --check`; capture exact outputs.
- [ ] **Step 3:** Run `go run ./cmd/drift chat -w .` against a local OpenAI-compatible mock and verify nested creation, edit, delete approval/denial, multiple files in one change set, cancellation, and fixed footer.
- [ ] **Step 4:** Review paths for secrets, absolute paths, `.drift`, and `tmp` pollution; remove disposable mock files.
- [ ] **Step 5:** Commit docs/evidence and merge only after all checks pass.
