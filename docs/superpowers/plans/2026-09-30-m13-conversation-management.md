# M1.3 Conversation Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 M1.2 单快照恢复基础上，增加本地会话命名、列表限制、删除预览和按时间清理。

**Architecture:** 继续使用 `internal/conversation.Store` 扫描 workspace 内的单文件快照，不引入独立索引。Store 负责标题校验、元数据更新、时间筛选和安全删除；`internal/app` 只负责 CLI 语法、确认流程和无 Provider 命令分发。

**Tech Stack:** Go 标准库、现有 `internal/conversation`、`internal/app`、JSON 快照；不新增依赖。

**Spec:** `docs/superpowers/specs/2026-09-30-m13-conversation-management-design.md`

## Global Constraints

- 完整快照仍位于 workspace 的 `.drift/conversations/`，与脱敏 `.drift/sessions/` 分离。
- `title` 最多 120 个 UTF-8 字节，禁止换行、NUL 和控制字符；空标题表示未命名。
- `conversation` 命令不加载 `.env`、不创建 Provider、不发送模型请求。
- 所有 ID 必须经过现有 `conv-...` 校验；删除只作用于精确单文件。
- `prune` 没有 `--yes` 只预览；带 `--yes` 才删除 `updated_at` 早于 RFC3339 阈值的快照。
- 保留 M1.2 的权限、临时文件、损坏 JSON、符号链接和跨 workspace 边界。
- 不实现正文历史、fork、树、`/undo`、自动摘要、正文搜索、导出、加密或云同步。

---

### Task 1: 快照标题与时间筛选存储 API

**Files:**
- Modify: `internal/conversation/store.go`
- Modify: `internal/conversation/store_test.go`

**Interfaces:**
- `Snapshot` 增加 `Title string`，持久化字段为 `title`。
- `Metadata` 增加 `Title string`。
- 新增 `func (s *Store) Rename(id, title string) error`。
- 新增 `func (s *Store) Before(before time.Time) ([]Metadata, error)`。
- 新增 `func (s *Store) Prune(before time.Time) ([]Metadata, error)`。
- 新增标题校验，非法标题返回 `ErrInvalidSnapshot`。

- [ ] **Step 1: 写失败测试**

覆盖标题保存/加载、控制字符、超过 120 字节、旧 JSON 缺少 `title`、`Before` 排序和 `Prune` 只删除过期快照。

```go
func TestStoreRenameAndLoadTitle(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil { t.Fatal(err) }
	if err := store.Rename(snapshot.ID, "FoxCode 分析"); err != nil { t.Fatal(err) }
	loaded, err := store.Load(snapshot.ID)
	if err != nil || loaded.Title != "FoxCode 分析" { t.Fatalf("loaded=%+v err=%v", loaded, err) }
}
```

- [ ] **Step 2: 运行 RED**

运行：`go test ./internal/conversation -run 'TestStore(Rename|Before|Prune)' -count=1 -v`。预期因新 API 不存在而失败。

- [ ] **Step 3: 实现最小存储改动**

复用 M1.2 的 `Save`、`Load`、`List`、`Delete`。标题验证使用 UTF-8 字节长度和 `unicode.IsControl`；旧快照缺少 `title` 按空字符串读取。`Rename` 读取目标、更新 `Title`/`UpdatedAt` 后原子保存。`Prune` 删除前重新 `Load` 并比较时间。

- [ ] **Step 4: 运行存储回归**

运行：`go test ./internal/conversation -count=1 -v`。预期 M1.2 与 M1.3 存储测试全部通过。

- [ ] **Step 5: 提交**

```powershell
git add internal/conversation/store.go internal/conversation/store_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：支持会话标题与过期筛选" -m "参与人：island"
```

### Task 2: 会话管理 CLI 语法与确认流程

**Files:**
- Modify: `internal/app/conversation_command.go`
- Modify: `internal/app/conversation_command_test.go`

**Interfaces:**
- 保留 `runConversationCommand(args []string, out, stderr io.Writer) int`。
- 支持 `conversation list [--limit N]`，默认 `N=50`。
- 支持 `conversation rename <id> <title>`。
- 支持 `conversation prune --before <RFC3339> [--yes]`。

- [ ] **Step 1: 写失败测试**

覆盖列表限制和标题展示、rename 成功/非法标题、prune 无 `--yes` 只预览、prune 带 `--yes` 删除旧快照、无 API Key 仍可运行。

- [ ] **Step 2: 运行 RED**

运行：`go test ./internal/app -run 'TestConversation(Rename|Limit|Prune)' -count=1 -v`。预期当前命令不支持新增语法而失败。

- [ ] **Step 3: 实现 CLI**

保持命令在 `.env` 加载前分发；解析失败返回 2。`list --limit` 只截取已排序元数据；`rename` 输出完成提示；`prune` 无确认时只输出候选数量、ID、标题、更新时间，不删除；带 `--yes` 调用 `Store.Prune` 并输出实际删除数量。

- [ ] **Step 4: 运行命令回归**

运行：`go test ./internal/app -run 'TestConversation|TestSession' -count=1 -v`。预期 M1.2 的 show/delete 隐私边界和 Session 命令保持通过。

- [ ] **Step 5: 提交**

```powershell
git add internal/app/conversation_command.go internal/app/conversation_command_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：完善会话生命周期命令" -m "参与人：island"
```

### Task 3: 规格、文档和人工验收

**Files:**
- Create: `doc/m1.3-conversation-management.md`
- Modify: `README.md`
- Modify: `spec/current.md`
- Modify: `doc/Process/代码理解.md`
- Modify: `docs/superpowers/plans/2026-09-30-m13-conversation-management.md`

- [ ] **Step 1: 写阶段文档和验收表**

加入可复制命令：

```powershell
go run ./cmd/drift conversation list --limit 10
go run ./cmd/drift conversation rename <id> "FoxCode 分析"
go run ./cmd/drift conversation prune --before 2026-09-01T00:00:00Z
go run ./cmd/drift conversation prune --before 2026-09-01T00:00:00Z --yes
```

验收表覆盖标题、列表限制、预览不删除、确认清理、Session 不受影响、无 API Key 管理命令和全量回归。

- [ ] **Step 2: 运行文档检查**

运行：`git diff --check`。预期退出码 0，命令语法与实现一致，无失效 M1.3 文档链接。

- [ ] **Step 3: 全量验证**

运行：`go test ./... -count=1`、`go vet ./...`、`go build -o .codex-temp\\drift-m13.exe ./cmd/drift`、`git diff --check`。预期四条命令均退出 0。

- [ ] **Step 4: 提交**

```powershell
git add README.md spec/current.md doc/m1.3-conversation-management.md doc/Process/代码理解.md docs/superpowers/plans/2026-09-30-m13-conversation-management.md
git -c user.name=island -c user.email=island0920@163.com commit -m "文档：补充 M1.3 会话管理说明" -m "参与人：island"
```
