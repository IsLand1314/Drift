# Task 6 report

状态：DONE

- 更新 App 既有单轮/多文件测试，探索轮继续携带三个默认工具 schema。
- 新增四轮 SSE 端到端验收：`list_files` → `search_text` → `read_file` → 最终文本。
- 校验每轮模型上下文、工具顺序、stdout 仅输出最终回答，以及 JSONL 中三组工具调用/结果、文本和结束事件。
- 修正测试中对后续请求消息数量的假设：system 仅在首轮临时注入，后续上下文不重复携带 system。

验证：

- `go test ./internal/app ./internal/session -count=1`

结果：通过。
