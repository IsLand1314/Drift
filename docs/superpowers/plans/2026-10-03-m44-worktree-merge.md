# M4.4 Worktree 自动合并计划

1. 先用 Git 现有 `MergeWorktree`，不重写合并安全检查。
2. 增加线程安全 `MergeQueue`，自动提交无冲突合并，保存冲突请求并支持重试。
3. 在 TaskRun 完成路径接入合并回调，保存合并状态和验证用 HEAD。
4. 用确定性 Git fixture 覆盖成功、冲突不改主工作区、重复入队和解决后重试。
5. 运行全量测试、race、vet、build 和独立只读 LLM 审查。
