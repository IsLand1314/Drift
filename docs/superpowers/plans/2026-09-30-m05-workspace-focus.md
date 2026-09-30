# M0.5 Workspace 与 Focus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `drift -w <目录或文件> -p <提示>` 在用户明确选定的只读 workspace 内运行；文件目标只作为首轮 focus，不自动读取。

**Architecture:** `internal/app` 解析 `-w` 并把目标转换成经过规范化的 `Root + Focus`。Agent 接收相对 focus，仅在首轮系统提示中注入该方向；所有工具继续以 Root 作为边界并且只接受相对路径。会话 JSONL 写入 Root 下的 `.drift/sessions`，而 Drift 的 `.env` 仍从启动目录读取。

**Tech Stack:** Go 1.26+、标准库（`flag`、`os`、`filepath`、`testing`、`httptest`），不增加第三方依赖。

**Spec:** `doc/m0.5-workspace-focus.md`

## Global Constraints

- `-w` 缺省时必须保持 M0.4 的“启动时当前目录即 workspace”行为。
- 目录目标的 focus 为空；普通文件目标的 workspace 是其父目录、focus 是 slash 分隔的相对文件名。
- `-w` 目标必须存在，且不得是符号链接、特殊文件或 dotenv 凭据文件。
- Provider 工具参数始终是 workspace 内相对路径；绝对路径、`..`、dotenv、符号链接和特殊文件继续拒绝。
- focus 只修改首轮系统提示，不自动执行工具、不增加任何请求或工具预算。
- `.env` 始终从启动命令时的当前目录读取；JSONL 始终写入选定 workspace 的 `.drift/sessions/`。
- 无效 `-w` 必须在任何 Provider HTTP 请求前返回退出码 2；错误中不暴露不必要的本地绝对路径。
- 所有 Go 提交使用作者 `island <island0920@163.com>` 和中文 commit 信息。

---

## File Structure

| 文件 | 责任 |
| --- | --- |
| `internal/app/workspace.go` | 将启动目录与 `-w` 原始值转换为已验证的 `workspaceSelection{Root, Focus}`。 |
| `internal/app/workspace_test.go` | 覆盖目录、文件、相对/绝对路径、dotenv、符号链接、缺失和特殊文件选择。 |
| `internal/app/app.go` | 定义 `-w` flag，调用 resolver，用 Root 创建会话，并把 Focus 传给 Agent。 |
| `internal/app/app_test.go` | 用本地 SSE 验证 CLI 的目录/file workspace、首轮 focus、相对工具路径、会话位置和失败前不请求 Provider。 |
| `internal/agent/agent.go` | 在现有 `RunEventsWithRegistry` 中接收 focus，并构造首轮系统提示。 |
| `internal/agent/agent_test.go` | 验证 focus 仅出现在首轮系统消息，且没有引入自动工具调用或后续轮污染。 |
| `internal/tool/read.go`、`internal/tool/walk.go` | 导出共享的 dotenv 文件名判断，避免 app 与 tool 各自维护安全规则。 |
| `spec/current.md`、`README.md`、`doc/m0.5-workspace-focus.md` | 将 M0.5 标记为当前已交付范围，记录 `-w` 用法与验收。 |

### Task 1: 建立 Workspace Resolver

**Files:**
- Create: `internal/app/workspace.go`
- Create: `internal/app/workspace_test.go`
- Modify: `internal/tool/read.go`
- Modify: `internal/tool/walk.go`

**Interfaces:**
- Produces:

  ```go
  type workspaceSelection struct {
      Root  string
      Focus string
  }

  func resolveWorkspace(launchDir, rawTarget string) (workspaceSelection, error)
  ```

- Consumes: `tool.IsDotEnvCredentialFile(name string) bool`; `launchDir` is the original process current directory and `rawTarget` is the value of `-w`.

