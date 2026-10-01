# M2.3 Session Switching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `drift chat` 进程内增加 `/resume` 会话选择、`/resume <id>` 直接切换和 `/new` 新建会话，同时保证切换失败不破坏当前上下文。

**Architecture:** 复用现有 `conversation.Store` 和 `chatPersistence`，新增一个只负责本地列表过滤与选择的 Bubble Tea 选择器。会话切换采用 staging：先读取并校验目标快照，成功后一次性替换 Runner、快照和 usage；审计仍是进程级脱敏 JSONL，不作为恢复来源。

**Tech Stack:** Go 1.26、标准库、Bubble Tea、Bubbles textarea、Lip Gloss、现有 `internal/agent` 与 `internal/conversation`。

**Spec:** `doc/m2.3-session-switching.md`

## Global Constraints

- 只保留 `list_files`、`search_text`、`read_file` 三个只读工具；不增加写文件、编辑文件、删除文件、shell 或 exec。
- 完整会话只从当前 workspace 的 `.drift/sessions/conv-<id>.json` 恢复。
- 脱敏审计只写入 `.drift/audits/run-*.jsonl`，不参与恢复，也不在选择器中展示正文。
- 不修改现有 JSON 快照格式，不引入树状会话、`/tree`、`/fork` 或 `clone`。
- 目标快照加载失败、ID 非法或用户按 Esc 取消时，当前 Runner、Session ID、usage 和输入草稿保持不变。
- 只有当前轮次空闲时才能切换；模型流、工具调用或 `/compact` 期间不得替换会话。
- `--no-session` 不创建或修改完整快照；`/new` 只重置当前进程内存上下文。

---

### Task 1: 为会话列表增加本地预览元数据

**Files:**
- Modify: `internal/conversation/store.go`
- Modify: `internal/conversation/store_test.go`
- Modify: `internal/app/conversation_command_test.go`

**Interfaces:**
- `conversation.Metadata` 新增 `Preview string`，只用于本地 UI/CLI 展示，不写入 JSON。
- `Store.List()` 从完整快照的第一条 user 消息生成单行预览，空会话使用空字符串。

- [ ] **Step 1: 写失败测试**

在 `internal/conversation/store_test.go` 增加测试：创建包含多行 user 消息的快照，调用 `List()`，断言 `Metadata.Preview` 为折叠后的单行文本，并且 `Store.Load()` 的持久化格式没有新增 `preview` 字段。

```go
func TestListIncludesLocalPreviewWithoutPersistingIt(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	snapshot, err := store.Create("")
	if err != nil { t.Fatal(err) }
	snapshot.Messages = []llm.Message{{Role: "user", Content: "第一行\n第二行"}}
	if err := store.Save(snapshot); err != nil { t.Fatal(err) }
	items, err := store.List()
	if err != nil { t.Fatal(err) }
	if got := items[0].Preview; got != "第一行 第二行" { t.Fatalf("preview=%q", got) }
	raw, err := os.ReadFile(filepath.Join(root, ".drift", "sessions", snapshot.ID+".json"))
	if err != nil { t.Fatal(err) }
	if bytes.Contains(raw, []byte(`"preview"`)) { t.Fatal("preview must not be persisted") }
}
```

测试实现时使用 Store 实际保存路径读取 JSON，并断言原始 JSON 不包含 `preview`；不要用伪造的临时路径替代真实快照路径。

- [ ] **Step 2: 运行测试确认失败**

运行：`go test ./internal/conversation -run TestListIncludesLocalPreviewWithoutPersistingIt -count=1`

预期：FAIL，原因是 `Metadata` 尚无 `Preview` 字段或列表未生成预览。

- [ ] **Step 3: 实现最小预览逻辑**

在 `Metadata` 增加非持久化字段 `Preview`；在 `List()` 构造 metadata 时从第一条 user 消息提取内容，替换换行为空格，合并连续空白，并按显示宽度或安全字符上限截断。保持 Snapshot JSON 编解码结构不变。

- [ ] **Step 4: 运行测试确认通过**

运行：`go test ./internal/conversation -run 'TestListIncludesLocalPreviewWithoutPersistingIt|TestStore' -count=1`

预期：PASS。

- [ ] **Step 5: 提交独立变更**

