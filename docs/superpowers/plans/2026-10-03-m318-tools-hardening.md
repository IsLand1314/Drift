# M3.18 工具能力复核与补缺 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在现有 M3.11/M3.12 实现之上，用 TDD 补齐 Glob、Grep 和预览状态校验的边界回归，并验证旧记录兼容性。

**Architecture:** 复用 `internal/tool/list.go`、`search.go` 和 `state.go`，只补暴露出的真实缺口。功能测试使用临时 workspace，不引入持久化缓存、新接口或新的沙箱策略。

**Tech Stack:** Go 标准库、现有 `internal/tool` 测试、JSONL session/change-set 记录。

**Spec:** `docs/superpowers/specs/2026-10-03-m318-tools-hardening-design.md`

## Global Constraints

- 不修改 Git 回滚和 `.git` 沙箱策略。
- 不全局放开 `.git` 或 `.drift`。
- 不兼容旧工具名和旧参数。
- 文件状态校验保持当前进程内存字节快照，不做持久化缓存、跨进程锁或多 Agent 协调。
- 任何实现修改都必须先有失败测试，再写最小修复。

---

### Task 1: 盘点并补齐 Glob 边界测试

**Files:**
- Modify: `internal/tool/list_test.go`
- Test: `internal/tool/list_test.go`

**Interfaces:**
- Consumes: `List(root, rawArguments string) (string, error)` and `listFilesTool.Execute`.
- Produces: regression coverage for pattern matching, protected paths, symlink handling, result cap, and cancellation.

- [ ] **Step 1: Write failing tests** for a temporary workspace containing nested `.go` files, `.git`, `.drift`, a symlink (where supported), and more than `MaxListEntries` files. Assert `**/*.go`, `internal/**/*.go`, `*.md`, protected directories, and malformed/absolute paths.
- [ ] **Step 2: Run the focused tests**

```powershell
go test ./internal/tool -run 'TestGlob' -count=1
```

Expected: any newly specified missing boundary behavior fails before implementation changes.
- [ ] **Step 3: Change only the smallest shared traversal/match guard** if a test exposes a real gap; preserve current output format and 200-entry cap.
- [ ] **Step 4: Re-run the focused tests** and confirm PASS.
- [ ] **Step 5: Commit**

```powershell
git add internal/tool/list_test.go internal/tool/list.go internal/tool/walk.go
git commit -m "test: harden Glob workspace boundaries"
```

### Task 2: 盘点并补齐 Grep 边界测试

**Files:**
- Modify: `internal/tool/search_test.go`
- Test: `internal/tool/search_test.go`

**Interfaces:**
- Consumes: `Search(root, rawArguments string) (string, error)` and context-aware execution.
- Produces: regression coverage for regex, include filtering, no-match behavior, binary skipping, limits, cancellation, and protected paths.

- [ ] **Step 1: Write failing tests** for valid regex with line numbers, `include` filtering, zero matches, invalid regex/include, binary files, protected directories, and a cancelled context.
- [ ] **Step 2: Run the focused tests**

```powershell
go test ./internal/tool -run 'TestGrep|TestSearch' -count=1
```

Expected: newly specified missing behavior fails before implementation changes.
- [ ] **Step 3: Modify only the shared search path** when a test reveals an actual defect; keep `MaxSearchFiles`, `MaxSearchMatches`, and `MaxSearchBytes` unchanged.
- [ ] **Step 4: Re-run focused tests** and confirm PASS.
- [ ] **Step 5: Commit**

```powershell
git add internal/tool/search_test.go internal/tool/search.go
git commit -m "test: harden Grep search boundaries"
```

### Task 3: Verify preview state protection end to end

**Files:**
- Modify: `internal/tool/write_test.go`
- Modify: `internal/tool/edit_test.go`
- Modify: `internal/tool/delete_test.go`
- Test: `internal/tool/state.go`

**Interfaces:**
- Consumes: `Preview.Before`, `Preview.BeforeExists`, `checkPreviewState`, and the three commit functions.
- Produces: proof that external content, deletion, replacement, symlink conversion, and normal approval behavior are handled consistently.

- [ ] **Step 1: Add failing coverage** for external replacement, target deletion, symlink/non-regular replacement, and unchanged normal commit for WriteFile, EditFile, and DeleteFile.
- [ ] **Step 2: Run the focused tests**

```powershell
go test ./internal/tool -run 'ExternalChangeAfterPreview|PreviewState|TestWrite|TestEdit|TestDelete' -count=1
```

Expected: any missing state transition fails before implementation changes.
- [ ] **Step 3: Reuse `checkPreviewState` and add no new cache abstraction; fix only a shared guard if needed.
- [ ] **Step 4: Re-run focused tests** and confirm no mutation occurs after a stale preview.
- [ ] **Step 5: Commit**

```powershell
git add internal/tool/state.go internal/tool/write_test.go internal/tool/edit_test.go internal/tool/delete_test.go
git commit -m "test: verify stale preview rejection"
```

### Task 4: Add temporary-workspace functional and historical-record checks

**Files:**
- Create: `internal/tool/tool_functional_test.go` if absent
- Modify: `internal/session/read_test.go` only if a regression is found
- Modify: `spec/current.md` with M3.18 acceptance rows
- Create: `artifacts/verification/m3.18/tdd-tests.txt`
- Create: `artifacts/verification/m3.18/final-check.txt`

**Interfaces:**
- Consumes: public tool registry names and existing JSONL readers.
- Produces: one functional test covering Glob → Grep → ReadFile and stale EditFile, plus evidence logs.

- [ ] **Step 1: Write a temporary-workspace test** that creates files, calls the public tools, checks results, mutates a file between preview and commit, and verifies the stale commit is rejected.
- [ ] **Step 2: Run it and record TDD output** in `artifacts/verification/m3.18/tdd-tests.txt`.
- [ ] **Step 3: Read one existing session/change-set fixture** and assert it remains readable while new records use only current public names.
- [ ] **Step 4: Update `spec/current.md`** with M3.18 acceptance commands and thresholds.
- [ ] **Step 5: Run final checks and record output** in `artifacts/verification/m3.18/final-check.txt`:

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

- [ ] **Step 6: Remove disposable temporary workspace and confirm `git status` contains only intended changes.**
- [ ] **Step 7: Commit**

```powershell
git add internal/tool/tool_functional_test.go internal/session/read_test.go spec/current.md artifacts/verification/m3.18
git commit -m "test: complete M3.18 tool acceptance"
```
