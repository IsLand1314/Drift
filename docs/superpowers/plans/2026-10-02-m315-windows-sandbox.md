# M3.15 Windows Sandbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add the M3.15-A platform capability contract: keep Linux `bwrap`, make macOS explicitly unavailable, and add a fail-closed Windows AppContainer probe boundary without claiming Windows isolation before the probe passes.

**Architecture:** Keep `SandboxCapabilities` and `SelectSandbox` as the control-plane boundary. Move platform-specific detection into build-tagged files; Windows owns the AppContainer probe and returns a reliable backend only when the probe verifies filesystem, network, child-process, and cleanup behavior. Existing Job Object cancellation remains the process-lifecycle primitive.

**Tech Stack:** Go 1.25+, standard `os/exec`, `syscall`, `golang.org/x/sys/windows`, build-tagged tests, existing Bubble Tea/app wiring unchanged.

**Spec:** `docs/superpowers/specs/2026-10-02-m315-windows-sandbox-design.md`

## Global Constraints

- macOS detection returns empty `SandboxCapabilities`; do not call `sandbox-exec`.
- `required` fails closed when a complete backend is unavailable; `auto` may continue with `sandboxed=false`.
- `bypassPermissions` never changes sandbox selection.
- The model-visible `Bash` schema does not gain a sandbox parameter.
- Do not add a global Windows Firewall rule, Node runtime, remote execution, or Git worktree.
- Do not modify unrelated dirty user files.

---

### Task 1: Make platform detection explicit and testable

**Files:**
- Modify: `internal/tool/sandbox.go`
- Create: `internal/tool/sandbox_platform_windows.go`
- Create: `internal/tool/sandbox_platform_unix.go`
- Test: `internal/tool/sandbox_test.go`

**Interfaces:**
- Consumes: `SandboxCapabilities`, `SandboxMode`, existing `SelectSandbox`.
- Produces: `detectPlatformSandbox() SandboxCapabilities` used only by `DetectSandbox()`.

- [ ] **Step 1: Write failing tests**

Add tests asserting that an unavailable platform capability yields `Backend == ""`, `Reliable == false`, and no capabilities, and that `SelectSandbox(SandboxRequired, capabilities)` fails while `SelectSandbox(SandboxAuto, capabilities)` succeeds with `Available == false`.

- [ ] **Step 2: Run the focused tests and verify the expected failure**

Run:

```powershell
go test ./internal/tool -run 'Test(Detect|Select)Sandbox' -count=1
```

Expected: the new detector seam is missing or the platform-specific expectation fails.

- [ ] **Step 3: Implement the smallest detector seam**

Change `DetectSandbox()` to delegate to `detectPlatformSandbox()`. Keep Linux `bwrap` probing unchanged. Add a non-Windows implementation for the current Linux/macOS switch; the macOS branch returns an empty capability value instead of probing `sandbox-exec`. Add a Windows implementation that currently returns an empty capability value until Task 2's probe is available.

- [ ] **Step 4: Run focused and cross-platform compile checks**

Run:

```powershell
go test ./internal/tool -run 'Test(Detect|Select)Sandbox' -count=1
$env:GOOS='windows'; $env:GOARCH='amd64'; go test -c -o .codex-temp/tool-windows.test ./internal/tool; Remove-Item Env:GOOS,Env:GOARCH
$env:GOOS='darwin'; $env:GOARCH='amd64'; go test -c -o .codex-temp/tool-darwin.test ./internal/tool; Remove-Item Env:GOOS,Env:GOARCH
```

Expected: focused tests pass and both target binaries compile. Delete the two `.codex-temp` binaries after the check.

- [ ] **Step 5: Commit**

```powershell
git add internal/tool/sandbox.go internal/tool/sandbox_platform_windows.go internal/tool/sandbox_platform_unix.go internal/tool/sandbox_test.go
git commit -m "refactor: make sandbox platform detection explicit"
```

### Task 2: Add the Windows AppContainer probe boundary

**Files:**
- Create: `internal/tool/sandbox_windows.go`
- Create: `internal/tool/sandbox_windows_test.go`
- Modify: `internal/tool/sandbox_platform_windows.go`

