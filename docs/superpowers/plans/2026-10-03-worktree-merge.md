# Git Worktree Merge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Safely merge a committed managed worktree into its repository without overwriting dirty user changes or leaving the main worktree in a conflicted state.

**Architecture:** New Git operations live beside the existing managed worktree lifecycle. New worktrees use a dedicated branch; merge validates both worktrees, performs a conflict preflight, then creates a merge commit only after explicit confirmation. Conflicts return file paths and leave the main worktree unchanged.

**Tech Stack:** Go standard library, Git CLI, existing `internal/git` and `internal/app` command patterns.

**Spec:** `spec/m3.30-worktree-merge.md`

## Global Constraints

- Never use `reset --hard`, `clean`, force merge, or automatic stash.
- Main worktree and source worktree must be clean before merge.
- Merge and branch creation require explicit `--yes`.
- Keep the source worktree after merge.

### Task 1: Branch-backed creation

**Files:** `internal/git/worktree.go`, `internal/git/worktree_test.go`

- [ ] Add failing coverage that a newly created managed worktree has branch `drift/<name>` and records its base revision.
- [ ] Run the targeted test and observe failure against the current detached creation.
- [ ] Change creation to `git worktree add -b drift/<name> ... <base>` with branch-name validation.
- [ ] Run targeted Git tests.

### Task 2: Safe merge operation

**Files:** `internal/git/worktree.go`, `internal/git/worktree_test.go`

- [ ] Add failing tests for dirty main rejection, dirty source rejection, clean merge, and conflict with unchanged main HEAD.
- [ ] Implement `MergeWorktree` with explicit path/name validation, clean status checks, source branch resolution, conflict preflight, and non-force merge.
- [ ] Return structured status, conflict paths, before/after HEAD and failure reason.
- [ ] Run targeted Git tests.

### Task 3: CLI and specification

**Files:** `internal/app/git_command.go`, `internal/app/git_command_test.go`, `spec/current.md`, `spec/m3.30-worktree-merge.md`

- [ ] Add `drift git worktree merge <name> [-w <workspace>] --yes`.
- [ ] Require confirmation and print success, dirty rejection, or conflict files.
- [ ] Add command-level tests.
- [ ] Update the current specification and acceptance matrix.

### Task 4: Verification and integration

- [ ] Run `go test ./... -count=1`, `go vet ./...`, `go build ./cmd/drift`, and committed diff check.
- [ ] Commit the feature branch, fast-forward `master`, remove only the temporary implementation worktree, and rerun verification on `master`.
