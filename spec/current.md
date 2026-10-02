# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构边界见 [`doc/architecture.md`](../doc/architecture.md)。当前版本为 M3.10。M3.9 及更早阶段记录见 [`docs/spec-history.md`](../docs/spec-history.md)。

## M3.10：工具公开名称统一

M3.10 统一模型可见的内建工具名称，不改变工具行为、路径边界、审批和沙箱策略。

### 当前范围

- 只读工具：`Glob`、`Grep`、`ReadFile`。
- chat 变更工具：`WriteFile`、`EditFile`、`DeleteFile`、`Bash`。
- `-p` 仍只暴露 `Glob`、`Grep`、`ReadFile`；chat 才暴露全部七个工具。
- 旧名称 `list_files`、`search_text`、`read_file`、`write_file`、`edit_file`、`delete_file`、`run_command` 不再注册或兼容；旧会话中的旧工具调用必须按未知工具处理，不自动改写。
- 变更记录中的内部 operation（例如 `create_file`、`edit_file`、`run_command`）保持不变，避免破坏 change set 恢复语义。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M310-001 | `go test ./internal/tool -run 'TestPublicToolNames|TestDefinitionsUsePublicToolNames' -count=1` | 两个 registry 仅暴露新名称，旧名称 Lookup 全部失败 | 测试日志 | 旧名称仍可调用 |
| AC-M310-002 | `go test ./... -count=1` | Agent、权限、审计、Provider 和 TUI 均使用新名称 | 测试日志 | 任一回归失败 |
| AC-M310-003 | `go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 全部退出码为 0 | 命令日志 | 任一命令失败 |

## M3.9：会话权限模式

M3.9 增加会话级权限模式，复用现有审批回调和工具安全校验，不改变 workspace 边界。

### 当前范围

- `default`：保持现有行为，写入、编辑、删除和命令默认询问。
- `acceptEdits`：自动允许写入和编辑；删除、命令仍需询问。
- `plan`：允许读取，拒绝所有写入、编辑、删除和命令。
- `bypassPermissions`：自动允许普通变更和命令，但只跳过审批，不绕过 workspace、`.drift`、路径/符号链接/特殊文件校验、危险命令硬拒绝或 `-p` 只读边界。
- 启动参数 `--permission-mode` 只作用于当前 chat；`/permissions mode` 可在当前 chat 查询或切换，不落盘、不跨会话继承。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M39-001 | `go test ./internal/app -run TestPermissionMode -count=1` | 四种模式判定和非法值校验通过 | 测试日志 | 模式矩阵错误 |
| AC-M39-002 | chat 使用 `--permission-mode plan` | 读取可用，变更/命令不执行且无审批 | 人工记录、审计 | 发生副作用 |
| AC-M39-003 | chat 使用 `--permission-mode acceptEdits` | 写入/编辑免审批，删除/命令仍审批 | 人工记录、审计 | 权限扩大 |
| AC-M39-004 | chat 使用 `--permission-mode bypassPermissions` | 普通操作免审批；越界、`.drift`、危险命令仍拒绝 | 测试日志、人工记录 | 绕过硬边界 |
| AC-M39-005 | `go test ./...`、`go vet ./...`、build、diff check | 全部退出码为 0 | 命令日志 | 任一命令失败 |

### OS 沙箱契约（第一阶段）

- Drift 启动参数 `--sandbox` 接受 `off`、`auto`、`required`，默认 `off`；沙箱策略由控制层固定，不出现在模型工具参数中。
- `auto` 在没有已验证后端时继续执行，但结果必须记录 `sandboxed=false`；`required` 不得静默降级。
- 当前阶段只做能力检测和 fail-closed 契约，不把 Windows Job Object 当作文件/网络沙箱。
- Linux 已实现 `bwrap` 包装参数和能力探针；当前 Windows 只完成 Linux 目标编译检查，尚未完成 Linux runtime 验证。
- macOS 的 `sandbox-exec` 仍未实现可靠 profile。
- 权限模式 `bypassPermissions` 不得关闭或绕过 OS 沙箱策略。

## M3.8：命令与测试执行收敛

M3.8 在现有 `run_command` 基础上完成命令、测试、取消、权限和审计的自动化验收，不新增重复的测试工具。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M38-001 | `go test ./internal/tool -run Command -count=1` | 成功、非零退出、超时、取消和截断状态准确 | 测试日志 | `artifacts/verification/m3.8/command-tests.txt` | 状态或退出码错误 |
| AC-M38-002 | 命令路径/cwd/保护目录测试 | 越界、符号链接和受保护目录全部拒绝 | 测试日志 | `artifacts/verification/m3.8/command-tests.txt` | 安全边界绕过 |
| AC-M38-003 | `-p` 注册表测试 | 只读模式不暴露 `run_command` | 测试日志 | `artifacts/verification/m3.8/command-tests.txt` | 出现命令工具 |
| AC-M38-004 | 全量回归 | test、vet、build、diff check 全部通过 | 命令日志 | `artifacts/verification/m3.8/final-check.txt` | 任一命令失败 |

## M3.7：Change Set 恢复

M3.7 为现有文件变更记录增加安全恢复能力。它恢复 Drift change set，不执行 Git 回滚。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M37-001 | `go test ./internal/changes -run TestRestoreCreateAndEdit` | 创建文件被撤销，编辑内容恢复 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 任一文件状态错误 |
| AC-M37-002 | `go test ./internal/changes -run TestRestoreCreateAndEdit` | 编辑/覆盖回到变更前内容 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 旧内容缺失或错误 |
| AC-M37-003 | `go test ./internal/changes -run TestRestoreDeletedFile` | 删除前内容恢复 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | `.before` 不存在或恢复错误 |
| AC-M37-004 | `go test ./internal/changes -run TestRestoreRefusesStaleTargetWithoutChanges` | 外部修改时拒绝且不改动目标 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 发生部分恢复 |
| AC-M37-005 | `go test ./... -count=1` | 工具、入口和安全校验全部通过 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 任一测试失败 |
| AC-M37-006 | vet、build、diff check | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.7/final-check.txt` | 任一命令失败 |

