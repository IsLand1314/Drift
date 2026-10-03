# M3.17.1 GitRevert 专用沙箱设计

## 目标

在不削弱普通 `Bash` 沙箱边界的前提下，让已经通过预检和用户确认的 `GitRevert` 可以安全修改 Git 必要元数据。该阶段复用现有 Linux `bwrap` 与 Windows AppContainer，不新增第二套 OS 沙箱运行时。

## 核心模型

```text
SandboxMode: off / auto / required
        ↓
SandboxRuntime: bwrap / AppContainer
        ↓
SandboxProfile: Bash / GitRevert
```

`SandboxMode` 决定沙箱是否必须可用；`SandboxProfile` 决定沙箱内允许的文件和命令边界。Profile 由控制层固定，不能由 LLM 工具参数修改。

## Bash profile（保持现状）

- workspace 普通文件可按现有命令规则访问；
- `.git`、`.drift` 继续隔离或拒绝写入；
- 网络隔离；
- workspace 外部路径不可访问；
- 超时和取消继续终止整个进程树；
- profile 不可用时，`required` 失败关闭。

## GitRevert profile

调用方只能传入已经由 `PreflightRevert` 解析出的完整 commit SHA，执行命令固定为：

```text
git -c core.hooksPath=<empty-directory> revert --no-edit <full-sha>
```

策略边界：

- 仅允许 `git revert --no-edit <full-sha>`，不接受模型传入任意 Git 命令；
- 允许 workspace 工作文件和 Git 必要元数据的读写；
- `.drift` 仍然拒绝写入；
- workspace 外路径拒绝；
- 网络关闭；
- hooks 使用临时空目录，确保不执行仓库 hooks；
- 进程树、超时、取消和输出限制沿用 Bash；
- profile 或 ACL 授权失败时不得降级为无沙箱执行。

## 平台实现

### Linux

在 `bwrap` 的 GitRevert profile 中保留 workspace bind，并取消对 `.git` 的 `tmpfs` 隔离；`.drift` 仍使用隔离挂载，继续使用 `--unshare-net`、`--die-with-parent` 和现有 cwd 约束。

### Windows

AppContainer 进程使用 GitRevert profile：

- 为 workspace 路径提供必要的遍历/读写 ACL；
- 只为 `.git` 提供必要的元数据读写 ACL；
- `.drift` 不授权写入；
- 进程结束后恢复临时 ACL；
- 继续使用 Job Object 终止子进程树；
- 父目录遍历授权必须覆盖嵌套 workspace，避免当前 `Permission denied` 工作目录问题。

## 审计

GitRevert 结果必须同时记录：

- `profile=git_revert`；
- sandbox mode/backend/probe；
- before/after HEAD；
- status 和 failure reason；
- 不记录完整命令输出、凭据或绝对路径。

## 测试矩阵

### 通用策略

- Bash profile 仍不能写 `.git`；
- GitRevert profile 可在临时仓库完成普通 commit 的 revert；
- dirty worktree、非法 commit、merge commit、冲突均在执行前或执行中安全失败；
- `.drift`、workspace 外路径和网络访问仍被拒绝；
- hooks 不执行；
- 取消/超时后无子进程和 marker 残留；
- profile 不可用时 `required` 拒绝且不发生无沙箱回退。

### Linux

在可靠 `bwrap` 可用时运行真实临时 Git 仓库集成测试；不可用时明确跳过，不伪造通过。

### Windows

运行 AppContainer/ACL 集成测试，重点覆盖嵌套 workspace cwd、`.git` 元数据写入、`.drift` 拒绝和 ACL 恢复；API 或权限不可用时明确报告失败关闭。

## 不做

- 不全局取消 `.git` 保护；
- 不让普通 Bash 自动识别命令文本后提升权限；
- 不实现 Git commit、merge、rebase、reset、clean、push profile；
- 不在本阶段实现 macOS 沙箱后端。
