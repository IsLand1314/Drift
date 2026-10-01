# M1.7 单轮取消实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让 `drift chat` 在模型流或只读工具执行期间按 Ctrl+C 只取消当前轮，保留进程、历史上下文和后续输入能力。

**Architecture:** 将进程生命周期 Context 与每轮请求 Context 分离。主程序把 Ctrl+C 作为可注入的中断事件交给 app；chat 在有活动轮次时取消该轮，在空闲输入时结束 chat。Agent 对每轮消息做事务式回滚，取消时不提交半轮上下文；chat 负责将取消转换为安全提示和 `agent_cancelled` 审计事件。

**Tech Stack:** Go 1.26+、`context`、`os/signal`、`bufio.Scanner`、标准库 `testing`/`httptest`。

**Spec:** `docs/superpowers/specs/2026-10-01-m17-turn-cancellation-design.md`

## Global Constraints

- 继续保持当前只读边界：不增加写文件、删除文件、命令执行或新的工具。
- 继续使用标准库，不引入 TUI、行编辑器、输入队列或后台任务依赖。
- Provider、工具、会话和审计的敏感内容不得进入取消错误文本或审计正文。
- 取消只影响当前轮；`/clear`、`/compact`、`/status` 和空闲退出命令保持既有语义。
- 所有提交使用仓库原作者 `IsLand1314`，提交信息使用中文。

---

### Task 1: 建立可注入的中断生命周期

**Files:**
- Modify: `cmd/drift/main.go`
- Modify: `internal/app/app.go`
- Create: `internal/app/interrupt.go`
- Test: `internal/app/interrupt_test.go`

**Interfaces:**
- `RunWithSignals(ctx context.Context, args []string, getenv func(string) string, in io.Reader, out, stderr io.Writer, interrupts <-chan os.Signal) int`
- `Run` 和 `RunWithInput` 保持现有签名，内部传入 nil interrupt channel，保证嵌入和已有测试兼容。
- `cmd/drift` 使用 `signal.Notify` 创建 `chan os.Signal`，不再用 `signal.NotifyContext` 直接取消整个 app Context。

- [ ] **Step 1: 写失败测试**

  在 `interrupt_test.go` 增加一个可控的中断协调器测试：活动轮次存在时，中断调用当前轮 cancel；没有活动轮次时，中断取消 chat 生命周期。测试使用 `context.WithCancel` 和 buffered `chan os.Signal`，断言两个 cancel 函数的触发顺序。

- [ ] **Step 2: 运行测试确认失败**

  运行：`go test ./internal/app -run TestInterruptCoordinator -count=1 -v`

  预期：FAIL，因为中断协调器和 `RunWithSignals` 尚不存在。

- [ ] **Step 3: 实现最小中断接口**

  新建 `interrupt.go`，定义只负责生命周期的内部协调器：保存当前轮 cancel 函数、提供 `beginTurn() func()` 注册/清理当前轮、监听 `interrupts`；收到信号时有活动轮次则调用轮次 cancel，否则调用 chat cancel。`RunWithSignals` 将该协调器传给 chat loop。

  `main.go` 改为：

  ```go
  interrupts := make(chan os.Signal, 1)
  signal.Notify(interrupts, os.Interrupt)
  defer signal.Stop(interrupts)
  code := app.RunWithSignals(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, interrupts)
  ```

- [ ] **Step 4: 运行测试确认通过**

  运行：`go test ./internal/app -run TestInterruptCoordinator -count=1 -v`

  预期：PASS，且 `go test ./internal/app -run 'TestRun|TestChat' -count=1` 不回归。

- [ ] **Step 5: 提交**

  ```powershell
  git add cmd/drift/main.go internal/app/app.go internal/app/interrupt.go internal/app/interrupt_test.go
  git -c user.name=IsLand1314 -c user.email=island0920@163.com commit -m "功能：建立单轮中断生命周期"
  ```

### Task 2: 让 Agent 每轮可事务式回滚

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