- [ ] **Step 1: Write failing resolver tests**

  In `internal/app/workspace_test.go`, add table-driven cases that create a temp launch directory and target fixtures:

  ```go
  got, err := resolveWorkspace(launchDir, filepath.Join(targetDir, "README.md"))
  if err != nil {
      t.Fatal(err)
  }
  if got.Root != targetDir || got.Focus != "README.md" {
      t.Fatalf("selection = %#v", got)
  }
  ```

  Cover all exact cases:

  - empty `rawTarget` returns `Root=launchDir`, `Focus=""`;
  - absolute directory returns itself as Root and no focus;
  - relative directory resolves against `launchDir`;
  - absolute and relative regular files return their parent and slash-normalized basename focus;
- missing target, target symlink, and `.env` / `.env.local` file target return an error;
  - where Windows symlink creation returns only permission/privilege errors, skip that subtest; any other symlink error fails the test.

- [ ] **Step 2: Run the resolver test before implementation**

  Run:

  ```powershell
  go test ./internal/app -run TestResolveWorkspace -count=1
  ```

  Expected: FAIL because `resolveWorkspace` and `workspaceSelection` do not exist.

- [ ] **Step 3: Export the single shared dotenv filename helper**

  In `internal/tool/read.go`, rename:

  ```go
  func isDotEnvCredentialFile(name string) bool
  ```

  to:

  ```go
  func IsDotEnvCredentialFile(name string) bool
  ```

  Update all internal uses in `read.go` and `walk.go`. Do not change its behavior: case-insensitively reject exactly `.env` and names starting `.env.`.

- [ ] **Step 4: Implement the smallest resolver**

  In `internal/app/workspace.go`:

  ```go
  type workspaceSelection struct {
      Root  string
      Focus string
  }

  func resolveWorkspace(launchDir, rawTarget string) (workspaceSelection, error) {
      target := launchDir
      if rawTarget != "" {
          target = rawTarget
          if !filepath.IsAbs(target) {
              target = filepath.Join(launchDir, target)
          }
      }
      target, err := filepath.Abs(filepath.Clean(target))
      if err != nil {
          return workspaceSelection{}, fmt.Errorf("workspace target is invalid")
      }
      info, err := os.Lstat(target)
      if err != nil {
          return workspaceSelection{}, fmt.Errorf("workspace target is invalid")
      }
      if info.Mode()&os.ModeSymlink != 0 {
          return workspaceSelection{}, fmt.Errorf("workspace target is invalid")
      }
      target, err = filepath.EvalSymlinks(target)
      if err != nil {
          return workspaceSelection{}, fmt.Errorf("workspace target is invalid")
      }
      info, err = os.Stat(target)
      if err != nil {
          return workspaceSelection{}, fmt.Errorf("workspace target is invalid")
      }

      switch {
      case info.IsDir():
          return workspaceSelection{Root: target}, nil
      case info.Mode().IsRegular():
          focus := filepath.ToSlash(filepath.Base(target))
          if tool.IsDotEnvCredentialFile(focus) {
              return workspaceSelection{}, fmt.Errorf("workspace target is invalid")
          }
          return workspaceSelection{Root: filepath.Dir(target), Focus: focus}, nil
      default:
          return workspaceSelection{}, fmt.Errorf("workspace target is invalid")
      }
  }
  ```

  Re-stat the resolved target after `EvalSymlinks`; do not use a stale `FileInfo`. Every resolver error must be phrased as validation failure, not return raw path details to the CLI.

- [ ] **Step 5: Run resolver and affected tool tests**

  Run:

  ```powershell
  go test ./internal/app -run TestResolveWorkspace -count=1
  go test ./internal/tool -count=1
  ```

  Expected: PASS.

- [ ] **Step 6: Commit the isolated resolver**

  ```powershell
  git add internal/app/workspace.go internal/app/workspace_test.go internal/tool/read.go internal/tool/walk.go
  git -c user.name=island -c user.email=island0920@163.com commit -m "功能：解析显式 Workspace 与 Focus 文件"
  ```

