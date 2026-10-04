# M5.14 验收记录

## 本地临时 MCP Server

- `TestHTTPClientSupportsToolsResourcesAndPrompts`：PASS
  - initialize / notifications/initialized
  - tools/list / tools/call
  - resources/list / resources/read
  - prompts/list / prompts/get
- `TestAttachMCPRegistersResourceAndPromptToolsLazily`：PASS
  - ToolSearch 加载 `mcp__demo__resource_read`
  - ToolSearch 加载 `mcp__demo__prompt_get`
  - Resource/Prompt 只读调用成功

临时 HTTP Server 由测试自动启动并关闭，未修改 workspace `.drift/mcp.json`，无残留进程。

## 回归

- `go test ./... -count=1`：PASS
- MCP/App/Tool race tests：PASS
- `go vet ./...`：PASS
- `go build ./cmd/drift`：PASS
- `git diff --check`：PASS