**Interfaces:**
- 保持 `(*Runner).RunEvents(ctx, prompt, sink) error` 不变。
- 在 `RunEvents` 内部记录调用前的 `r.messages` 快照；成功时保留本轮消息，任意错误返回时恢复快照。

- [ ] **Step 1: 写失败测试**

  增加两个测试：Provider 在第一次流中因 `context.Canceled` 失败时，Runner 的消息和 `ContextBytes` 与运行前一致；工具执行返回 `context.Canceled` 时，已经追加的 assistant/tool 临时消息也全部回滚。

- [ ] **Step 2: 运行测试确认失败**

  运行：`go test ./internal/agent -run 'TestRunEvents.*Rollback|TestRunEvents.*Cancel' -count=1 -v`

  预期：FAIL，当前实现会在运行开始时直接追加 user 消息，并可能保留工具轮次消息。

- [ ] **Step 3: 实现事务式回滚**

  在 `RunEvents` 入口复制 `r.messages`；使用一个统一的失败出口，在返回非 nil 错误前恢复消息切片。成功的 `EventRunFinished` 路径不回滚。恢复必须使用已有的消息复制 helper，避免调用方持有内部 slice。

- [ ] **Step 4: 运行测试确认通过**

  运行：`go test ./internal/agent -run 'TestRunEvents.*Rollback|TestRunEvents.*Cancel' -count=1 -v`，再运行 `go test ./internal/agent -count=1`。

- [ ] **Step 5: 提交**

  ```powershell
  git add internal/agent/agent.go internal/agent/agent_test.go
  git -c user.name=IsLand1314 -c user.email=island0920@163.com commit -m "修复：取消轮次回滚临时上下文"
  ```

