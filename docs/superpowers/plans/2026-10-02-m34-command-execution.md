# M3.4 受控命令执行实施计划

## 交付边界

实现 `chat` 专属 `run_command`，覆盖审批、workspace cwd、超时、输出限制、取消和脱敏审计；不实现 OS 沙箱、永久授权和 Git 回滚。

## 任务

### Task 1：命令契约与权限模型

- 为 `run_command` 增加参数校验、命令/cwd 规范化和 `PermissionRequest` 摘要。
- 复用 M3.3 的 `ask / allow / deny` 选择器和当前 chat 精确记忆。
- 先写 `-p` 无工具、拒绝不启动、allow 仅同 cwd/同命令匹配的失败测试。

### Task 2：进程生命周期

- 使用平台适配的进程启动方式，固定 workspace cwd。
- 增加 context 取消、超时和进程树终止；无法确认终止时返回不确定状态。
- 测试启动失败、非零退出、取消和超时。

### Task 3：输出与审计

- stdout/stderr 独立限流，结果包含字节数、截断标记、退出码和耗时。
- TTY 显示简短开始/完成行，不把完整输出塞进面板。
- 审计只写脱敏命令摘要、状态和安全错误阶段。

### Task 4：测试命令验收

- 运行一个成功测试命令、一个失败测试命令和一个超时命令。
- 验证每次都经过审批，退出 chat 后 allow 失效。
- 验证 `-p` 不执行命令，session 不保存完整命令输出。

### Task 5：证据与文档

- 运行全量 Go 测试、vet、build 和 diff 检查。
- 记录真实 TTY 验收与脱敏审计证据到 `artifacts/verification/m3.4/`。
- M3.4 完成前不把 `spec/current.md` 的当前版本从 M3.3 提升。
