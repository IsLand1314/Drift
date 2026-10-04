# M5.15 MCP 安全与生命周期验收

## 自动化结果

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | PASS |
| `go test -race ./internal/mcp ./internal/tool ./internal/app -count=1` | PASS |
| `go test ./internal/tool -run 'TestMCPProcess' -count=1 -v` | PASS (Windows AppContainer lifecycle and protected-directory checks) |
| `go vet ./...` | PASS |
| `go build ./cmd/drift` | PASS |
| `git diff --check` | PASS |

覆盖内容：stdio/HTTP/Streamable HTTP 握手、工具/Resource/Prompt 发现与调用、恶意 `file://` 越界 URI、结果大小限制、未信任内容标记、断开/重连（含动态工具清理）、能力缓存、sandbox required 不可用时拒绝，以及取消后的进程清理。

## 运行时边界

- Linux stdio MCP 在 `auto`/`required` 下通过 bwrap 启动，默认关闭网络，并遮蔽 `.drift` 与 `.git`；`network_enabled` 只来自可信配置。
- `off` 模式不启用 OS 沙箱；`required` 在后端不可用时拒绝连接；`auto` 才允许记录 `sandboxed=false` 后回退。
- HTTP MCP 是客户端网络连接，不创建本地子进程，因此不套用 stdio 子进程沙箱。
- Windows stdio MCP 使用 AppContainer + Job Object，自定义进程句柄接入 MCP 客户端；默认无网络，并通过 `internetClient` capability 仅在 `network_enabled=true` 时放行。

## 外部 MCP 验收样例

1. 配置一个本地 stdio server，执行 `/mcp connect demo`：状态为 `connected`，审计包含 `connected` 和能力快照。
2. 执行 `/mcp disconnect demo`：状态变为 `disconnected`，子进程被关闭；再执行 `/mcp reconnect demo` 可恢复 `connected`。
3. 读取 workspace 外的 `file://` URI：工具拒绝，结果带安全错误，不读取外部文件。
4. 返回超过限制的文本或非文本块：调用失败或输出为摘要，不把原始大块内容注入上下文。
5. 使用 `--sandbox required` 且后端不可用：连接拒绝并记录 `sandbox_denied`，不启动 server。
