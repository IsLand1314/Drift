# M3.5 命令取消与进程树终止设计

## 目标

让 `chat` 中正在执行的 `run_command` 可以被可靠取消：Ctrl+C 只结束当前命令和当前轮，chat 进程继续接收下一条输入；命令启动的子进程不能在取消后继续留在工作区后台运行。

## 范围

- 取消当前 `run_command` 时，结果状态固定为 `cancelled`，不能被报告为成功。
- 命令超时仍返回 `timeout`；命令自身非零退出仍返回 `failed`。
- Windows 通过独立进程组和系统进程终止机制结束 `cmd.exe` 及其后代进程。
- Unix 通过独立进程组发送终止信号，必要时升级为强制终止。
- Ctrl+C 取消当前轮后，保留既有会话、TUI 和权限记忆，允许继续输入。
- 取消过程中不保存完整命令输出、回答正文或原始参数到 session、audit 或 changes。

## 非目标

- 不引入永久权限配置、命令白名单或跨 chat 授权。
- 不改变 M3.4 的 `ask / allow / deny` 语义和精确命令匹配规则。
- 不提供跨平台完全一致的 shell 语义。
- 不增加后台任务、并发命令或任务队列。

## 现状边界

M3.4 已使用 `exec.CommandContext`、命令超时和当前轮 Context。当前实现的风险是：Context 取消主要保证父进程返回，但不同平台的 shell 子进程可能继续存在。M3.5 只收紧进程生命周期，不重写 Agent 工具循环或审批模型。

## 设计

### 1. 进程启动

`internal/tool` 保留现有 `runCommand` 公共入口，新增平台内部的进程启动/终止实现：

- Windows：启动 `cmd.exe /d /s /c` 时设置新进程组；取消或超时后先结束进程组，再等待句柄退出。
- Unix：启动 `/bin/sh -c` 时设置独立 process group；取消或超时后向进程组发送 `SIGTERM`，短暂等待后发送 `SIGKILL`。
- 普通命令、工作目录校验、输出限流、退出码和审计字段继续复用 M3.4 实现。

平台差异放在 `internal/tool` 的平台文件中，公共命令结果结构不携带平台专有字段。

### 2. 取消与超时顺序

1. `run_command` 收到父 Context 取消或超时信号。
2. 终止当前命令进程组。
3. 等待 stdout/stderr 管道关闭和进程退出，设置有限的清理等待上限。
4. 根据触发源返回 `cancelled` 或 `timeout`；若清理失败，仍不得返回 `success`。
5. Agent 将非成功状态转换为安全错误摘要，chat 保持可继续输入。

用户 Ctrl+C 的空闲退出语义保持不变；只有存在活动轮次时才取消当前命令。

### 3. 结果和审计

命令结果继续使用 M3.4 的紧凑格式，至少包含：

```text
run_command status=cancelled exit_code=-1 duration=... stdout_bytes=... stderr_bytes=... truncated=false
```

审计只保存工具名、状态、退出码、耗时、字节数和安全的相对 cwd；不保存输出正文或完整模型回答。取消不生成成功的 change 记录。

## 测试策略

- `internal/tool`：验证取消会结束父进程和子进程、超时不会误报取消、非零退出仍为 `failed`、管道关闭后函数返回。
- Windows 专项测试：启动一个会派生子进程的 `cmd.exe` 命令，取消后确认子进程退出；测试必须有明确的清理超时，避免 CI 残留。
- Unix 专项测试：启动 shell 子进程组，取消后确认进程组退出；非 Unix 平台跳过。
- `internal/agent`：验证命令取消产生安全错误摘要，当前轮回滚，下一轮仍可执行。
- `internal/app`：验证活动轮 Ctrl+C 取消当前轮，空闲 Ctrl+C 仍退出，取消后输入框和模型名恢复。
- 回归：`go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check`。

## 验收标准

| ID | 验证方法 | 通过阈值 | 证据 |
| --- | --- | --- | --- |
| AC-M35-001 | 工具取消专项测试 | 父进程和派生子进程均退出；状态为 `cancelled` | `artifacts/verification/m3.5/command-cancel-tests.txt` |
| AC-M35-002 | chat + Ctrl+C 人工验收 | 当前轮取消后 chat 不退出，下一条消息可继续；空闲 Ctrl+C 仍退出 | `artifacts/verification/m3.5/manual-acceptance.md` |
| AC-M35-003 | 审计与会话检查 | 不保存输出正文/回答正文；取消轮不伪装成功或写入 change | `artifacts/verification/m3.5/audit-check.txt` |
| AC-M35-004 | 全量回归命令 | 测试、vet、构建和 diff 检查全部退出码为 0 | `artifacts/verification/m3.5/final-check.txt` |

## 取舍

首版采用平台原生进程组终止，不引入第三方进程树库；这样依赖最少、现有 `exec.Cmd` 边界稳定。Windows 终止进程组的系统调用和 Unix 信号处理分别隔离，避免把平台分支扩散到 Agent、TUI 和审计代码。
