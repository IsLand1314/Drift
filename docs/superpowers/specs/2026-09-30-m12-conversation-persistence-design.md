# M1.2：本地对话持久化与恢复设计

## 1. 目标

M1.2 让 `drift chat` 默认把**当前可恢复上下文**保存到当前 workspace 的本地目录。用户可以在进程退出后恢复最近或指定会话；也可以使用 `--no-session` 获得退出即丢失的临时模式。

本阶段借鉴 Pi 与 Codex 的“长任务可继续”体验，但不实现 Pi 的树状历史、fork、自动摘要或云同步。

## 2. 两类本地记录必须分离

```text
.drift/
├── sessions/                 脱敏审计：已有能力，永不恢复给模型
└── conversations/            完整上下文快照：仅 M1.2 显式恢复使用
```

| 类型 | 内容 | 用途 | 是否会被模型读取 |
| --- | --- | --- | --- |
| `sessions/*.jsonl` | 脱敏事件、相对路径、字节数、错误 stage | 审计与排障 | 否 |
| `conversations/*.json` | user、assistant、tool 消息及工具结果 | 恢复同一对话 | 仅用户使用 `--resume` 后 |

完整会话可能包含提示词、模型回答和读取过的文件内容；它不是安全日志，也不承诺自动脱敏。两类目录均已被 Git 忽略。

## 3. CLI 契约

```powershell
# 默认：创建并保存一个新会话
drift chat -w .

# 临时模式：不创建完整会话，但仍保留既有脱敏审计
drift chat --no-session -w .

# 恢复当前 workspace 最近更新的完整会话
drift chat --resume -w .

# 恢复当前 workspace 中指定 ID 的完整会话
drift chat --resume <id> -w .

# 管理完整会话；不会回显消息正文
drift conversation list
drift conversation show <id>
drift conversation delete <id> --yes
```

`--resume` 与 `--no-session` 互斥。恢复只在当前选定 workspace 的 `.drift/conversations/` 中查找，不支持跨 workspace 引用。`-w <文件>` 与 `--resume` 同时出现时拒绝：恢复使用会话保存的 focus，避免两个 focus 来源冲突。

默认新建或恢复完整会话时，终端须明确显示会话 ID，并提示该会话会在本机保存完整上下文；`--no-session` 不显示此提示。

## 4. 快照数据模型

每个会话是一个 JSON 文件，而不是 JSONL。它只保存**最新可用上下文快照**，不保存旧分支或已被 `/clear` 清理的历史。

```json
{
  "version": 1,
  "id": "conv-...",
  "created_at": "2026-09-30T00:00:00Z",
  "updated_at": "2026-09-30T00:00:00Z",
  "focus": "README.md",
  "context_bytes": 0,
  "messages": []
}
```

- `messages` 使用现有 `llm.Message`，因此保留 assistant tool call、tool result 和 reasoning content；恢复后的 Provider 请求与进程未退出时一致。
- `context_bytes` 是最近一次成功保存时由 Runner 计算的上下文字节估算；list/show 只显示该数字，不读取消息正文。
- 快照 schema 不额外保存 API Key、Authorization、Provider URL、模型名或 workspace 绝对路径；恢复使用当前启动时的 Provider 配置。由于它保存完整消息，用户主动输入的密钥或其他文件中的敏感内容仍可能随消息落盘。
- ID 由 Drift 生成，只含小写字母、数字和短横线；所有 CLI 输入 ID 必须按同一规则校验，禁止路径穿越。
- 文件位于 workspace 内，因此无需记录 workspace 绝对路径；`focus` 仅保存安全相对路径或为空。

快照大小没有独立的更高限额，恢复后的 `Runner.ContextBytes()` 仍受 M1.1 的 1 MiB 请求上限约束。

## 5. 写入、恢复与 `/clear`

### 5.1 创建与保存

1. 普通 `drift chat` 在发起 Provider 请求前创建空快照；创建失败则退出，不发请求。
2. 每个 `RunEvents` 成功结束后，以 Runner 当前消息覆盖保存快照。
3. 写入使用同目录临时文件、`0600` 权限和原子替换；目录使用 `0700` 权限。
4. Provider、工具或输出失败的半完成轮次不保存；当前进程内仍保留现有 Runner 行为。

