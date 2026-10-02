# M3.8 命令与测试执行收敛设计

## 目标

在现有 `run_command` 基础上完成命令和测试运行的稳定验收，不新增重复的 `run_test` 工具。

## 范围

- `go test ./...`、`go vet ./...`、`go build ./cmd/drift` 继续通过 `run_command` 执行。
- 固定结果字段：`status`、`exit_code`、`duration`、stdout/stderr 字节数、`truncated`。
- 明确区分成功、非零退出、超时、取消和启动失败。
- 保持 workspace cwd、受保护目录、超时上限、输出上限和审批策略。
- `-p` 仍只读，不注册 `run_command`。
- 审计只保存脱敏元数据，不保存完整命令输出。

## 非目标

- 不新增 `run_test` 工具。
- 不实现 OS 沙箱、容器、网络隔离或 Git 回滚。
- 不提供命令白名单、后台任务或永久全局授权。

## 验收

- 命令成功和非零退出均返回稳定状态与退出码。
- 超时和 Ctrl+C 取消能收敛进程树，chat 可继续。
- stdout/stderr 达到上限时返回 `truncated=true`。
- 越界 cwd、受保护目录、符号链接和非法参数全部拒绝。
- `-p` 不会暴露命令工具。
- 全量 Go 测试、vet、build、diff 检查通过。
