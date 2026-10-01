# M2.0：Workspace Skills 设计

## 1. 目标

为 Drift 增加可复用的只读工作流说明。Skill 使用 Codex 风格的目录结构，以 `SKILL.md` 作为入口；它只提供提示和流程约束，不增加工具权限，不改变 Drift 的 workspace、Agent 预算或只读边界。

M2.0 的最小闭环是：发现 Skill → 查看 Skill → 显式选择 Skill → 将内容作为模型上下文注入 → 继续使用现有 Agent Loop。

## 2. 目录结构

Skill 只从当前 workspace 的 `.drift/skills/` 发现：

```text
.drift/
└── skills/
    ├── project-overview/
    │   └── SKILL.md
    └── code-review/
        └── SKILL.md
```

M2.0 不读取用户目录、全局目录、Git 仓库外目录或任意绝对路径；不跟随符号链接。每个 Skill 名称只能使用 ASCII 小写字母、数字和连字符，长度 1～64。

## 3. CLI 接口

```text
drift skill list [-w workspace]
drift skill show <name> [-w workspace]
drift -skill <name> -p "用户问题"
drift chat -skill <name> -w workspace
```

- `skill list` 只列出名称和入口文件状态，不请求 Provider。
- `skill show` 只输出本地 `SKILL.md` 内容，不请求 Provider；不存在、非法名称或超限返回参数错误。
- `-skill` 只能显式选择一个 Skill；未指定时不加载任何 Skill。
- `-skill` 与 `skill list/show` 的解析发生在 Provider 创建前，缺少 API Key 时仍可运行查看命令。
- 恢复会话时 Skill 由本次命令重新选择；快照不保存 Skill 正文，避免隐式恢复旧工作流。

## 4. 文件格式与限制

M2.0 不强制 YAML front matter。`SKILL.md` 是 UTF-8 Markdown 原文，首行可选标题，其余内容作为指令文本。

限制如下：

- 单个 `SKILL.md` 最大 64 KiB；读取失败、非 UTF-8 或超限时拒绝加载。
- 一次运行最多加载一个 Skill；不支持 Skill 继承、嵌套引用、动态脚本、二进制资产或网络 URL。
- Skill 内容只作为额外 system context 注入，不拼接进用户 prompt，不写入脱敏审计。
- 完整会话快照仍遵循现有 M1.2 规则；Skill 正文不作为独立持久化字段保存。

## 5. 调用流程

```text
CLI 解析 -skill
  ↓
workspace/.drift/skills/<name>/SKILL.md
  ↓
路径、名称、大小和 UTF-8 校验
  ↓
app 将 Skill 文本放入 Agent 的额外 system context
  ↓
Agent 继续使用既有只读工具和预算
  ↓
Provider 请求、工具调用、会话和审计沿用 M1.x
```

基础只读系统指令优先级高于 Skill；用户问题仍是当前任务目标。Skill 不能声明新工具、放宽 workspace 边界、要求读取 `.env`、执行命令或修改文件。模型若试图执行这些内容，仍由现有工具注册表和路径边界拒绝。

## 6. 分层边界

新增最小的 `internal/skill` 包，负责发现、名称校验、读取和限制；`internal/app` 负责 CLI 选择与错误码；`internal/agent` 只接收已经准备好的额外 system context，不导入文件系统 Skill 发现逻辑。

```text
cmd -> app -> skill loader
          -> agent -> llm
```

Provider 不感知 Skill 名称；Session 审计只记录安全的 `skill` 名称或加载失败 stage，不记录 Skill 正文。`/status` 在 M2.0 可增加 `Skill` 字段，但不显示正文。

## 7. 错误与安全

- 未知 Skill、非法名称、符号链接、目录、缺失入口、超限或无效 UTF-8 在 Provider 请求前失败。
- 错误不得回显绝对 workspace 路径、文件正文或 Skill 全文；`skill show` 是用户主动请求时的本地查看例外。
- Skill 读取只允许 `.drift/skills/<name>/SKILL.md`，不得通过 `..` 越界。
- Skill 不改变 API Key、Provider URL、工具 schema、请求预算、读取上限或会话脱敏规则。

## 8. 测试与验收

### 单元测试

- 名称校验、缺失入口、目录/符号链接、大小上限和 UTF-8 校验。
- `list/show` 不创建 Provider、不要求 API Key。
- Skill 文本作为额外 system context 出现在请求中，用户 prompt 和工具 schema保持原有结构。
- 未选择 Skill 时请求字节和行为与 M1.10 一致。

### 人工验收

1. 创建 `.drift/skills/project-overview/SKILL.md`，运行 `drift skill list -w .`，确认列出名称。
2. 运行 `drift skill show project-overview -w .`，确认只输出本地内容且不请求模型。
3. 配置 OpenAI Compatible Provider 后运行 `drift -skill project-overview -p "分析当前项目"`，确认回答遵循 Skill，同时仍只能调用三个只读工具。
4. 使用不存在的 Skill、绝对路径、`..`、超大文件和符号链接，确认请求前失败。
5. 检查 `.drift/sessions` 和完整快照，确认不出现 Skill 正文、API Key 或绝对路径。

### 完成标准

`go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过；M1.10 的 OpenAI 联调、TTY 进度和 `Done - <seconds>s` 不回归。

## 9. 非目标

M2.0 不实现全局 Skill marketplace、远程下载、MCP、脚本执行、写文件、命令执行、自动 Skill 选择、多 Skill 合并、Skill 版本管理或复杂 front matter 解析。
