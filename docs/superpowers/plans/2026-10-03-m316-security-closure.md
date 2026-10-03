# M3.16 Security Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax with TDD checkpoints.

**Goal:** Close the Drift permission, sandbox refusal, audit, and failed-change recovery loop without adding a new sandbox or command parser.

**Architecture:** Keep policy resolution separate from interactive approval. A single resolver produces `allow`, `ask`, or `deny`; the UI maps an `ask` result to one-time allow, persistent allow, deny, or cancelled. Tool execution remains behind existing previews, sandbox decisions, and change sets.

**Tech Stack:** Go standard library, existing Bubble Tea approval UI, existing JSONL audit writer, existing `internal/changes` snapshots, existing Linux/Windows sandbox backends.

**Spec:** `docs/superpowers/specs/2026-10-03-m316-security-closure-design.md`

## Global Constraints

- Persistent permissions remain workspace-local at `.drift/permissions.json`.
- Persistent matching remains exact; no glob, regex, prefix, or global allow-all.
- Hard path, symlink, protected-directory, dangerous-command, and required-sandbox checks cannot be bypassed by approval.
- macOS continues to report no OS sandbox backend.
- Existing user modifications under `doc/Process/` are not staged.

### Task 1: Add explicit permission decision types

**Files:**
- Modify: `internal/agent/event.go`
- Modify: `internal/app/permission_mode.go`
- Test: `internal/app/permission_mode_test.go`

- [ ] Write a failing table test asserting policy outcomes remain `allow`, `ask`, and `deny`, while approval results use `allow_once`, `allow_persistent`, `deny`, and `cancelled`.
- [ ] Run `go test ./internal/app -run 'TestPermission' -count=1` and verify the new assertions fail because the typed outcomes do not exist.
- [ ] Add the smallest typed constants and conversion helpers; keep existing serialized reason strings compatible.
- [ ] Run the focused test and then `go test ./internal/app -run 'TestPermission' -count=1` until green.

### Task 2: Centralize approval and sandbox gate

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_tui.go`
- Modify: `internal/agent/agent.go`
- Test: `internal/app/chat_test.go`
- Test: `internal/app/chat_tui_test.go`

- [ ] Add failing tests for plan hard-deny, required-sandbox unavailable after an allow decision, and persistent allow only after a successful tool result.
- [ ] Run the focused tests and verify each fails for the missing gate or incorrect reason.
- [ ] Route both main-screen and fullscreen approval paths through one resolver; preserve the existing UI labels and exact-rule behavior.
- [ ] Ensure `allow_once` never enters the policy file, `allow_persistent` is pending until a successful `EventToolResult`, and `deny/cancelled` clear pending state.
- [ ] Run the focused tests and the existing chat test suite.

### Task 3: Add structured sandbox and execution audit fields

**Files:**
- Modify: `internal/agent/event.go`
- Modify: `internal/tool/command.go`
- Modify: `internal/session/session.go`
- Modify: `internal/session/jsonl.go`
- Test: `internal/session/jsonl_test.go`
- Test: `internal/tool/command_test.go`

- [ ] Add failing audit tests asserting permission source, sandbox backend/probe, execution status, and failure reason are present while output/content remain redacted.
- [ ] Run the focused audit tests and verify the fields are absent before implementation.
- [ ] Populate structured fields at the tool boundary instead of parsing result text in the UI.
- [ ] Run session and command focused tests, confirming old JSONL entries remain readable.

### Task 4: Complete failed change-set recovery tests

**Files:**
- Modify: `internal/changes/restore.go` only if a test exposes a gap.
- Test: `internal/changes/restore_test.go`
- Test: `internal/app/chat_test.go`

- [ ] Add failing tests for a partial multi-file change, an externally modified target, and a failed operation not persisting permission.
- [ ] Run them and verify the failure is due to missing atomic preflight or stale-target behavior.
- [ ] Implement only the minimal change-set fix needed; preserve the existing explicit restore command.
- [ ] Run all change and chat tests, checking no partial restore occurs.

### Task 5: Verification matrix and documentation

**Files:**
- Modify: `spec/current.md`
- Modify: `docs/superpowers/specs/2026-10-03-m316-security-closure-design.md`
- Create: `.codex-temp/m316-verification.txt`

- [ ] Run the approval, policy, audit, sandbox, and change tests as separate commands and record outputs in the scratch verification file.
- [ ] Run `go test ./... -count=1`, `go vet ./...`, `go build ./cmd/drift`, Windows cross-compile, and `git diff --check`.
- [ ] Remove the scratch verification file after evidence is captured unless the project explicitly requests it.
- [ ] Review the diff, stage only M3.16 files, commit, and push `master`.
