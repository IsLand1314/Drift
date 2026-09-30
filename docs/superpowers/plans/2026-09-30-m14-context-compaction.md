# M1.4 Context Compaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `drift chat` 增加手动 `/stats` 和 `/compact`，让长会话在不完全清空的情况下继续工作。

**Architecture:** `agent.Runner` 提供只读统计和原子压缩 API；压缩请求使用当前 Provider 但不提供工具 schema，成功后以摘要消息和最近完整消息替换旧上下文。`internal/app/chat.go` 负责命令交互、审计和持久化回滚，不改变 M1.2 的恢复协议。

**Tech Stack:** Go 标准库、现有 `internal/agent`、`internal/app`、`internal/session`、`internal/conversation`、OpenAI Compatible Provider；不新增依赖。

**Spec:** `docs/superpowers/specs/2026-09-30-m14-context-compaction-design.md`

## Global Constraints

- `/stats` 不请求 Provider、不写审计事件、不修改快照。
- `/compact` 请求不带 tools，不执行文件工具；压缩失败时 Runner 消息必须完全保持不变。
- 摘要请求和摘要结果可能包含敏感内容；审计/trace 只能记录计数、字节数和稳定错误 stage，不记录正文。
- 成功压缩后持久模式必须更新 `.drift/conversations/<id>.json`；保存失败时恢复旧内存消息。
- 保留 M1.1 的 1 MiB 上限、M1.2 的 `/clear`、`--resume`、`--no-session` 和只读工具边界。
- 不实现自动压缩、后台任务、摘要树、fork、`/undo`、正文搜索、导出、加密或云同步。

---

### Task 1: Runner 统计与原子压缩 API

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/event.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**
- Add `const EventCompactionStarted`, `EventCompactionFinished`, `EventCompactionError`.
- Add `type CompactResult struct { Summary llm.Message; KeptMessages []llm.Message; BeforeBytes int; AfterBytes int }`.
- Add `func (r *Runner) Compact(ctx context.Context) (CompactResult, error)`.
- Existing `Messages`, `ContextBytes`, `ResetContext` and `RunEvents` signatures remain unchanged.

- [ ] **Step 1: 写失败测试**

增加测试覆盖：

```go
func TestRunnerCompactUsesNoToolsAndKeepsRecentMessages(t *testing.T) {
	// 用 scriptedClient 返回一条摘要；预置两轮 user/assistant 消息。
	// 断言请求 Tools 为空，压缩后保留 summary 和最近消息。
}
```

同时覆盖 Provider 失败、空摘要、伪工具文本、消息不足和取消；每个失败用例都断言 `runner.Messages()` 与调用前深度相等。

- [ ] **Step 2: 运行 RED**

运行：`go test ./internal/agent -run 'TestRunnerCompact' -count=1 -v`。预期因压缩 API 和事件常量不存在而失败。

- [ ] **Step 3: 实现最小压缩逻辑**

使用现有 `llm.Client.Stream` 发起无 tools 请求；构造固定摘要指令，拒绝空响应和 DSML 文本。先复制旧消息，成功后按完整 user/assistant/tool 交互组保留最近两轮，再原子替换 `r.messages`。Provider 错误、取消或格式错误直接返回，不修改 Runner。

- [ ] **Step 4: 运行 Agent 回归**

运行：`go test ./internal/agent -count=1`。预期 M1.1 上下文限制、工具循环、取消和 DSML 守卫全部通过。

- [ ] **Step 5: 提交**

```powershell
git add internal/agent/agent.go internal/agent/event.go internal/agent/agent_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：增加手动上下文压缩 API" -m "参与人：island"
```

### Task 2: 压缩审计与 Trace 安全摘要

**Files:**
- Modify: `internal/session/session.go`
- Modify: `internal/session/jsonl.go`
- Modify: `internal/session/session_test.go`
- Modify: `internal/app/trace.go`
- Modify: `internal/app/trace_test.go`

**Interfaces:**
- `session.Entry` 增加 `BeforeBytes`、`AfterBytes`、`MessageCount`、`KeptMessages` 等可选计数 JSON 字段。
- `session.Writer.Append` 继续只接收 `agent.Event`，由事件字段承载计数，不保存摘要正文。
- Trace 对三个新事件输出事件名和计数，不输出 `Event.Text` 或摘要内容。