```powershell
git add internal/conversation/store.go internal/conversation/store_test.go internal/app/conversation_command_test.go
git commit -m "功能：为会话列表增加本地预览"
```

### Task 2: 实现可搜索、可取消的 Resume 选择器

**Files:**
- Create: `internal/app/chat_resume.go`
- Create: `internal/app/chat_resume_test.go`
- Modify: `internal/app/chat_input.go`

**Interfaces:**
- `runChatResumePicker(in io.Reader, out io.Writer, items []conversation.Metadata) (id string, selected bool, err error)`。
- 选择器模型持有 `items`、`filtered`、`cursor`、`search`、`scrollTop`、窗口尺寸，不执行磁盘写入。

- [ ] **Step 1: 写失败测试**

添加模型级测试，覆盖：字符输入过滤 preview/ID/title、上下移动边界、Enter 返回当前 ID、Esc 返回未选择、窄终端渲染不 panic 且不输出无效 UTF-8。

```go
func TestResumePickerFiltersAndSelects(t *testing.T) {
	m := newResumePickerModel([]conversation.Metadata{
		{ID: "conv-other12345678", Preview: "README 项目说明"},
		{ID: "conv-fox12345678", Preview: "FoxCode 项目分析"},
	}, 40, 12)
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f', 'o', 'x'}}).(resumePickerModel)
	_ = cmd
	if len(m.filtered) != 1 || m.filtered[0].ID != "conv-fox12345678" { t.Fatal("filter mismatch") }
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(resumePickerModel)
	if !m.selected || m.selectedID != "conv-fox12345678" { t.Fatal("selection mismatch") }
}
```

- [ ] **Step 2: 运行测试确认失败**

运行：`go test ./internal/app -run TestResumePicker -count=1`

预期：FAIL，因为选择器模型和更新逻辑尚不存在。

- [ ] **Step 3: 实现最小 Bubble Tea 选择器**

实现 `↑/↓`、Enter、Esc、普通字符搜索和 Backspace；过滤字段为 `Title`、`Preview`、`ID`；列表项显示标题/预览、相对更新时间、消息数和 KB；空列表显示“暂无完整会话，可输入 /new”。

输入框和当前聊天保持现有分隔线、青色选中项、灰色辅助信息，不引入新的终端依赖。

- [ ] **Step 4: 运行测试确认通过**

运行：`go test ./internal/app -run 'TestResumePicker|TestChatInput' -count=1`

预期：PASS。

- [ ] **Step 5: 提交独立变更**

```powershell
git add internal/app/chat_resume.go internal/app/chat_resume_test.go internal/app/chat_input.go
git commit -m "功能：增加会话恢复选择器"
```

