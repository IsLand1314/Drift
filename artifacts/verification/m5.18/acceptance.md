# M5.18 真实 LLM 工具链验收

日期：2026-10-04

## DeepSeek

命令：

```powershell
$env:DRIFT_PROVIDER='deepseek'
go run ./cmd/drift chat -provider deepseek -w . --trace
```

输入：严格要求先 `ToolSearch` 搜索并加载 `ReadFile`，再读取 `README.md`。

观察到的脱敏链路：

```text
run_started
tool_call ToolSearch
tool_result ToolSearch bytes=84
tool_call ToolSearch
tool_result ToolSearch bytes=109
tool_call ReadFile
tool_result ReadFile bytes=12351
run_finished
```

同一 Provider 的追加验收还观察到：

```text
ToolSearch → WriteFile → permission_decision allowed=true → tool_result WriteFile
ToolSearch → Bash → permission_decision allowed=true → tool_result Bash
ToolSearch → mcp__demo__echo → permission_decision allowed=true → tool_result "mcp-echo:deepseek-mcp"
```

其中 WriteFile 创建了临时 workspace 的 `result.txt`，Bash 输出 `deepseek-bash`，MCP stdio helper 返回 `mcp-echo:deepseek-mcp`。结论：DeepSeek 真实产生并执行了原生 ToolSearch、WriteFile、Bash 和 MCP tool call；模型读到的 README 旧版本文本也反向证明 M5.17 文档同步是必要的。

## OpenAI Compatible

命令同上，将 Provider 改为 `openai`；结果为：

```text
错误：模型认证失败：HTTP 401，请检查当前 Provider 的 API Key 是否有效
```

结论：本轮 OpenAI Compatible 未进入工具链，不能报告通过；需要修复 `.drift/auth.json` 或对应环境变量后重跑。

## 失败与恢复矩阵

| 场景 | 证据 | 结论 |
| --- | --- | --- |
| 伪工具 DSML/XML | `internal/agent/*_test.go` 的 DSML 守卫测试 | 拒绝执行并返回不兼容格式错误 |
| 用户拒绝 | `internal/app`/`internal/agent` 审批测试 | 不写入、不执行，轮次可继续 |
| Bash 超时/进程树 | `internal/tool/command*_test.go` | 标记 timeout 并清理子进程 |
| MCP 首次失败后重试 | `TestMCPManagerRetriesAndReportsStatus` | 有限重试后 connected，状态/审计可见 |
| 取消后恢复 | agent/chat 取消测试 | 当前轮取消，会话继续 |

这些是确定性回归证据，不伪装成真实 Provider 证据。
