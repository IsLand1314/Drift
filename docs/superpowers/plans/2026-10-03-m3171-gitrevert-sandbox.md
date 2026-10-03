# M3.17.1 GitRevert 专用沙箱 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在现有 bwrap/AppContainer 运行时上增加受控 `GitRevert` profile，同时保持普通 Bash 的 `.git` 隔离。

**Architecture:** 在 `tool.Preview` 中携带控制层固定的 profile；Linux 和 Windows 只调整 profile 对应的 OS 资源授权。Git 层继续负责预检和固定 SHA，工具层负责 profile 边界、进程生命周期和审计。

**Tech Stack:** Go 标准库、Linux bubblewrap、Windows AppContainer/ACL/Job Object、现有 `internal/git` 与 `internal/tool` 测试。

**Spec:** `docs/superpowers/specs/2026-10-03-m3171-gitrevert-sandbox-design.md`

## Global Constraints

- 普通 `Bash` 继续隔离 `.git`、`.drift`，不因命令文本自动提升权限。
- `GitRevert` 只允许固定的 `git revert --no-edit <full-sha>`。
- profile 或 OS 沙箱不可用时，`required` 失败关闭，不降级到无沙箱。
- `.drift`、workspace 外路径和网络访问始终拒绝；hooks 使用临时空目录。
- 不实现 Git commit、merge、rebase、reset、clean、push profile；macOS 保持不可用。

---

### Task 1: Profile 类型与固定命令

**Files:** `internal/tool/tool.go`, `internal/tool/command.go`, `internal/git/revert.go` and their tests.

- [ ] 写失败测试：Bash 与 GitRevert profile 区分；GitRevert 只能使用完整 SHA 和空 hooks 路径。
- [ ] 运行 `go test ./internal/tool ./internal/git -run 'Profile|RevertCommand|Sandbox' -count=1`，确认因 profile/命令约束缺失而失败。
- [ ] 增加最小 profile 类型、`Preview.Profile` 和集中式固定命令构造；拒绝任意命令/profile 组合。
- [ ] 重跑聚焦测试并提交：`feat: add GitRevert sandbox profile`。

### Task 2: Linux bwrap profile

**Files:** `internal/tool/sandbox.go`, `internal/tool/sandbox_linux_test.go` and existing command tests.

- [ ] 写失败参数测试：Bash 隔离 `.git`，GitRevert 绑定 `.git`，两者都隔离 `.drift`，并保留网络/进程限制。
- [ ] 运行 `go test ./internal/tool -run 'TestBwrap.*Profile|TestSandbox.*GitRevert' -count=1`，确认 GitRevert 规则缺失。
- [ ] 只在 `bwrapArguments` 增加 profile 分支，保持 Bash 默认参数不变；可靠 bwrap 可用时运行真实临时 Git 测试。
- [ ] 重跑并提交：`feat: allow GitRevert metadata in bwrap`。

### Task 3: Windows AppContainer/ACL

**Files:** `internal/tool/sandbox_windows.go`, `internal/tool/command_process_appcontainer_windows.go` and Windows-tagged tests.

- [ ] 写失败测试：嵌套 workspace cwd、GitRevert `.git` 写入、Bash `.git` 拒绝、`.drift` 拒绝、ACL 恢复。
- [ ] 运行 `GOOS=windows GOARCH=amd64 go test -c ./internal/tool` 和 Windows 集成测试，确认当前 ACL 缺口。
- [ ] 增加父目录遍历/继承 ACL；GitRevert 只授权 `.git` 必要写入；保留 `.drift` 拒绝、Job Object 和恢复回调。
- [ ] 重跑并提交：`fix: authorize nested GitRevert AppContainer workspace`。

### Task 4: GitRevert 连接与审计

**Files:** `internal/git/revert.go`, `internal/app/git_command.go`, `internal/agent/event.go`, `internal/session/session.go` and tests.

- [ ] 写失败测试：成功回滚、hooks 不执行、沙箱拒绝、取消和结构化审计字段。
- [ ] 运行 `go test ./internal/git ./internal/app ./internal/session -run 'GitRevert|Revert|Sandbox|Audit' -count=1`，确认 profile/审计尚未接通。
- [ ] 从 `git.Revert` 传入 GitRevert profile，创建并清理空 hooks 目录，记录 profile、HEAD 和 failure reason，禁止无沙箱回退。
- [ ] 重跑并提交：`feat: execute GitRevert through audited profile`。

### Task 5: 真实临时仓库与最终验收

**Files:** `spec/current.md`, `artifacts/verification/m3.17.1/tdd-tests.txt`, `git-sandbox-e2e.txt`, `final-check.txt`。

- [ ] 在 `.codex-temp` 创建临时 Git 仓库，验证普通 commit 回滚、dirty/merge/conflict、hook marker、`.drift`、workspace 外、网络、取消和超时；完成后删除。
- [ ] Linux bwrap 或 Windows AppContainer 不可用时明确跳过/失败关闭，不伪造通过。
- [ ] 更新 `spec/current.md` 验收行并记录 TDD、E2E、最终命令输出。
- [ ] 运行以下命令并全部通过：

```powershell
go test ./...
go vet ./...
go build ./cmd/drift
git diff --check
```

- [ ] 删除所有临时文件并提交：`test: complete M3.17.1 GitRevert sandbox acceptance`。

