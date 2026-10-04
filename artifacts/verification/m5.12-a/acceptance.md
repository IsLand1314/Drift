# M5.12-A 验收记录

状态：PASS

已通过：

- 工具调用边界不会被压缩切断；
- 自动压缩发生在请求预算溢出前；
- 空摘要、伪工具摘要和 Provider 错误均原子回滚；
- 全量测试、race、vet、build 通过；
- 构建临时产物已清理。

真实 LLM 验收：使用 `deepseek-chat` 原生 Tool Call Provider，通过 chat 连续发送两轮消息、执行 `/compact`，输出 `上下文已压缩：保留最近 4 条消息`；随后正常退出。该次使用 `--no-session`，生成的两条临时审计文件已删除。
