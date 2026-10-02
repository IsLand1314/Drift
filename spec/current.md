# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构边界见 [`doc/architecture.md`](../doc/architecture.md)。当前版本为 M3.5。M3.4 及更早阶段记录见 [`docs/spec-history.md`](../docs/spec-history.md)。

## M3.5：命令取消与进程树终止

M3.5 在 M3.4 的 `run_command` 基础上，确保取消或超时时命令进程树收敛，当前 chat 轮次安全结束并可继续输入。

### 当前范围

- Ctrl+C 在活动轮次中只取消当前 Agent 轮和命令；空闲时仍按既有语义退出 chat。
- Windows 使用 Job Object，并以 `taskkill /T /F` 作为进程树清理回退；Unix 使用独立 process group 和信号升级。
- 结果明确区分 `cancelled`、`timeout`、`failed`，任何非成功状态都不能报告为成功。
- 取消后的 TUI 显示安全提示，不泄露原始 `context canceled`；下一轮可以继续输入。
- 审计、session 和 changes 不保存完整命令输出或回答正文。

### 非目标

- 不引入永久权限配置、命令白名单、跨 chat 授权、后台任务或并发命令。
- 不改变 M3.4 的 `ask / allow / deny` 及精确命令匹配语义。
- 不承诺 Windows 与 Unix 的 shell 语义完全一致。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M35-001 | `go test ./internal/tool -run 'Command.*Cancel|Command.*ProcessTree' -count=1` | 父进程和派生子进程均退出；状态为 `cancelled` | 测试日志 | `artifacts/verification/m3.5/command-cancel-tests.txt` | 进程残留或状态误报 |
| AC-M35-002 | chat + Ctrl+C 人工验收 | 当前轮取消后 chat 不退出，下一条消息可继续；空闲 Ctrl+C 仍退出 | 人工记录 | `artifacts/verification/m3.5/manual-acceptance.md` | chat 退出或无法继续 |
| AC-M35-003 | Agent/TUI/审计专项测试 | 安全取消提示、当前轮回滚、审计不含正文 | 测试日志 | `artifacts/verification/m3.5/audit-check.txt` | 泄露原始错误或半轮状态 |
| AC-M35-004 | `go test ./... -count=1`、`go vet ./...`、build、diff 检查 | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.5/final-check.txt` | 任一命令退出码非 0 |
