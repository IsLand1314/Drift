# M1.3：会话索引与生命周期管理设计

## 1. 目标

M1.3 在 M1.2 的单文件快照基础上补齐会话管理体验：用户能给会话命名、按元数据筛选、预览待删除对象并安全清理旧会话。恢复协议保持不变，完整正文仍只在明确 `--resume` 后送入当前 Provider。

本阶段解决“会话太多后难以区分和清理”的问题，不提前引入 Pi/Codex 式树状历史。

## 2. 当前基础与新增范围

M1.2 已提供：

- `.drift/conversations/<id>.json` 单快照；
- `conversation list/show/delete`；
- `chat --resume`、`--resume <id>`、`--no-session`；
- `.drift/sessions/` 脱敏审计与完整会话分离。

M1.3 新增：

- 可选用户标题 `title`，默认为空，不自动把首条提示词复制到列表；
- `conversation rename <id> <title>`，只修改指定快照的元数据；
- `conversation list` 的排序、限制和标题展示；
- 删除前的预览/确认流程，以及按时间清理旧会话；
- Store 层对快照的统一元数据更新和保留策略测试。

## 3. CLI 契约

```powershell
# 列出最近会话，默认按 updated_at 倒序
drift conversation list

# 只列出前 N 个
drift conversation list --limit 20

# 查看单个会话的元数据
drift conversation show <id>

# 修改标题；标题只作为元数据保存，不发送给模型
drift conversation rename <id> "FoxCode 代码分析"

# 先预览待清理对象
drift conversation prune --before 2026-09-01T00:00:00Z

# 明确确认后才删除匹配对象
drift conversation prune --before 2026-09-01T00:00:00Z --yes

# 继续保留精确删除
drift conversation delete <id> --yes
```

规则：

- `list` 默认不输出消息正文、工具参数、工具结果、API Key 或绝对路径；`title` 是用户主动设置的文本，需按单行和长度上限校验。
- `--limit` 只接受正整数，默认值为 50；超过限制返回退出码 2，不静默修正。
- `prune` 没有 `--yes` 时只输出数量、ID、标题和更新时间，不删除任何文件；带 `--yes` 后只删除 `updated_at` 早于阈值的完整快照。
- `prune` 不删除 `.drift/sessions/*.jsonl`，也不跨 workspace；空匹配是成功且不修改文件。
- `rename` 与 `delete` 都要求精确 ID；不存在、损坏或非法 ID 返回退出码 2。
- `chat --resume` 继续使用 ID 或最近更新时间选择，不读取标题作为模型上下文。

## 4. 数据模型

在 M1.2 快照上增加：

```json
{
  "version": 1,
  "id": "conv-...",
  "title": "FoxCode 代码分析",
  "created_at": "2026-09-30T00:00:00Z",
  "updated_at": "2026-09-30T00:00:00Z",
  "focus": "README.md",
  "context_bytes": 1234,
  "messages": []
}
```

- `title` 最大 120 个 UTF-8 字节，禁止换行、NUL 和控制字符；空标题表示未命名。
- 版本仍为 1，旧快照缺少 `title` 时按空标题兼容读取；保存时补充空字段或省略字段均可，但读写结果必须稳定。
- 不引入单独索引文件，继续扫描 `.json` 快照，避免索引与快照不一致。
- 不保存首条用户提示词作为自动标题，避免把敏感输入无意暴露到 `list` 输出。

## 5. 生命周期与安全

```text
创建快照 -> 多轮保存 updated_at
       -> rename 只更新标题
       -> list/show 只读元数据
       -> prune 预览 -> --yes 精确删除旧快照
       -> resume 仍从当前 workspace 恢复最新/指定快照
```

- 完整快照仍可能包含提示词、回答和文件内容，不是脱敏日志，不应上传或共享。
- `rename`、`prune` 不创建 Provider、不加载 `.env`，缺少 API Key 也可执行。
- 删除操作使用经过 ID 校验的单文件路径，不递归删除目录；删除失败不修改其他会话。
- `prune --yes` 在执行前重新读取并校验匹配文件，避免按过期列表误删新内容。
- 保留 M1.2 的目录/文件权限、临时文件写入、符号链接和损坏快照拒绝规则。

## 6. 测试与验收

| ID | 验证方法 | 通过阈值 |
| --- | --- | --- |
| AC-M13-001 | Store 保存/加载带 title 的快照 | 标题、旧无 title 快照和消息正文均正确恢复 |
| AC-M13-002 | `conversation rename` 正常、非法 ID、过长/控制字符标题 | 只更新目标快照；非法输入退出码 2；不请求 Provider |
| AC-M13-003 | `conversation list --limit` 和默认排序 | 按更新时间倒序；结果不超过限制；不输出正文 |
| AC-M13-004 | `conversation prune --before` 预览与 `--yes` | 无 `--yes` 不删除；带 `--yes` 只删除阈值前快照，不影响新快照和 Session |
| AC-M13-005 | 真实 CLI 无 API Key 执行 list/show/rename/prune | 命令可完成；不加载 `.env`、不创建 Provider 请求 |
| AC-M13-006 | `go test ./... -count=1`、`go vet ./...`、构建和 `git diff --check` | 全部退出码为 0 |

## 7. 明确不做

M1.3 不实现 JSONL 全量正文历史、fork、树状导航、`/undo`、自动摘要、正文搜索、导出、加密、云同步、跨 workspace 恢复或后台自动清理。需要这些能力时另开设计阶段，避免把当前单快照协议变成隐式历史系统。