### Task 2: 将 Focus 接入首轮 Agent 提示

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**
- Changes:

  ```go
  func RunEventsWithRegistry(
      ctx context.Context,
      client llm.Client,
      root string,
      prompt string,
      focus string,
      registry tool.Registry,
      sink EventSink,
  ) error
  ```

- Produces: first request system content includes focus only when `focus != ""`; `Run` and `RunEvents` pass an empty focus string.

- [ ] **Step 1: Add failing Agent tests**

  Add a direct-answer scripted-client test:

  ```go
  err := RunEventsWithRegistry(ctx, client, root, "explain it", "README.md", registry, sink)
  if !strings.Contains(client.requests[0].Messages[0].Content, "README.md") {
      t.Fatal("first system message lacks focus")
  }
  if len(client.requests) != 1 {
      t.Fatalf("requests = %d, want 1", len(client.requests))
  }
  ```

  Add one tool-call round-trip test that asserts:

  - first request system content includes the quoted focus;
  - second request has no system message and no copied focus instruction;
  - the model-provided `{"path":"README.md"}` remains unchanged and relative;
  - no tool is automatically inserted before the scripted model tool call.

  Update existing direct `RunEventsWithRegistry` test calls to supply `""` for focus.

- [ ] **Step 2: Run focused Agent tests before implementation**

  Run:

  ```powershell
  go test ./internal/agent -run "TestRun.*Focus|TestRunWithRegistry" -count=1
  ```

  Expected: FAIL because `RunEventsWithRegistry` has no focus parameter and the first system prompt is static.

- [ ] **Step 3: Implement focus-aware system instruction**

  Keep the existing static instruction as the base. Add:

  ```go
  func systemInstruction(focus string) string {
      if focus == "" {
          return nativeToolSystemInstruction
      }
      return nativeToolSystemInstruction +
          "\n\nThe user-selected initial focus target is " + strconv.Quote(focus) +
          ". Prioritize answering about it; use read_file only when needed."
  }
  ```

  Use `systemInstruction(focus)` only while constructing request index 0. Do not append focus to `messages`, tool results, JSONL events, or any later Provider request. Pass `""` from `Run` and `RunEvents`.

- [ ] **Step 4: Run the full Agent package**

  Run:

  ```powershell
  go test ./internal/agent -count=1
  ```

  Expected: PASS. If the Windows temporary executable is blocked, compile to a checked temporary file with `go test -c`, execute that binary, then delete it.

- [ ] **Step 5: Commit Agent integration**

  ```powershell
  git add internal/agent/agent.go internal/agent/agent_test.go
  git -c user.name=island -c user.email=island0920@163.com commit -m "功能：在首轮提示注入 Focus 文件"
  ```

### Task 3: 在 CLI/App 中启用 -w

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `resolveWorkspace(launchDir, rawTarget)` and focus-aware `agent.RunEventsWithRegistry`.
- Produces: `-w` CLI flag; selected Root determines tool boundary and JSONL location.

- [ ] **Step 1: Add failing App integration tests**

  Add `TestRunWorkspaceDirectory` with a separate launch directory and target workspace. The local SSE handler must assert:

  ```go
  if request.Messages[0].Role != "system" ||
      strings.Contains(request.Messages[0].Content, "initial focus target") {
      t.Fatalf("directory request system = %#v", request.Messages[0])
  }
  if request.Messages[1].Content != "analyze target" {
      t.Fatalf("user prompt = %q", request.Messages[1].Content)
  }
  ```

  Respond with a `read_file` tool call using `{"path":"README.md"}`, then assert the tool result comes from the target workspace and its session file is `<target>/.drift/sessions/*.jsonl`, not the launch directory.

  Add `TestRunWorkspaceFileFocusDoesNotAutoRead`:

  - launch from a directory that contains the Drift `.env`;
  - pass `-w <target>/README.md`;
  - return a direct stop response from the SSE server;
  - assert exactly one request, its first system message includes `README.md`, stdout contains only the final server answer, and target session JSONL exists.

  Add `TestRunInvalidWorkspaceDoesNotCallProvider` using a server request counter:

  ```go
  code := Run(ctx, []string{"-w", filepath.Join(dir, "missing"), "-p", "x"}, getenv, &out, &stderr)
  if code != 2 || requests != 0 {
      t.Fatalf("code/requests = %d/%d, want 2/0", code, requests)
  }
  ```