### Task 3: 接入 `/resume` 与目标会话的事务式切换

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`
- Modify: `internal/agent/agent.go` only if an existing restore method cannot preserve the current Runner contract

**Interfaces:**
- Add a chat-local helper with behavior equivalent to `switchChatSession(runner, persistence, targetID) error`.
- Helper must load and validate the target snapshot before mutating `runner` or `persistence`.

- [ ] **Step 1: 写失败测试**

在 `internal/app/chat_test.go` 增加真实 Store/Runner 测试：

- `/resume <valid-id>` 后 `/status` 使用目标 Session ID、消息数和 token 统计；
- 非法 ID 后原 Session ID 和消息不变；
- 目标快照损坏后原 Session ID 和消息不变；
- `--no-session` 下 `/resume` 明确提示持久会话不可切换，当前上下文不变。

测试输入使用 `strings.NewReader("/resume <id>\n/status\nexit\n")`，Provider 不应因切换本身发起请求。

- [ ] **Step 2: 运行测试确认失败**

运行：`go test ./internal/app -run 'TestChatResume|TestChatResumeRejects' -count=1`

预期：FAIL，因为 chat loop 当前把 `/resume` 当作普通模型提示。

- [ ] **Step 3: 实现直接恢复流程**

在 chat loop 的 slash 命令分支中加入 `/resume <id>`：调用 Store.Load，构造完整候选状态，使用 Runner 现有消息恢复能力替换消息，并同步 `persistence.snapshot` 与 usage。任何错误在 stderr 或聊天错误行输出，但不得修改旧状态。

不要重新读取审计 JSONL，不要创建额外索引，不要让模型参与会话选择。

- [ ] **Step 4: 接入无参数选择器**

在 `/resume` 无参数时调用 Task 2 的选择器；选择成功后复用同一个事务切换函数，Esc 或关闭选择器直接回到输入区。

- [ ] **Step 5: 运行测试确认通过**

运行：`go test ./internal/app -run 'TestChatResume|TestChatResumeRejects|TestChatStatus' -count=1`

预期：PASS，且切换不产生 Provider 请求。

- [ ] **Step 6: 提交独立变更**

```powershell
git add internal/app/chat.go internal/app/chat_test.go internal/agent/agent.go
git commit -m "功能：支持聊天内恢复历史会话"
```

### Task 4: 接入 `/new` 且保证持久化失败不丢上下文

**Files:**
- Modify: `internal/app/chat.go`
- Modify: `internal/app/chat_test.go`

**Interfaces:**
- Add a chat-local helper with behavior equivalent to `startNewChatSession(runner, persistence) error`.

- [ ] **Step 1: 写失败测试**

增加测试：持久模式下 `/new` 先保留旧快照，再产生新的 `conv-` ID；`--no-session` 下 `/new` 只清空内存；模拟保存失败时旧 Runner 消息、旧快照和旧 Session ID 均保留。

- [ ] **Step 2: 运行测试确认失败**

运行：`go test ./internal/app -run 'TestChatNewSession' -count=1`

预期：FAIL，因为 `/new` 当前会被发送给模型。

- [ ] **Step 3: 实现 `/new`**

持久模式下调用现有 Store.Create 创建新快照；只有创建成功后才 Reset Runner、更新 `persistence.snapshot` 和 usage。无持久模式只 Reset Runner。输出新的 Session ID 和继续输入提示，不删除旧文件。

- [ ] **Step 4: 运行测试确认通过**

运行：`go test ./internal/app -run 'TestChatNewSession|TestChatResume' -count=1`

预期：PASS。

- [ ] **Step 5: 提交独立变更**

```powershell
git add internal/app/chat.go internal/app/chat_test.go
git commit -m "功能：支持聊天内创建新会话"
```

### Task 5: 同步 M2.3 文档、README 和验收证据

**Files:**
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/Process/代码理解.md`
- Modify: `doc/m2.3-session-switching.md`
- Create: `artifacts/verification/m2.3/full-check.txt`
- Create: `artifacts/verification/m2.3/manual-acceptance.txt`

- [ ] **Step 1: 更新当前范围**

把 `spec/current.md` 当前版本提升为 M2.3，增加 `/resume`、`/new` 的 AC 表，并明确 `audit` 不进入 chat 命令空间；README 的快速开始和命令列表同步。

- [ ] **Step 2: 更新代码理解文档**

补充 chat loop 的 slash 命令分支、选择器状态、事务式切换和持久化/无持久化差异；说明 `.drift/sessions` 与 `.drift/audits` 仍然分离。

- [ ] **Step 3: 运行全量验证**

```powershell
go test ./... -count=1
go vet ./...
go build -o .codex-temp\drift-m23.exe ./cmd/drift
git diff --check
```

- [ ] **Step 4: 运行人工验收并记录输出**

按 `doc/m2.3-session-switching.md` 的脚本验证选择、搜索、Esc、直接 ID、`/new`、`--no-session` 和目录分离；证据中不得写入 API Key、完整回答正文或文件内容。

- [ ] **Step 5: 提交文档和验收证据**

```powershell
git add README.md spec/current.md doc/Process/代码理解.md doc/m2.3-session-switching.md artifacts/verification/m2.3
git commit -m "文档：补充 M2.3 会话切换验收"
```

## Plan Self-Review

- `/resume`、`/resume <id>`、`/new`、搜索、Esc、失败回滚和 `--no-session` 均有对应任务与测试。
- 没有引入会话树、写入工具、命令执行或新的存储格式。
- 审计保持进程级 JSONL；不会被选择器读取为恢复上下文。
- `Metadata.Preview` 是内存展示字段，不改变快照 JSON 格式。
- M2.3 的实现顺序满足先数据元信息、再选择器、再切换事务、最后文档验收。
