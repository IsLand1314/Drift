# M1.1 验收证据

- 代码版本：`a761e0f`（已推送至 `origin/master`）。
- 自动化证据：`go-test.txt`、`go-vet.txt`、`go-build.txt`、`git-diff-check.txt`。
- 交互证据：`chat-clear.txt`。使用已构建的 `.codex-temp\\drift-m11.exe` 输入 `hello`、`/clear`、`hello`、`exit`，退出码为 0；`/clear` 后第二轮仍能得到回答。
- 上下文超限和清理行为由 `internal/agent`、`internal/app` 的聚焦测试覆盖；测试输出收录在 `go-test.txt`。
- Session 检查：人工查看最新 `.drift/sessions/*.jsonl`，事件只保留 `<redacted>`、安全相对路径和字节数，没有提示词、回答正文、文件内容或绝对路径。

这些证据不包含 API Key 或其他敏感配置。