## M3.6：工作区权限策略持久化

M3.6 在 M3.3–M3.5 的审批基础上，持久化用户明确选择“允许此类操作”的精确授权，同时保持默认 `ask` 和现有工具安全校验。

### 当前范围

- 授权只写入当前 workspace 的 `.drift/permissions.json`，不跨 workspace 或用户目录共享。
- 只持久化第二项 `Yes, and don't ask again for this pattern`；第一项只对当前操作有效，`No` 和取消不写入。
- 匹配必须同时满足工具、操作、相对路径，或命令与 cwd；不支持 glob、前缀、正则和 `allow all`。
- 策略文件使用版本 `1`、目录 `0700`、文件 `0600`，通过同目录临时文件和原子替换保存。
- 缺失、损坏、未知版本、重复规则或保存失败均安全降级为 `ask`，不得自动放行。
- `/permissions` 显示脱敏摘要，`/permissions clear` 清除当前 workspace 的持久化授权。

### 非目标

- 不提供用户级全局授权、跨 workspace 授权、命令白名单、通配符策略、权限继承或永久 `allow all`。
- 不改变现有路径、符号链接、隐藏目录、特殊文件和命令 cwd 安全校验。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M36-001 | 权限策略单元测试与新 workspace chat | 无策略时写入/编辑/删除/命令默认每次 ask | 测试日志、人工记录 | `artifacts/verification/m3.6/permission-policy-tests.txt` | 无策略自动放行 |
| AC-M36-002 | 第一项与第二项审批测试 | 第一项不跨进程保留；第二项成功后可跨进程精确命中 | 测试日志、人工记录 | `artifacts/verification/m3.6/manual-acceptance.md` | 授权范围扩大或失败操作落盘 |
| AC-M36-003 | 精确匹配、损坏配置和清除测试 | 任一字段改变、配置损坏或 clear 后重新 ask | 测试日志 | `artifacts/verification/m3.6/permission-policy-tests.txt` | 错误命中或安全降级失败 |
| AC-M36-004 | 全量回归 | `go test ./...`、`go vet ./...`、build、diff 检查全部退出码为 0 | 命令日志 | `artifacts/verification/m3.6/final-check.txt` | 任一命令失败 |

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
| AC-M35-001 | `go test ./internal/tool -run Command -count=1` | 父进程和派生子进程均退出；状态为 `cancelled` | 测试日志 | `artifacts/verification/m3.5/command-cancel-tests.txt` | 进程残留或状态误报 |
| AC-M35-002 | chat + Ctrl+C 人工验收及 TUI 等价测试 | 当前轮取消后 chat 不退出，下一条消息可继续；空闲 Ctrl+C 仍退出 | 测试日志 | `artifacts/verification/m3.5/audit-check.txt` | chat 退出或无法继续 |
| AC-M35-003 | Agent/TUI/审计专项测试 | 安全取消提示、当前轮回滚、审计不含正文 | 测试日志 | `artifacts/verification/m3.5/audit-check.txt` | 泄露原始错误或半轮状态 |
| AC-M35-004 | `go test ./... -count=1`、`go vet ./...`、build、diff 检查 | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.5/final-check.txt` | 任一命令退出码非 0 |