### Task 3: 在 chat 中取消当前轮并继续输入

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/chat_test.go`

**Interfaces:**
- 将 `runChatLoopWithPersistence` 增加一个内部 `interrupt *interruptCoordinator` 参数；外部 `runChatLoop` 继续传 nil。
- 每条普通输入创建 `turnCtx, turnCancel := context.WithCancel(ctx)`，注册到协调器，`defer` 清理。

- [ ] **Step 1: 写失败测试**

  用可控 fake Client：第一次请求等待 `ctx.Done()`，第二次请求返回正常回答。通过 buffered interrupt channel 发送一个 `os.Interrupt`，断言 chat 输出“已取消当前轮；会话仍可继续”、随后第二个问题得到回答，最终退出码为 0。

- [ ] **Step 2: 运行测试确认失败**

  运行：`go test ./internal/app -run TestChatCancelsCurrentTurnAndContinues -count=1 -v`

  预期：FAIL，当前 app 将 `context.Canceled` 转成退出码 130，并结束 chat。

- [ ] **Step 3: 实现 chat 轮次控制**

  在普通问题分支中使用 turn context 调用 `runner.RunEvents`。若错误是当前轮取消：

  ```go
  if errors.Is(err, context.Canceled) && turnCtx.Err() != nil && ctx.Err() == nil {
      appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventError, Stage: "agent_cancelled"})
      fmt.Fprintln(out, "已取消当前轮；会话仍可继续")
      continue
  }
  ```

  取消前保存 `chatPersistence.usage` 快照，取消后恢复，避免未完成轮次的 usage 计入完整会话。成功轮次仍按现有逻辑保存 Runner 和 Token 统计。

- [ ] **Step 4: 运行测试确认通过**

  运行：`go test ./internal/app -run 'TestChatCancelsCurrentTurnAndContinues|TestChat.*Cancel' -count=1 -v`，再运行 `go test ./internal/app -count=1`。

- [ ] **Step 5: 提交**

  ```powershell
  git add internal/app/chat.go internal/app/app.go internal/app/chat_test.go
  git -c user.name=IsLand1314 -c user.email=island0920@163.com commit -m "功能：支持取消当前对话轮次"
  ```

### Task 4: 补齐取消审计、快照和空闲退出边界

**Files:**
- Modify: `internal/session/jsonl_test.go`
- Modify: `internal/conversation/store_test.go`
- Modify: `internal/app/chat_test.go`
- Modify: `doc/m1.7-turn-cancellation.md`
- Modify: `spec/current.md`
- Modify: `README.md`

**Interfaces:**
- 复用现有 `agent.Event{Type: EventError, Stage: "agent_cancelled"}` 和 JSONL 脱敏 Writer，不新增敏感字段。
- 快照格式保持 version 1；取消轮不改变旧快照 schema。

- [ ] **Step 1: 写失败测试**

  增加审计测试，写入取消事件后只能看到 `type`、`stage` 和时间；增加会话测试，取消前后的消息、Token 统计和旧快照兼容值保持预期；增加空闲 Ctrl+C 测试，断言返回码 130。

- [ ] **Step 2: 运行测试确认失败**

  运行：`go test ./internal/session ./internal/conversation ./internal/app -run 'Cancel|Cancelled|Interrupt' -count=1 -v`

  预期：至少 chat 的空闲退出/审计断言失败，促使边界行为固定下来。

- [ ] **Step 3: 实现最小边界修正**

  只补齐测试暴露的缺口：取消错误统一使用 `agent_cancelled`；审计继续走现有脱敏字段；取消后不调用 `saveRunner`；空闲中断通过 chat 生命周期 Context 返回 130。不得加入正文历史、自动压缩或 TUI。

- [ ] **Step 4: 更新阶段文档**

  在 `doc/m1.7-turn-cancellation.md` 记录命令示例、取消与退出差异和人工验收；在 `spec/current.md` 将当前版本更新为 M1.7，并增加 AC-M17 表；README 增加 Ctrl+C 语义和边界链接。

- [ ] **Step 5: 运行测试确认通过**

  运行：`go test ./internal/session ./internal/conversation ./internal/app -count=1`。

- [ ] **Step 6: 提交**

  ```powershell
  git add internal/session/jsonl_test.go internal/conversation/store_test.go internal/app/chat_test.go doc/m1.7-turn-cancellation.md spec/current.md README.md
  git -c user.name=IsLand1314 -c user.email=island0920@163.com commit -m "文档：补充 M1.7 取消验收边界"
  ```

### Task 5: 全量验证与验收证据

**Files:**
- Create: `artifacts/verification/m1.7/cancel-test.txt`
- Create: `artifacts/verification/m1.7/audit-test.txt`
- Create: `artifacts/verification/m1.7/full-check.txt`

- [ ] **Step 1: 运行取消专项测试并记录证据**

  运行 `go test ./internal/agent ./internal/app -run 'Cancel|Interrupt|Rollback' -count=1 -v`，使用 `capture_command.py` 保存到 `cancel-test.txt`。

- [ ] **Step 2: 运行审计与快照测试并记录证据**

  运行 `go test ./internal/session ./internal/conversation -run 'Cancel|Cancelled|Snapshot' -count=1 -v`，保存到 `audit-test.txt`。

- [ ] **Step 3: 运行全量检查并记录证据**

  依次运行 `go test ./... -count=1`、`go vet ./...`、`go build -o .codex-temp\drift-m17.exe ./cmd/drift`、`git diff --check`，保存到 `full-check.txt`。

- [ ] **Step 4: 人工验收**

  启动 `go run ./cmd/drift chat -w .`，在模型持续输出期间按 Ctrl+C，确认 chat 继续显示 `❯`；再次提问确认可回答；输入 `/status` 确认历史 Context/Token 未被清空；退出后检查最新快照和 JSONL，不出现取消轮正文。

- [ ] **Step 5: 提交证据**

  ```powershell
  git add artifacts/verification/m1.7
  git -c user.name=IsLand1314 -c user.email=island0920@163.com commit -m "验收：完成 M1.7 单轮取消验证"
  ```

## 执行顺序

按 Task 1 → Task 2 → Task 3 → Task 4 → Task 5 顺序执行。每个 Task 完成后先运行该 Task 的专项测试，再进入下一个 Task；最后执行全量测试和人工验收。

