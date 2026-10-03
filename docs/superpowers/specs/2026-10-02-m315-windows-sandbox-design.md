# M3.15 Windows OS 沙箱设计

## 1. 目标

为 Drift 的 `Bash` 提供可验证的 Windows OS 沙箱后端，只覆盖 Windows 和 Linux。Windows 后端必须同时约束文件访问、网络能力和子进程生命周期；现有 Job Object 继续负责进程树清理，但不单独声称自己是完整沙箱。

macOS 本阶段不实现 OS 沙箱后端：能力检测直接返回无后端能力，`auto` 继续执行但记录 `sandboxed=false`，`required` 明确拒绝执行。

## 2. 不变量

- workspace 边界、`.drift`/`.git` 保护、符号链接校验和命令审批仍由 Drift 控制层负责；OS 沙箱不能替代这些检查。
- `bypassPermissions` 只能跳过审批，不能关闭或绕过沙箱策略。
- `required` 只有在后端探针证明完整能力后才允许执行，不能把部分隔离静默标记为可靠后端。
- 沙箱策略固定在控制层，不出现在模型可控的 `Bash` 参数中。
- 失败、取消和超时必须保留现有 `failed`、`cancelled`、`timeout` 语义。

## 3. 后端模型

### Linux

继续使用已验证的 `bwrap`：只读根、workspace bind、独立临时目录、网络 namespace 和 `--die-with-parent`。M3.13/M3.14 已覆盖运行时边界、取消和超时。

### Windows

目标后端由四层组成：

1. **AppContainer 身份**：为每次命令创建临时 AppContainer profile/SID，不授予网络、设备和用户目录能力。
2. **Workspace ACL**：仅向该 SID 授予 workspace 和受控临时目录的必要读写权限；命令结束后撤销并删除临时身份。
3. **Job Object**：复用现有实现，设置 `KILL_ON_JOB_CLOSE`，并将 AppContainer 进程加入同一 Job，确保 Ctrl+C/超时终止整棵进程树。
4. **环境收缩**：只传递必要环境变量，清除凭据、用户目录和不必要的搜索路径；工作目录固定到 workspace。

本阶段不使用全局 Windows 防火墙规则。网络隔离必须由 AppContainer 能力模型和探针共同证明；无法证明时不能把后端标记为可靠。

### macOS

`DetectSandbox()` 对 macOS 返回空 `SandboxCapabilities`（`Backend` 为空、`Reliable=false`、`Capabilities=nil`）。不调用 `sandbox-exec`，不生成伪造 profile，也不把 macOS 进程组清理当作 OS 沙箱。

## 4. 能力探针

Windows 后端只有在一次临时探针全部通过时才报告 `Reliable=true`：

- workspace 内创建和读取文件成功；
- workspace 外部文件创建失败；
- `.drift`、`.git` 写入失败；
- 网络访问失败；
- 子进程可被 Job Object 完整终止；
- 超时后无延迟写入残留；
- 结果状态和退出码可被 Drift 正确采集。

探针失败时返回具体原因供审计使用，但不得执行未经沙箱的 `required` 命令。

## 5. 运行流程

```text
Bash Preview
  -> 校验 workspace/cwd/保护目录
  -> DetectSandbox()
  -> Windows AppContainer probe
  -> SelectSandbox(off/auto/required)
  -> 创建临时 AppContainer + ACL + Job
  -> 启动命令
  -> 绑定 stdout/stderr 限制
  -> 取消/超时关闭 Job
  -> 撤销 ACL、删除 profile
  -> 写入 sandbox backend/capabilities/status 审计
```

清理失败必须记录为错误；不能因为命令本身成功就吞掉 profile、ACL 或 Job 清理失败。

## 6. 审计字段

沿用现有字段并补充 Windows 能力：

```text
sandbox_mode=auto|required|off
sandbox_backend=windows-appcontainer|bwrap|空
sandboxed=true|false
sandbox_capabilities=workspace-write,network-isolated,process-tree
sandbox_probe=passed|failed:<reason>|unavailable
```

macOS 使用 `sandbox_backend=`、`sandboxed=false`、`sandbox_probe=unavailable`。

## 7. 测试策略

### 单元测试

- macOS 能力检测返回空后端；
- Windows 模式选择在后端不可用时 `required` 失败、`auto` 不阻塞；
- 模型不能通过工具参数修改沙箱模式；
- 审计字段包含 backend、capabilities 和 probe 结果。

### Windows 集成测试

在 Windows runner 上验证：

- workspace 内写入成功；
- workspace 外、`.drift`、`.git` 写入失败；
- 网络请求失败；
- Ctrl+C 和 timeout 终止父子进程；
- 临时 profile、ACL、Job 全部清理；
- `required` 不会降级执行。

### Linux 回归

保留 M3.13/M3.14 的 `bwrap` 集成测试；Windows 后端改动不得改变 Linux 结果。

## 8. 非目标

- 不实现 macOS 沙箱；
- 不引入 Node `sandbox-runtime`；
- 不使用全局防火墙规则；
- 不将 Job Object 单独宣传为文件/网络沙箱；
- 不在本阶段实现 Git worktree 或远程执行。

## 9. 分阶段实现

1. M3.15-A：实现 Windows 能力检测、AppContainer 探针和 fail-closed 选择。
2. M3.15-B：接入 AppContainer token、workspace ACL 和 Job Object 生命周期。
3. M3.15-C：补 Windows 集成测试、审计和清理失败语义。

只有 M3.15-C 全部通过后，Windows 后端才可在 `required` 模式下报告可靠。

## 10. M3.15-A 交付状态

M3.15-A 已交付：平台检测边界已显式化，Windows 和 macOS 在没有完整后端时均 fail-closed，Linux `bwrap` 行为保持不变。AppContainer token、workspace ACL、网络隔离和 Windows runtime 验收均明确留到 M3.15-B/C。

## 11. M3.15-B 交付状态

M3.15-B 已完成 Windows 原生探针：沙箱检测接收实际 workspace 根路径，创建临时 AppContainer profile，授予 marker 文件显式 ACL，使用 `SECURITY_CAPABILITIES` 启动进程并加入 Job Object。探针会验证 workspace 写入、`.drift/.git` 拒绝、网络无 `TTL=`、子进程 containment 和 profile/ACL/marker 清理；任一项失败都返回 unavailable。

Job Object 只负责进程树生命周期，AppContainer 负责文件和网络隔离；两者缺一不可。

## 12. CubeSandbox 参考边界

CubeSandbox 采用 KVM MicroVM、独立控制面/执行面、快照回滚和 egress 网关，适合作为未来远程 Linux 执行后端；它要求 x86_64 Linux 与 KVM，不是 Windows 本地沙箱替代品。Drift 当前不引入 CubeSandbox、云服务器、远程 workspace 同步或新的 `SandboxExecutor` 抽象；只有明确进入远程执行阶段后再单独立项。

参考：<https://github.com/TencentCloud/CubeSandbox/blob/master/docs/architecture/overview.md>