- [ ] **Step 1: 写失败测试**

构造 compaction start/finish/error 事件，断言 JSONL 包含计数和 stage，不包含摘要文本；trace 断言不含敏感正文。

- [ ] **Step 2: 运行 RED**

运行：`go test ./internal/session ./internal/app -run 'Test(Compaction|Trace)' -count=1 -v`。预期新事件未被安全映射。

- [ ] **Step 3: 实现脱敏映射**

在 Session Writer 和 Trace Sink 中只写 `type`、字节数、消息数、保留数和稳定 `stage`；忽略摘要正文。兼容旧 JSONL 读取。

- [ ] **Step 4: 回归测试**

运行：`go test ./internal/session ./internal/app -run 'Test(Compaction|Trace|Session)' -count=1 -v`。

- [ ] **Step 5: 提交**

```powershell
git add internal/session/session.go internal/session/jsonl.go internal/session/session_test.go internal/app/trace.go internal/app/trace_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "安全：记录上下文压缩摘要事件" -m "参与人：island"
```

### Task 3: Chat 命令与持久化生命周期

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- `/stats` 输出 `ContextBytes`、`agent.MaxConversationBytes`、剩余字节、消息数，不请求 Provider。
- `/compact` 调用 `runner.Compact`；成功后调用现有 `chatPersistence.saveRunner`。
- 压缩保存失败时恢复压缩前消息和快照，不丢失当前上下文。

- [ ] **Step 1: 写失败的 chat 测试**

使用 httptest Provider 覆盖：`/stats` 零请求；`/compact` 请求 body 无 `tools`；成功后下一轮只携带摘要/最近消息；Provider 错误和保存失败后仍可继续原上下文；持久 chat 退出后 `--resume` 恢复压缩结果；临时模式压缩不写完整快照。

- [ ] **Step 2: 运行 RED**

运行：`go test ./internal/app -run 'TestChat(Stats|Compact)' -count=1 -v`。预期当前 chat 将 `/stats`/`/compact` 当作普通输入或不支持。

- [ ] **Step 3: 实现命令分支**

在普通 prompt 发送给 Runner 前识别 `/stats` 和 `/compact`；保留 `clear` 提示和 `/clear` 行为。压缩开始/完成/错误事件先进入既有审计和 trace，再显示安全提示；摘要正文只交给 Runner，不直接打印到终端。

- [ ] **Step 4: 运行 App 回归**

运行：`go test ./internal/app -count=1 -v`。预期 M1.0 多轮、M1.1 `/clear`、M1.2 resume/no-session 和 M1.3 conversation 命令全部通过。

- [ ] **Step 5: 提交**

```powershell
git add internal/app/app.go internal/app/chat.go internal/app/chat_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：支持手动查看和压缩上下文" -m "参与人：island"
```

### Task 4: 规格、文档与最终验收

**Files:**
- Create: `doc/m1.4-context-compaction.md`
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/Process/代码理解.md`
- Modify: `docs/superpowers/plans/2026-09-30-m14-context-compaction.md`

- [ ] **Step 1: 写阶段文档和验收表**

加入可复制命令：

```powershell
go run ./cmd/drift chat -w .
# 输入 /stats
# 输入 /compact
go run ./cmd/drift chat --resume <id> -w .
```

明确摘要不是安全擦除证明，完整快照可能含敏感内容，`/clear` 与 `/compact` 的差异，以及压缩失败保留旧上下文。

- [ ] **Step 2: 运行文档检查**

运行：`git diff --check`。预期退出码 0，文档中的命令与实现一致。

- [ ] **Step 3: 全量验证**

运行：`go test ./... -count=1`、`go vet ./...`、`go build -o .codex-temp\\drift-m14.exe ./cmd/drift`、`git diff --check`。预期四条命令均退出 0。

- [ ] **Step 4: 提交**

```powershell
git add README.md spec/current.md doc/m1.4-context-compaction.md doc/Process/代码理解.md docs/superpowers/plans/2026-09-30-m14-context-compaction.md
git -c user.name=island -c user.email=island0920@163.com commit -m "文档：补充 M1.4 上下文压缩说明" -m "参与人：island"
```