若成功回答后保存失败，终端必须显示“会话保存失败，本次上下文只保留在当前进程”，但不得伪装成已保存。下一次成功轮次可以再次尝试保存。

### 5.2 恢复

1. `--resume` 查找最近更新的快照；带 ID 时只读取指定文件。
2. 先严格解析、校验版本、ID、focus 与消息结构，再将消息恢复至新建 Runner。
3. 文件不存在、格式损坏、版本不支持或 focus 无效时，返回退出码 2，且不发送 Provider 请求。
4. 恢复后显示 ID、消息数量和上下文字节估算；若估算已超过 1 MiB，仅提示用户下次请求前需要 `/clear`，不自动截断。

### 5.3 `/clear`

持久会话中的 `/clear` 必须先原子保存空 `messages` 快照，成功后再执行 `Runner.ResetContext()` 并显示“已清空当前对话上下文”。如果快照写入失败，保留内存上下文并显示错误，避免本次进程与下次恢复的上下文不一致。

临时模式的 `/clear` 沿用 M1.1：只清空内存，既不保存完整上下文，也不新增审计事件。

## 6. 会话管理命令

`drift conversation` 与已有 `drift session` 完全分离：前者操作完整上下文，后者只读查看脱敏审计。

| 命令 | 输出或行为 | 正文保护 |
| --- | --- | --- |
| `conversation list` | ID、创建/更新时间、消息数、上下文字节数、focus | 不显示消息内容 |
| `conversation show <id>` | 同上，外加文件版本 | 不显示消息内容、工具参数或结果 |
| `conversation delete <id> --yes` | 删除一个经过 ID 校验的精确文件 | 不递归删除；缺少 `--yes` 返回退出码 2 |

删除不会删除同名审计 JSONL，也不会影响其他 workspace。

## 7. 安全和边界

- 默认持久化是本机私有功能，不上传、不共享、不同步；只有恢复后的下一次模型请求会按既有 Provider 行为发送历史上下文。
- 首次创建会话时必须提示“保存完整本地上下文，可能包含读取结果和用户输入；使用 `--no-session` 可关闭”。
- 不对完整快照进行“部分脱敏”：不完整脱敏会让恢复语义不可预测，也会制造错误安全感。
- 不实现加密、密码保护、自动过期、跨用户访问、跨 workspace 恢复、fork、树结构、搜索正文、导出或自动摘要。
- `.env` 仍由工具层禁止读取；但用户应假定其他读取文件内容也会出现在完整会话中。

## 8. 测试与验收

| ID | 验证方法 | 通过阈值 |
| --- | --- | --- |
| AC-M12-001 | 本地模拟 Provider 下启动普通 chat 并完成一轮 | 创建 `conversations/<id>.json`；文件可解析；schema 不额外记录 Provider 凭据或 workspace 绝对路径 |
| AC-M12-002 | 新进程 `--resume <id>` 后发起第二轮 | Provider 第二次请求携带第一轮 user/assistant/tool 上下文 |
| AC-M12-003 | `--no-session` 运行一轮 | 不创建完整快照；仍生成 `.drift/sessions/` 脱敏审计 |
| AC-M12-004 | 记住暗号、`/clear`、退出后恢复 | 恢复后的请求不包含清理前暗号；清理不触发额外 Provider 请求 |
| AC-M12-005 | 损坏 JSON、非法 ID、跨 workspace ID、`--resume` 与 `--no-session`、`-w 文件` 与 `--resume` | 退出码 2；Provider 请求计数为 0；不修改其他会话 |
| AC-M12-006 | list/show/delete 及缺少 `--yes` 的 delete | list/show 不回显正文；只有精确 ID 且带 `--yes` 才删除一个文件 |
| AC-M12-007 | `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 四条命令退出码均为 0 |

## 9. 后续阶段

M1.3 再评估以下能力：JSONL 追加历史、会话分叉、`/tree`、自动/手动上下文压缩、可控导出与保留策略。M1.2 不提前为这些能力引入抽象或依赖。
