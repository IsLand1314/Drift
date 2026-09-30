# M0.9 Session 审计查看器实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `.drift/sessions/*.jsonl` 明确定义为安全审计记录，并提供只读的 `session list/show` 查看命令。

**Architecture:** Agent 继续产生完整的内存事件，但 Session Writer 只保存事件元数据、字节数和稳定错误阶段，不保存用户提示词、工具参数、文件内容或模型回答正文。查看器读取 JSONL 后只输出摘要；本阶段不实现把 Session 恢复给 LLM 的功能。

**Tech Stack:** Go 标准库 `flag`、`bufio`、`encoding/json`、`os`、`filepath`、`sort`、`testing`。

**Spec:** `spec/current.md` 的 M0.9 章节；阶段说明为 `doc/m0.9-session-audit.md`。

## Global Constraints

- 继续使用 Go 1.26+ 和标准库，不新增依赖。
- Session 是本地审计记录，不是可直接发送给 LLM 的上下文。
- 新写入记录不保存提示词、原始工具 arguments、工具结果内容或回答正文。
- 已知只读工具可以保存经过校验的相对 `path`，但不保存原始 arguments。
- 运行完成或异常结束时可保存经过校验的 `finish_reason`，不保存 Provider 正文。
- 兼容读取旧 JSONL；查看旧记录时也只输出摘要，不回显旧的正文内容。
- `session list/show` 只读本地文件，不请求 Provider、不需要 API Key、不修改文件。
- 所有行为变更先写失败测试，再写最小实现。

---

### Task 1: 收紧 Session 写入并增加读取 API

**Files:**
- Modify: `internal/session/session.go`
- Modify: `internal/session/jsonl.go`
- Create: `internal/session/read.go`
- Test: `internal/session/jsonl_test.go`
- Test: `internal/session/read_test.go`

**Interfaces:**
- Produces: `Entry.TextBytes`、`Entry.ArgumentBytes`、`Entry.ResultBytes`。
- Produces: `ReadEntries(path string) ([]Entry, error)`。
- Produces: `ListFiles(root string) ([]string, error)`。

- [x] **Step 1: Write failing tests**

新增测试：事件中的提示词、arguments 和 result 不出现在 JSONL；对应字节数仍被记录；`ReadEntries` 能读取一行一条 JSON；`ListFiles` 只返回 `run-*.jsonl` 并按最新修改时间排序。

- [x] **Step 2: Run focused tests and confirm RED**

```powershell
go test ./internal/session -run "TestJSONLWriterStoresAuditMetadataOnly|TestReadEntries|TestListFiles" -count=1
```

Expected: FAIL because metadata fields and read functions are not implemented.

- [x] **Step 3: Implement minimal audit writer and reader**

新记录使用 `<redacted>` 或空字符串替代内容，只写 `TextBytes`、`ArgumentBytes`、`ResultBytes`；`Error` 继续使用现有脱敏器，`Stage` 保留。读取器设置足够的 Scanner buffer，错误包含文件和行号但不回显行内容。

- [x] **Step 4: Run focused tests and existing session tests**

```powershell
go test ./internal/session -count=1
```

Expected: PASS；旧 JSONL 可以解析，新 JSONL 不含 fixture 中的提示词、路径、密钥和文件正文。

### Task 2: 增加 `session list/show` 只读命令

**Files:**
- Create: `internal/app/session_command.go`
- Create: `internal/app/session_command_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Consumes: `session.ListFiles`、`session.ReadEntries`。
- Produces: `Run` 支持 `session list` 与 `session show <path>`，不加载 `.env`、不创建 Provider。

- [x] **Step 1: Write failing command tests**

覆盖：`session list` 输出会话文件名和事件数量；`session show <path>` 输出事件摘要、工具名、结果字节数和错误 stage；输出不包含旧记录中的 prompt、arguments 或 result；缺少路径和不存在文件返回退出码 2。

- [x] **Step 2: Run tests and confirm RED**

```powershell
go test ./internal/app -run "TestSessionList|TestSessionShow" -count=1
```

Expected: FAIL because `session` is currently treated as an invalid prompt invocation.

- [x] **Step 3: Implement command routing and formatting**

在 `app.Run` 最前面识别 `session` 子命令。`list` 默认查看当前目录 `.drift/sessions`；`show` 接受用户明确给出的 JSONL 路径。查看器只打印：事件类型、工具名、`text_bytes`、`argument_bytes`、`result_bytes`、错误 stage 和时间，不打印正文。

- [x] **Step 4: Run focused command tests**

```powershell
go test ./internal/app -run "TestSessionList|TestSessionShow|TestRunDirectAnswer" -count=1
```

Expected: PASS；普通 `-p` 运行行为不变。

### Task 3: 更新 M0.9 文档和代码理解入口

**Files:**
- Create: `doc/m0.9-session-audit.md`
- Modify: `spec/current.md`
- Modify: `README.md`
- Modify: `doc/Process/代码理解.md`

- [x] **Step 1: 写清 Session 语义**

说明 `.drift/sessions` 是 audit，不是 LLM context；给出 list/show 命令和脱敏字段示例。

- [x] **Step 2: 更新当前规格和导航**

将当前版本改为 M0.9，写入安全边界、兼容旧 JSONL、无 Provider 请求和验收命令；README 增加 M0.9 链接。

- [x] **Step 3: 更新代码理解文档**

补充 Agent Event → Session Audit → session show 的数据流，明确未来会话恢复必须另行设计并显式授权。

### Task 4: 全量验证

**Files:**
- Test: all Go packages.

- [x] **Step 1: Run verification**

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

Expected: 全部退出码为 0。

- [x] **Step 2: Manual verification**

```powershell
go run ./cmd/drift session list
go run ./cmd/drift session show .drift/sessions/<run-file>.jsonl
```

Expected: 能看到事件摘要和字节数，看不到提示词、文件内容、工具原始参数、API Key 或绝对路径；命令不请求 Provider。