**Interfaces:**
- Consumes: `detectPlatformSandbox()`, workspace root, existing `sandboxCommand` process lifecycle.
- Produces: `probeWindowsSandbox(root string) SandboxCapabilities` and `windowsSandboxUnavailable(reason string) SandboxCapabilities`.

- [ ] **Step 1: Write the failing probe contract tests**

Add Windows-only tests for a probe result with `Reliable == false` when AppContainer creation or capability verification fails, and for a successful injected probe result containing exactly `workspace-write`, `network-isolated`, and `process-tree`. Keep the real probe behind a small function variable so tests do not require admin privileges or mutate a user workspace.

- [ ] **Step 2: Run the Windows-focused tests and verify failure**

Run:

```powershell
$env:GOOS='windows'; $env:GOARCH='amd64'; go test -c -o .codex-temp/tool-windows.test ./internal/tool; Remove-Item Env:GOOS,Env:GOARCH
```

Expected: the probe contract symbols are missing.

- [ ] **Step 3: Implement the minimal Windows probe boundary**

Use `golang.org/x/sys/windows` and lazy-loaded Windows APIs to create a temporary AppContainer identity, construct a restricted process token with no network capability, launch the command through the existing Job Object path, and remove the profile/ACL in a deferred cleanup block. The probe must return `Reliable == true` only after all checks in the design document pass; any API, ACL, launch, or cleanup error returns `Reliable == false` with no partial capabilities.

- [ ] **Step 4: Verify required/auto behavior**

Add assertions that an unsuccessful probe makes `required` return an error and `auto` return an unavailable decision. Verify the model schema still does not contain `sandbox_mode`.

- [ ] **Step 5: Run Windows tests and commit**

Run:

```powershell
go test ./internal/tool -run 'TestWindowsSandbox|TestSelectSandbox|TestRunCommandSchema' -count=1
git add internal/tool/sandbox_windows.go internal/tool/sandbox_windows_test.go internal/tool/sandbox_platform_windows.go
git commit -m "feat: add Windows AppContainer sandbox probe"
```

### Task 3: Add runtime acceptance and documentation synchronization

**Files:**
- Create: `internal/tool/sandbox_runtime_windows_test.go`
- Modify: `spec/current.md`
- Modify: `doc/m3.14-sandbox-cancellation.md`
- Modify: `docs/superpowers/specs/2026-10-02-m315-windows-sandbox-design.md`

**Interfaces:**
- Consumes: the Windows capability probe and existing `ExecuteCommand` status output.
- Produces: repeatable Windows acceptance evidence and current-scope documentation.

- [ ] **Step 1: Write Windows integration tests**

Add tests that use a dedicated temporary workspace and verify: workspace write succeeds, outside/`.drift`/`.git` writes fail, network access fails, cancellation and timeout leave no marker file, and the temporary profile/ACL is removed.

- [ ] **Step 2: Run the integration tests before implementation changes**

Run:

```powershell
go test ./internal/tool -run 'TestWindowsSandboxRuntime' -count=1
```

Expected: tests fail or skip with an explicit unavailable-backend reason; a skip is not recorded as a pass.

- [ ] **Step 3: Wire the probe result into audit fields**

Keep `sandbox_backend`, `sandboxed`, and capability fields stable. Add only the documented `sandbox_probe` value, and ensure cleanup failures are returned as command errors.

- [ ] **Step 4: Run the complete verification set**

Run:

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

Expected: all commands exit 0; Windows runtime tests either pass on a supported host or report a clear unavailable backend without claiming reliability.

- [ ] **Step 5: Update scope and commit**

Record M3.15-A/B/C status in `spec/current.md`, keep macOS explicitly unavailable, and commit:

```powershell
git add internal/tool/sandbox_runtime_windows_test.go spec/current.md doc/m3.14-sandbox-cancellation.md docs/superpowers/specs/2026-10-02-m315-windows-sandbox-design.md
git commit -m "test: verify Windows sandbox runtime contract"
```