- [ ] **Step 2: Run App tests before implementation**

  Run:

  ```powershell
  go test ./internal/app -run "TestRunWorkspace" -count=1
  ```

  Expected: FAIL because `-w` is not a recognized flag and app always uses `os.Getwd()`.

- [ ] **Step 3: Implement `-w` at the app composition root**

  In `Run`:

  1. Define `workspaceTarget := flags.String("w", "", "要分析的目录或文件（默认当前目录）")`.
  2. After `flags.Parse` and prompt validation, capture `launchDir, err := os.Getwd()`.
  3. Call `resolveWorkspace(launchDir, *workspaceTarget)`; print a stable short error and return 2 on failure.
  4. Do not move `config.LoadDotEnv(".env")`: it deliberately reads the launch directory before `-w` is applied.
  5. Replace the old `root = os.Getwd()` result with `selection.Root` for Session Writer and Agent.
  6. Call `agent.RunEventsWithRegistry(ctx, client, selection.Root, *prompt, selection.Focus, tool.NewDefaultRegistry(), sink)`.
  7. Extend the usage string with `[-w 路径]`.

  Do not change tool definitions, Provider request wire format, stdout behavior, or session redaction logic.

- [ ] **Step 4: Run App and Session tests**

  Run:

  ```powershell
  go test ./internal/app ./internal/session -count=1
  ```

  Expected: PASS.

- [ ] **Step 5: Commit CLI integration**

  ```powershell
  git add internal/app/app.go internal/app/app_test.go
  git -c user.name=island -c user.email=island0920@163.com commit -m "功能：支持 -w 选择 Workspace"
  ```

### Task 4: 同步 M0.5 当前文档并完成验证

**Files:**
- Modify: `spec/current.md`
- Modify: `README.md`
- Modify: `doc/m0.5-workspace-focus.md`

**Interfaces:**
- Consumes: final CLI behavior and acceptance tests from Tasks 1–3.
- Produces: M0.5 becomes current scope; M0.4 remains a historical stage record.

- [ ] **Step 1: Update the current spec**

  At the top of `spec/current.md`, add M0.5 as the current version. State:

  - `-w` directory/file/default rules;
  - Root/Focus semantics and first-turn-only focus instruction;
  - relative-only tool paths and unchanged safety boundaries;
  - launch-directory `.env` and selected-root session placement;
  - invalid `-w` exit code 2 and no Provider request;
  - M0.4 is historical behavior underneath.

- [ ] **Step 2: Update user-facing docs**

  In `README.md`, add one directory and one file `-w` invocation. Link `doc/m0.5-workspace-focus.md`.

  In `doc/m0.5-workspace-focus.md`, change status to completed, replace future-tense wording with actual behavior, and append exact completed verification commands.

- [ ] **Step 3: Run final verification**

  Run:

  ```powershell
  go test ./... -count=1
  go vet ./...
  go build ./cmd/drift
  git diff --check
  ```

  Expected: all commands exit 0. Remove any ignored `drift.exe` or temporary test binary created only for verification before finishing.

- [ ] **Step 4: Check for secrets and unintended scope**

  Run:

  ```powershell
  rg -n -i "authorization:\s*bearer\s+[^<]|OPENAI_API_KEY\s*=\s*[^\"\s]" README.md spec/current.md doc/m0.5-workspace-focus.md internal/app internal/agent
  git status --short
  ```

  Expected: no real credential appears; only intended M0.5 files remain staged or changed.

- [ ] **Step 5: Commit documentation and verification**

  ```powershell
  git add spec/current.md README.md doc/m0.5-workspace-focus.md
  git -c user.name=island -c user.email=island0920@163.com commit -m "文档：同步 M0.5 Workspace 使用说明"
  ```
