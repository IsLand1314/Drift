# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构边界见 `doc/architecture.md`。当前版本为 M3.4。历史阶段记录迁移至 [`docs/spec-history.md`](../docs/spec-history.md)，不参与当前版本验收。

## M3.4：受控命令执行

M3.4 在 `chat` 中增加经审批的 `run_command`。`-p`、`audit` 和 session 管理保持只读，不暴露命令工具。

### 当前范围

- `run_command` 只在 `chat` 注册；Windows 使用 `cmd.exe /d /s /c`，Unix 使用 `/bin/sh -c`，cwd 必须是 workspace 内的真实目录。
- 默认超时 30 秒，最大 60 秒；stdout/stderr 分别限流，默认总上限 32 KiB；结果包含状态、退出码、耗时、字节数和截断标记。
- 每次执行前使用现有三选审批；当前 chat 的允许记忆仅匹配同一工具、同一 cwd 和同一精确命令，退出 chat 后失效。
- 取消、超时、非零退出和拒绝均不得报告成功；工具输出回传模型，但完整命令输出不写入 session、audit 或 changes。

### 非目标

M3.4 不提供 OS 沙箱、永久权限配置、跨进程授权、命令事务或 Git 回滚；不承诺跨平台 shell 语义完全一致。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M34-001 | `go test ./internal/tool -run RunCommand -count=1` | workspace 执行、cwd 边界、超时和输出截断通过；Registry 边界包含在同一包测试中 | 测试日志 | `artifacts/verification/m3.4/command-tests.txt` | 任一命令边界失败 |
| AC-M34-002 | `go test ./internal/agent ./internal/app ./internal/session -count=1` | chat-only 注册、审批精确记忆、命令审计脱敏通过 | 测试日志 | `artifacts/verification/m3.4/integration-tests.txt` | `-p` 暴露命令或审计泄漏正文 |
| AC-M34-003 | `go test ./... -count=1`、`go vet ./...`、build、diff 检查 | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.4/final-check.txt` | 任一命令退出码非 0 |
