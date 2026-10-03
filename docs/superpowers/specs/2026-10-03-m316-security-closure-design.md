# M3.16 安全闭环设计

## 目标

统一审批决策、权限模式、持久化授权、沙箱拒绝、脱敏审计和变更失败恢复，确保一次工具调用只能沿一条可解释且可验证的安全路径执行。

## 决策模型

策略层和交互层分离：

```text
PolicyDecision: allow | ask | deny
ApprovalDecision: allow_once | allow_persistent | deny | cancelled
```

策略层先处理硬安全边界和权限模式；只有 `ask` 才进入人工审批。`Yes` 映射为 `allow_once`，第二项映射为 `allow_persistent`，`No` 映射为 `deny`，Esc/Ctrl+C 映射为 `cancelled`。`allow_persistent` 只在工具成功后写入当前 workspace 的精确规则；不持久化 deny。

## 执行顺序

```text
预览与路径校验
  -> 硬拒绝（越界、保护目录、危险命令、plan 限制）
  -> 权限模式
  -> 精确持久化 allow
  -> ask / 人工审批
  -> required 沙箱检查
  -> 工具执行
  -> 变更记录、审计和结果
```

人工允许不能绕过硬拒绝或 `required` 沙箱。执行前必须使用审批时的同一预览；预览状态变化时拒绝提交。

## 审计

权限请求、权限决定、沙箱决定和工具结果继续使用现有 JSONL 审计文件，但增加稳定的结构化字段：`permission_source`、`permission_decision`、`sandbox_mode`、`sandbox_backend`、`sandbox_available`、`sandbox_probe`、`execution_status`、`failure_reason`。审计不保存回答正文、完整命令输出、文件内容或凭据。

## 失败恢复

同一回合的多文件变更使用一个 change set。工具失败或取消时不写入待持久化授权，change set 标记为 `partial`；恢复前先验证每个目标仍等于变更后的快照，任一目标被外部修改则整体拒绝，不覆盖用户改动。Git reset 不属于本阶段。

## 非目标

- 不引入 Bash AST 解析器、通配符权限或持久化 deny。
- 不改变既有 Windows AppContainer、Linux bwrap 和 macOS 无后端策略。
- 不实现 Git worktree 或 Git rollback。
