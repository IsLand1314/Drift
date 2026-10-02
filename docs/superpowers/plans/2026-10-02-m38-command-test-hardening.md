# M3.8 命令与测试执行实施计划

1. 以现有 `run_command` 测试为基线，补齐结果状态、输出限制、测试命令和只读边界测试。
2. 检查 Windows/Unix 进程树取消测试，避免重复实现平台逻辑。
3. 运行全量自动验收并生成 `artifacts/verification/m3.8/` 证据。
4. 同步 `spec/current.md`，确认 OS 沙箱和 Git 回滚仍是后续阶段。
