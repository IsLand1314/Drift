# M5.12-A 上下文管理设计

## 目标

让长对话在达到预算阈值时自动压缩，同时保留系统约束、当前计划、未完成任务和已验证事实；Session 恢复后能够重新构建可继续执行的 Context，压缩失败时原上下文保持不变。

## 当前基础

Drift 已有 `agent.Runner.Compact`、UTF-8 字节预算、80% 压缩阈值、Session JSONL 审计和工具结果上限。M5.12-A 不重写这些能力，而是补齐状态保留、恢复重建和失败原子性。

## 边界

- Session 是完整审计/恢复来源，不等于当前 Context。
- Context 是发送给 Provider 的动态消息投影。
- 本阶段不实现长期 Memory、向量数据库、外部知识库或 Skill 生命周期。
- 不保存 API Key、完整命令输出、未经脱敏的绝对路径或完整模型原文到摘要元数据。
- 不改变权限模式、沙箱、工具注册和 Coordinator 状态机。

## 数据模型

Context 由以下部分组成：

1. 固定系统约束和当前 Provider/workspace 元数据；
2. 当前计划、未完成 Task 和已验证事实的结构化摘要；
3. 最近若干条对话消息；
4. 当前轮用户输入和必要的工具结果。

压缩摘要必须是普通文本，不得包含工具调用、XML、DSML 或凭据。摘要失败、为空、含伪工具格式或无法解析时，Runner 保留原消息数组。

## 压缩策略

- 使用现有 UTF-8 字节预算作为保守指标，不伪装成 token 计数。
- 达到 `CompactionTriggerBytes` 时，在下一次 Provider 请求前压缩。
- 保留系统上下文、最近消息、计划/Task 状态和已验证事实。
- 工具结果继续遵守既有头尾裁剪和总读取预算。
- 压缩成功后写入结构化 compaction 事件，包含前后字节数和保留消息数，不写摘要正文。
- 压缩失败时不修改消息、不写成功事件，并将错误作为当前轮可恢复失败。

## Session 恢复

恢复流程从 JSONL 读取事件，重建用户、助手、工具调用/结果、计划状态和 Task 快照，再按当前 Provider 与工具注册表重新计算 Context。恢复不得把审计正文直接全部注入 Provider；必须经过同一预算和裁剪策略。

## 验收门槛

- 单元 TDD：阈值、保留策略、空摘要、Provider 错误、伪工具摘要和原子回滚。
- 集成功能：长对话压缩后继续提问；Session 恢复后计划、Task、权限模式和 workspace 不变。
- 安全：摘要和恢复 Context 不含 API Key、完整命令输出或绝对路径。
- 真实 LLM：使用支持原生 Tool Call 的 Provider 验证一次压缩前后连续对话。
- 回归：`go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check`。

## 后续阶段接口

M5.12-B 的短期 Memory 只能从已验证 Context/Session 事件提取候选事实；M5.12-C 的长期 Memory 通过独立 `.drift/memory` 存储检索，不改变 Context 压缩器的职责。
