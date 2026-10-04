# M5.9 真实 LLM 验收记录

日期：2026-10-04
Provider：DeepSeek（配置来源 `E:/DeepSeek.txt`，密钥未写入本记录）
模型：`deepseek-chat` / `deepseek-flash`

## 验收目标

验证结构化计划的真实 LLM 链路：

`ToolSearch → EnterPlanMode → PlanUpdate → ExitPlanMode → PlanExecute → TaskStatus`

## 自动化验收

| 检查 | 结果 |
|---|---|
| `go test ./... -count=1` | PASS |
| `go vet ./...` | PASS |
| `go build ./cmd/drift` | PASS |
| `git diff --check`（本轮代码） | PASS |
| PlanUpdate/PlanExecute TDD 测试 | PASS |
| 控制工具 schema 的 `required` 始终为数组 | PASS |

修复：控制工具 schema 在无必填参数时曾输出 `required: null`，DeepSeek API 会以 HTTP 400 拒绝；现统一输出空数组，并增加回归测试。提交：`ae33984`。

## 真实 LLM 结果

### 成功部分

真实终端会话中，模型成功完成：

1. `ToolSearch` 搜索并加载计划工具；
2. `EnterPlanMode`，进入只读规划状态；
3. `PlanUpdate`，创建 `plan-1791089135099222900`，包含 `task-1` 和工作树路径。

对应脱敏输出示例：

```text
EnterPlanMode executed successfully.
PlanUpdate executed successfully.
Plan ID: plan-1791089135099222900
Tasks: 1 — task-1 (读取 README 并汇报, status=pending, worktree=.worktrees/agent-1)
```

### 未通过部分

完整自动链路未达到 PASS：

- DeepSeek 在部分轮次返回不兼容的 DSML/伪工具调用格式，Agent 正确拒绝并报告 `模型返回了不兼容的伪工具调用格式`；
- 一次真实 `PlanExecute` 调用返回 `tool execution failed`，随后 `TaskStatus` 仍显示 `task-1 pending`。

这不是把失败标为成功：确定性 TaskRunner/PlanExecute 测试已通过，因此当前剩余风险属于 Provider 输出兼容性或真实子 Agent 运行环境问题，需要后续单独定位。

## 结论

M5.9 代码实现和确定性测试通过；真实 DeepSeek 验收为 **PARTIAL**：规划链路已真实打通，完整执行链路受 Provider 伪工具响应和一次子任务执行失败阻断。未记录 API 密钥，未修改用户工作区文件。

## 后续收口

为避免宿主缺少 `TaskRunner` 时任务静默停留在 `pending`，`PlanExecute` 现在会将未完成任务标记为 `failed` 并写入明确原因 `TaskRun is unavailable in this host`。回归测试覆盖该路径，修复提交：`7b145ba`。
