# M3.9 OS 沙箱设计

## 为什么需要沙箱

`run_command` 执行的是用户批准的 shell 命令。即使命令的 cwd 位于 workspace，shell 仍可能：

- 读取 workspace 外的凭据、环境变量和用户文件；
- 通过绝对路径、网络、子进程或系统 API 访问主机资源；
- 修改或删除 workspace 外文件；
- 启动脱离当前进程树的后台进程；
- 将命令输出或敏感数据发送到网络。

因此，workspace 路径校验、审批和 Job Object 不能替代 OS 沙箱。审批解决“用户是否同意”，沙箱解决“同意后进程最多能做什么”。

## 威胁模型

防护对象是模型生成的命令、命令调用的脚本和其子进程。默认假设命令可能是恶意的或被间接依赖污染的。M3.9 重点限制文件系统、网络和进程树边界；不承诺防御内核漏洞、已获得管理员权限的进程或用户主动关闭沙箱后的命令。

## 模式

`run_command` 增加显式沙箱模式：

- `off`：保持现有行为，仍需要审批；适用于明确需要主机能力的命令。
- `auto`：有可靠 OS 后端时启用；无后端时继续执行但在审批和审计中明确显示“未沙箱”。
- `required`：没有可靠后端就拒绝执行，不能静默降级。

默认仍为 `off`，避免改变现有用户工作流；高风险 workspace 或 CI 可使用 `required`。

## 最小策略

沙箱进程允许：

- 访问指定 workspace 的必要文件；
- 访问受控临时目录；
- 在 workspace 内创建子进程并受现有超时/取消约束。

默认拒绝：

- workspace 外文件和用户目录；
- `.drift`、`.git`、凭据目录；
- 网络访问；
- 提权、设备和系统管理接口；
- 脱离进程树的后台任务。

网络和额外路径必须作为独立、可审计的显式策略，不通过命令字符串猜测放行。

## 平台取舍

- Linux：优先检测 `bwrap`，用 mount namespace、只读根、workspace bind 和 network namespace 实现；不可用时 `required` 必须失败。
- macOS：仅在可用时使用系统 sandbox profile；不把已废弃或不可用的后端伪装成安全沙箱。
- Windows：现有 Job Object 只负责进程树终止，不是文件/网络沙箱。M3.9 先做能力检测和明确拒绝；真正的受限 token/AppContainer 后端另立阶段，避免误报安全。

## 审计与失败语义

每次命令记录 `sandbox_mode`、`backend`、`capabilities` 和最终状态。`required` 无后端、策略编译失败或启动失败都返回明确失败，不执行未沙箱命令。沙箱拒绝不能被报告为命令成功。

## 非目标

- 不把审批、权限持久化、change set 恢复和 Git 回滚合并到沙箱实现。
- 不新增 `run_test` 工具；测试仍通过 `run_command` 执行。
- 不承诺跨平台完全相同的系统调用或 shell 语义。

## 分阶段交付

M3.9 交付策略契约、能力检测、模式解析、审计字段和 fail-closed 验收；Linux/macOS/Windows 后端按可验证程度分别交付。Windows 完整文件/网络隔离进入后续独立阶段。

## 第一阶段实现状态（2026-10-02）

- 已实现 `off`、`auto`、`required` 模式解析与能力选择。
- `run_command` 预览和结果记录沙箱模式、后端及 `sandboxed` 状态。
- 当前 Windows 无可靠 OS 后端，`required` 安全拒绝，`auto` 明确记录未沙箱执行。
- 尚未把 `bwrap` 或 `sandbox-exec` 标记为可靠后端；后续必须先提供并验证受限 profile，再启用实际包装执行。

## Linux 远程验证（2026-10-02）

- 已连接用户提供的 Linux 服务器，确认内核为 `Linux 5.15.0-126-generic x86_64`。
- 服务器未安装 `bwrap`，因此本轮不能声称真实 Linux OS 沙箱已启用；`required` 应保持 fail-closed。
- 仅在 `/tmp/drift-m39-*` 下创建探针目录和文件，验证后删除并确认 `cleanup=ok`；未修改服务器其他文件。
- 服务器没有 Go 工具链，无法在服务器直接运行 Drift 测试；本地 Windows 全量 Go 验收仍通过。
