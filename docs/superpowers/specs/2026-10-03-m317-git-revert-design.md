# M3.17 Git Revert 设计

## 目标

为已经提交到当前 workspace Git 仓库的 commit 提供可审计、可审批、失败可恢复的反向提交能力，同时明确禁止使用 `git reset --hard` 作为 Drift 的回滚实现。

## 用户入口

```text
drift git revert [-w <workspace>] <commit> --yes
```

- `-w` 指定 workspace；未指定时使用当前目录。
- `<commit>` 接受完整 SHA、短 SHA 或本地可解析的 ref；解析结果必须固定为一个普通 commit，merge commit 在本阶段拒绝。
- `--yes` 是 CLI 入口对本次回滚的显式确认，不代表绕过 workspace 边界或沙箱；chat 内触发时仍遵循当前权限模式和审批层。
- 未提供 `--yes` 时只做预览并拒绝执行，不进入交互式隐式确认。
- 不提供 `reset`、`clean`、`checkout --` 或任意可丢弃未提交内容的入口。

## 安全边界

1. workspace 必须是 Git 工作树，且目标 commit 必须属于该仓库可解析的历史。
2. 回滚前运行等价于 `git status --porcelain` 的检查；存在任何未提交、未跟踪或冲突状态时拒绝执行。
3. 目标必须解析为单个 commit；空值、歧义 ref、非法对象和非 commit 对象全部拒绝。
4. 先生成反向变更预览，再经过当前权限模式和审批；`plan` 始终拒绝，`default` 需要确认，`bypassPermissions` 只跳过普通审批。
5. `bypassPermissions` 不能绕过 workspace、`.git`、命令安全检查或 required 沙箱。
6. 实际执行只允许 `git revert --no-edit <resolved-commit>`；不允许拼接用户输入到 shell 字符串。
7. 冲突、hook 失败、非零退出或取消时停止并报告；不得自动执行 `git revert --abort`，也不得删除用户文件。
8. 回滚命令本身不得写入 Drift change set；Git 自己生成的反向 commit 由 Git 历史记录。

## 执行流程

```text
解析 workspace
  -> 确认 Git 工作树
  -> 解析并固定 commit
  -> 检查工作区干净
  -> 生成 git revert 预览
  -> 权限模式策略
  -> 人工审批（需要时）
  -> required 沙箱检查
  -> git revert --no-edit <commit>
  -> 读取状态和新 HEAD
  -> 结构化审计
```

失败分类必须稳定区分：`invalid_target`、`dirty_worktree`、`permission_denied`、`sandbox_denied`、`conflict`、`hook_failed`、`cancelled`、`failed`。

## 失败处理

- 执行前失败：不改变 Git 工作树。
- `git revert` 产生冲突：保留 Git 的冲突状态，报告需要用户处理；只允许用户随后显式执行 abort，不自动替用户决定。
- hook 或命令失败：记录退出码和脱敏摘要；若 Git 已进入 revert 状态，提供明确的 `git revert --abort` 建议。
- 审批失败、沙箱不可用或用户取消：不启动 Git 命令。

## 审计

复用现有结构化事件，至少记录：

- workspace 相对标识；
- 原始输入的脱敏摘要和解析后的 commit SHA；
- 当前分支和执行前 dirty 状态；
- `permission_source`、`policy`、`permission_outcome`；
- `sandbox_mode`、`sandbox_backend`、`sandbox_available`、`sandbox_probe`；
- `execution_status`、`failure_reason`、退出码摘要；
- 执行后 HEAD（成功时）。

不记录完整命令输出、文件内容、凭据或绝对路径。

## TDD 验收

- 干净临时仓库回滚普通 commit，生成一个新的反向 commit，文件内容恢复。
- dirty 工作区、未跟踪文件和冲突状态均在执行前拒绝，原状态不变。
- 非法/歧义/非 commit 目标拒绝。
- `plan`、用户拒绝、取消和 required 沙箱不可用均不启动 Git。
- 冲突和 hook 失败分类正确，审计不泄露输出。
- 全量 `go test ./...`、`go vet ./...`、构建、Windows 目标编译通过。

## 非目标

- 不实现 `git reset --hard`、`git clean`、强制 checkout 或自动丢弃未提交内容。
- 不实现远程 push、远程分支删除、rebase、merge 或 worktree 管理。
- 不把 change set restore 改造成 Git 回滚；两者保持独立入口和审计语义。
