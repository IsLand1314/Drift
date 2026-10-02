# M3.6 人工验收记录

## 自动化等价验收

- `internal/app` 权限策略、审批和 TUI 等价测试：通过；
- `go test ./... -count=1`：通过；
- `go vet ./...`：通过；
- `go build ./cmd/drift`：通过；
- `git diff --check`：通过。

## 真实终端复核步骤

在一个全新的 workspace 中执行：

1. `go run ./cmd/drift chat -w <workspace>`；
2. 创建文件，选择 `1. Yes`，退出 chat 后再次创建同一路径，必须重新审批；
3. 创建另一个文件，选择 `2. Yes, and don't ask again for this pattern`；
4. 退出并重新进入 chat，对同一工具、操作和路径执行，必须不再审批；
5. 修改路径、操作、命令或 cwd 任一字段，必须重新审批；
6. 输入 `/permissions`，只显示工具、操作和相对目标摘要；
7. 输入 `/permissions clear`，再次执行同一操作必须重新审批；
8. 手动写入损坏或未知版本的 `.drift/permissions.json`，重新进入 chat，必须安全降级为审批。

本记录不伪造外部 Provider 的人工操作结果；自动化等价路径已通过，真实终端复核按上述步骤执行即可。
