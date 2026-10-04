# M5.16 MCP 生产化验收

## 自动化验收

| 检查 | 结果 |
| --- | --- |
| `go test ./internal/mcp ./internal/app -run 'TestLoad(AcceptsRetryAndTimeoutPolicy|RejectsUnsafeRetryAndTimeoutPolicy)|TestMCPManagerRetriesAndReportsStatus' -count=1` | PASS |
| `go test ./... -count=1` | PASS |
| `go test -race ./internal/mcp ./internal/tool ./internal/app -count=1` | PASS |
| `go vet ./...` | PASS |
| `go build ./cmd/drift` | PASS |
| `git diff --check`（本次修改范围） | PASS |

## 关键输入输出

配置：

```json
{"servers":[{"name":"demo","transport":"stdio","command":"...","retry_count":1,"timeout_ms":2000}]}
```

第一次初始化失败、第二次成功时：

```text
/mcp connect demo
MCP 已连接：demo

/mcp status
demo · connected · stdio · retry=1 · capabilities-cached
```

非法配置（例如 `retry_count=4` 或 `timeout_ms=20`）在加载阶段拒绝，不启动 MCP 子进程。
