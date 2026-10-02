# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构边界见 `doc/architecture.md`。当前版本为 M3.4。下面的 M3.3、M3.2、M2.5、M2.4、M2.3、M2.2、M2.1、M2.0、M1.10、M1.9、M1.8、M1.7、M1.6、M1.5、M1.4、M1.3、M1.2、M1.1、M1.0、M0.9、M0.8、M0.7、M0.6、M0.5、M0.4、M0.3、M0.2.2 和 M0.2.1 章节是已完成阶段的历史记录，不覆盖当前 M3.4 的运行边界。

## M3.4：受控命令执行

M3.4 在 `chat` 中增加经审批的 `run_command`。`-p`、`audit` 和 session 管理保持只读，不暴露命令工具。

### 当前范围

- `run_command` 只在 `chat` 注册；Windows 使用 `cmd.exe /d /s /c`，Unix 使用 `/bin/sh -c`，cwd 必须是 workspace 内的真实目录。
- 默认超时 30 秒，最大 60 秒；stdout/stderr 分别限流，默认总上限 32 KiB；结果包含状态、退出码、耗时、字节数和截断标记。
- 每次执行前使用现有三选审批；当前 chat 的允许记忆仅匹配同一工具、同一 cwd 和同一精确命令，退出 chat 后失效。
- 取消、超时、非零退出和拒绝均不得报告成功；工具输出回传模型，但完整命令输出不写入 session、audit 或 changes。

### 非目标

M3.4 不提供 OS 沙箱、永久权限配置、跨进程授权、命令事务或 Git 回滚；不承诺跨平台 shell 语义完全一致。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M34-001 | `go test ./internal/tool -run RunCommand -count=1` | workspace 执行、cwd 边界、超时和输出截断通过 | 测试日志 | `artifacts/verification/m3.4/command-tests.txt` | 任一命令边界失败 |
| AC-M34-002 | `go test ./internal/agent ./internal/app ./internal/session -count=1` | chat-only 注册、审批精确记忆、命令审计脱敏通过 | 测试日志 | `artifacts/verification/m3.4/integration-tests.txt` | `-p` 暴露命令或审计泄漏正文 |
| AC-M34-003 | `go test ./... -count=1`、`go vet ./...`、build、diff 检查 | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.4/final-check.txt` | 任一命令退出码非 0 |

## M3.3：安全文件编辑与变更集合

M3.3 在 `chat` 中完成受控文件修改能力；`-p` 仍只读。写入、编辑和删除均先预检，再由用户审批；一轮中的多个文件操作聚合到一个 `.drift/changes` change set。

### 当前范围

- `write_file` 支持 workspace 内缺失父目录的逐级创建，仍拒绝绝对路径、`..`、符号链接逃逸和受保护目录。
- `edit_file` 只替换 UTF-8 文件中恰好一次匹配的 `old_text`；零匹配、多匹配、二进制或目标变更均拒绝。
- `delete_file` 只删除经审批的普通文件，不递归删除目录。
- `Yes`、`Yes, and don't ask again for this pattern`、`No` 统一适用于三类文件工具；允许记忆仅限当前 chat 的工具/操作/路径。
- 每个用户回合使用一个 change set，`manifest.json` 聚合文件操作，`diff.patch` 聚合差异，删除操作保存 `.before` 内容；空回合不留下记录。
- 写入工具只在 `chat` 注册；`-p`、`audit` 和会话存储职责不变。

### 非目标

M3.3 不提供 `run_command`、测试执行、OS 沙箱、永久权限配置或 Git 回滚；固定底栏的持续 TUI 设计记录在 `doc/Process/面板视觉设计.md`，不改变非 TTY 协议。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M33-001 | `go test ./internal/tool ./internal/changes -count=1` | 嵌套创建、精确编辑、删除边界和 change set 聚合通过 | 测试日志 | `artifacts/verification/m3.3/final-focused-tests.txt` | 任一文件工具或 change set 测试失败 |
| AC-M33-002 | `go test ./internal/agent ./internal/app -count=1` | 三类工具只在 chat 暴露，审批与安全错误摘要通过 | 测试日志、人工记录 | `artifacts/verification/m3.3/acceptance-2026-10-02.md` | 写入工具出现在 `-p` 或审批边界失败 |
| AC-M33-003 | `go test ./... -count=1`、`go vet ./...`、build、diff 检查 | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.3/final-go-test.txt` | 任一命令退出码非 0 |

## M3.1：安全文件写入

M3.1 将 Drift 从只读 Runtime 扩展为受控写入 Runtime。`drift -p` 继续只读；只有 `drift chat` 注册 `write_file`，且创建/覆盖文件前必须获得用户确认。

### 当前范围

- `write_file` 接受 workspace 相对路径和完整 UTF-8 文本，支持创建新文件和覆盖已有普通文件。
- 拒绝绝对路径、`..`、符号链接逃逸、受保护目录以及不存在的父目录；不自动创建目录。
- 写入采用临时文件和原子替换；批准前不修改目标，拒绝/取消后目标保持不变。
- 修改过程记录写入 `.drift/changes/YYYY/MM/DD/change-<timestamp>-<id>/`，包含 `manifest.json`、`diff.patch` 和 `work/`；不作为会话恢复或脱敏审计来源。
- M3.1 不提供 `edit_file`、删除、命令执行、测试运行、OS 沙箱或 Git 回滚。

### M3.1 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M31-001 | `go test ./internal/tool ./internal/changes -count=1` | 创建、覆盖、路径边界、临时记录通过 | `artifacts/verification/m3.1/full-check.txt` |
| AC-M31-002 | `go test ./internal/agent ./internal/app -count=1` | `chat` 可确认写入，`-p` 不注册写入工具 | `artifacts/verification/m3.1/full-check.txt` |
| AC-M31-003 | `go run ./cmd/drift chat -w .` | 拒绝不改文件，批准后创建/覆盖目标并生成 changes 记录 | `artifacts/verification/m3.1/manual-acceptance.txt` |
| AC-M31-004 | 全量测试、vet、构建和 diff 检查 | 全部退出码为 0 | `artifacts/verification/m3.1/full-check.txt` |

## M3.2：审批选择器与运行反馈

M3.2 将 M3.1 的 TTY 写入确认改为方向键选择，并统一读写工具的紧凑进度和耗时反馈。当前会话内的“允许此类操作”不持久化，不改变 `-p` 的只读边界。

### 当前范围

- 真实 TTY 提供 `Yes`、`Yes, and don't ask again for this pattern`、`No` 三个选择；方向键/数字键移动，Enter 确认，Esc/Ctrl+C 拒绝。
- 非 TTY 保留 `y`/`yes` 兼容输入，重定向输出无 ANSI。
- 工具反馈显示开始、成功/失败和耗时；TTY 审批不展开完整 diff，完整 diff 仍写入 `.drift/changes/`。
- 写入预检失败显示脱敏、可理解的安全原因；模型不得读取 Drift 实现文件解释运行时失败。
- 不自动创建父目录，不提供永久权限、完整全屏 TUI、删除、命令执行、测试运行、OS 沙箱或 Git 回滚。

### M3.2 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M32-001 | `go test ./internal/app -run 'Approval|PermissionMemory' -count=1` | 选择移动、数字键、确认、拒绝和当前会话记忆通过 | `artifacts/verification/m3.2/full-check.txt` |
| AC-M32-002 | `go test ./internal/agent ./internal/app -count=1` | 安全错误摘要、紧凑工具反馈和既有 chat 行为通过 | `artifacts/verification/m3.2/full-check.txt` |
| AC-M32-003 | `go run ./cmd/drift chat -w .` | 审批选择完成后面板消失；批准、记忆批准、拒绝和取消行为正确 | `artifacts/verification/m3.2/manual-acceptance.txt` |
| AC-M32-004 | 全量测试、vet、构建、diff 检查和重定向输出 | 全部退出码为 0，非 TTY 无 ANSI | `artifacts/verification/m3.2/full-check.txt` |

## M2.5：消息完整性与会话时间线

M2.5 修正运行时控制提示的消息来源：预算耗尽和伪工具重试提示只作为当次请求的 system context，不作为用户消息保存在完整会话中；完整会话快照不再持久化 Provider 的 `reasoning_content`。本阶段新增不显示正文的结构化时间线，便于检查快照消息形态而不扩大敏感内容暴露面。

### 当前范围

- 强制终答时的预算和 DSML 重试提示只影响当前模型请求，后续 Runner 消息与新保存的完整会话不含这类伪用户消息。
- `.drift/sessions/` 的 JSON 快照继续作为恢复格式；不改为 JSONL，也不新增逐消息时间戳。
- `drift session timeline <id>` 只输出序号、角色、工具调用名、工具调用 ID 和正文长度；不输出用户、助手或工具正文。
- 既有快照不自动改写；历史快照中已经保存的内容不会被本阶段删除。需要干净测试时创建新会话。

### M2.5 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M25-001 | 触发工具/读取预算终答和 DSML 终答重试的单元测试 | 最终请求在 system context 含控制提示，Runner 消息不新增伪用户提示 | `artifacts/verification/m2.5/full-check.txt` |
| AC-M25-002 | 保存带 `reasoning_content` 的消息并重新加载 | 快照 JSON 不含该字段，恢复消息不含该字段 | `artifacts/verification/m2.5/full-check.txt` |
| AC-M25-003 | `drift session timeline <id>` | 仅显示结构元数据，不回显提示词、回答或工具结果 | `artifacts/verification/m2.5/manual-acceptance.txt` |
| AC-M25-004 | 全量测试、vet、构建和 diff 检查 | 全部退出码为 0 | `artifacts/verification/m2.5/full-check.txt` |

## M2.4：按日期分片的会话与审计存储

M2.4 保持 `session` 与 `audit` 的职责分离：完整会话和脱敏审计都按 UTC 年/月/日写入日期目录；不再读取旧的平铺路径，也不在 `audit` 命令中增加会话管理操作。

### 当前范围

- 完整会话写入 `.drift/sessions/YYYY/MM/DD/session-<timestamp>-<id>.json`；审计写入 `.drift/audits/YYYY/MM/DD/run-<timestamp>-<audit-id>.jsonl`。
- `session list/show/rename/delete/prune` 只操作完整会话；`audit list/show` 只读取脱敏 JSONL。
- 会话和审计读取递归日期目录；旧平铺文件不兼容读取，由用户使用清理命令或人工清理。
- 日期使用 UTC；会话按创建日期归档，更新标题不会改变归档目录。

### M2.4 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M24-001 | 新建 chat、运行单次请求 | 会话与审计分别落入 UTC 日期目录 | `artifacts/verification/m2.4/manual-acceptance.txt` |
| AC-M24-002 | `session list/show/rename/delete/prune` | 只管理日期目录中的完整会话 | `artifacts/verification/m2.4/manual-acceptance.txt` |
| AC-M24-003 | `audit list/show` | 递归读取日期目录，不出现 rename/delete/prune | `artifacts/verification/m2.4/manual-acceptance.txt` |
| AC-M24-004 | 旧平铺路径检查 | 旧文件不被自动迁移或读取 | `artifacts/verification/m2.4/manual-acceptance.txt` |
| AC-M24-005 | 全量测试、vet、构建和 diff 检查 | 全部退出码为 0 | `artifacts/verification/m2.4/full-check.txt` |

## M2.3：聊天内会话切换

M2.3 在现有完整会话快照之上增加 chat 内 `/resume`、`/new` 和 `/rename`。`/resume` 可搜索并选择当前 workspace 的会话，也可通过 ID 直接切换；`/new` 在持久模式下保留旧快照并创建新的空会话；`/rename <标题>` 更新当前持久会话标题。切换采用先加载后替换，失败时当前上下文不变。

### 当前范围

- 选择器只显示至少包含一条消息的会话，按会话标题、首条用户消息和 ID 过滤，支持上下移动、Enter 选择和 Esc 取消；空快照不自动删除。
- `/rename <标题>` 只更新当前完整会话快照的标题，不请求 Provider；`--no-session` 模式拒绝持久标题操作。
- `session` 仍只管理 `.drift/sessions/` 完整快照，`audit` 仍只查看 `.drift/audits/` 脱敏审计。
- 不改变快照格式，不增加 `/audit`、`/tree`、`/fork`、写文件或命令执行能力。

### M2.3 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M23-001 | chat 输入 `/resume` | 出现可搜索、可滚动的会话选择器 | `artifacts/verification/m2.3/manual-acceptance.txt` |
| AC-M23-002 | 选择另一条会话、查看 `/status` | Session ID、消息和 Context 切换到目标快照 | `artifacts/verification/m2.3/manual-acceptance.txt` |
| AC-M23-003 | 输入 `/resume <id>`、非法 ID、Esc | 合法 ID 切换；非法 ID/Esc 不改变当前会话 | `artifacts/verification/m2.3/manual-acceptance.txt` |
| AC-M23-004 | 输入 `/new` 与 `--no-session` 下输入 `/new` | 持久模式保留旧快照并创建新 ID；临时模式只清空内存 | `artifacts/verification/m2.3/manual-acceptance.txt` |
| AC-M23-005 | 全量测试、vet、构建和 diff 检查 | 全部退出码为 0 | `artifacts/verification/m2.3/full-check.txt` |

## M2.2：Workspace 存储布局重命名

M2.2 将完整会话快照放入 `.drift/sessions/`，将脱敏运行审计放入 `.drift/audits/`，让目录名称直接表达职责。旧 `.drift/conversations/` 和旧审计 `.drift/sessions/*.jsonl` 保守迁移并兼容读取，不改变 JSON 快照或 JSONL 格式。

### 当前范围

- 新会话：`.drift/sessions/conv-<id>.json`；新审计：`.drift/audits/run-*.jsonl`。
- 旧 `conversations/*.json` 按文件迁入新 sessions；旧 `sessions/run-*.jsonl` 按文件迁入 audits；同名目标不覆盖。
- 新运行只写新目录；读取优先新目录并回退旧目录。
- `drift session list/show/rename/delete/prune` 只管理完整可恢复会话；`drift audit list/show` 只查看脱敏 JSONL 审计。两套命令不互为别名，也不在 chat 中增加 `/audit`。
- `.drift/` 整体继续被 Git 忽略；本阶段不新增 `tmp/`。

### M2.2 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M22-001 | `go test ./internal/layout ./internal/conversation ./internal/session ./internal/app -count=1` | 布局迁移、新写入和旧目录回退通过 | `artifacts/verification/m2.2/full-check.txt` |
| AC-M22-002 | `go test ./... -count=1`、`go vet ./...`、build、diff | 全部退出码为 0 | `artifacts/verification/m2.2/full-check.txt` |
| AC-M22-003 | 运行一次 chat、`session list` 和 `audit list` | 新文件分别出现在 `sessions/` 和 `audits/`，两套命令读取对象不交叉 | `artifacts/verification/m2.2/manual-acceptance.txt` |

## M2.1：只读探索预算调整

M2.1 将单次运行的工具调用上限由 6 次提高到 12 次、模型请求上限由 4 次提高到 6 次，以支持真实项目的目录、规格与关键文件探索；累计成功工具结果仍为 512 KiB，单文件读取仍为 128 KiB。

### 当前范围

- 仅调整 `agent.MaxToolCalls`：6 → 12、`agent.MaxModelRequests`：4 → 6；不新增配置项，不允许无限循环。
- 达到 12 次或读取总量上限后，工具 schema 仍会撤掉；模型后续工具请求不会执行，并会获得一次或多次剩余的无工具终答机会。
- 终答请求会重新注入只读与 Skill system context；若模型在无工具请求中仍返回工具调用或 DSML，运行时只重试普通文本，不执行伪工具。
- 三个只读工具、workspace 路径边界、DSML 守卫、Session 审计与完整会话隐私规则保持不变。

### M2.1 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M21-001 | `go test ./internal/agent -run TestMaxToolCallsSupportsProjectExploration -count=1` | 当前工具调用上限为 12 | `artifacts/verification/m2.1/full-check.txt` |
| AC-M21-002 | `go test ./... -count=1`、`go vet ./...`、build、diff | 全部退出码为 0 | `artifacts/verification/m2.1/full-check.txt` |
| AC-M21-003 | `drift --trace -skill project-overview -p "介绍一下当前项目"` | 可进行超过 6 次的只读探索或直接回答；没有额外工具类型 | `artifacts/verification/m2.1/manual-acceptance.txt` |

## M2.0：Workspace Skills

M2.0 增加 Codex 风格的 workspace-local Skills。Skill 只作为显式选择的额外 system context，不改变三个只读工具、路径保护、模型请求预算或会话隐私边界；运行时会要求 Skill 在证据足够时停止探索，并以普通文本完成回答。

### 当前范围

- 只从 `.drift/skills/<name>/SKILL.md` 发现；名称为 ASCII 小写字母、数字和连字符，长度 1～64。
- `skill list` 和 `skill show` 在 Provider 创建前执行，不需要 API Key。
- `-skill <name>` 可用于单轮 `-p` 和 `chat`；一次运行最多一个 Skill，恢复进程时需要重新选择。
- `SKILL.md` 必须是 UTF-8、最大 64 KiB；不读取全局目录、绝对路径、符号链接、嵌套引用、脚本或网络内容。
- Skill 文本注入 provider-neutral system context，不写入脱敏审计正文或完整会话元数据；审计最多记录 Skill 名称。

### M2.0 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M20-001 | `go test ./internal/skill -count=1` | 名称、目录、大小、UTF-8 和 symlink 边界通过 | `artifacts/verification/m2.0/full-check.txt` |
| AC-M20-002 | `drift skill list/show -w .` 无 API Key运行 | 列出和查看本地 Skill，不创建 Provider | `artifacts/verification/m2.0/manual-acceptance.txt` |
| AC-M20-003 | `drift -skill project-overview -p "..."` | Skill 出现在 system context，工具仍只有三个只读工具 | `artifacts/verification/m2.0/manual-acceptance.txt` |
| AC-M20-004 | 检查 Session 和完整快照 | 不出现 Skill 正文、密钥或绝对路径 | `artifacts/verification/m2.0/manual-acceptance.txt` |
| AC-M20-005 | 全量测试、vet、build、diff | 全部退出码为 0 | `artifacts/verification/m2.0/full-check.txt` |

## M1.10：运行过程反馈

M1.10 在真实 TTY 中显示只读工具调用的安全进度摘要，并将本轮完成标记统一为 ASCII 英文 `Done`，降低终端编码差异影响。

### 当前范围

- `tool_call` 显示工具名和经过校验的相对路径，`tool_result` 显示工具名和结果字节数。
- 进度行不显示原始 arguments、模型思维链、文件内容、密钥或绝对路径。
- 完成标记为 `Done - <seconds>s`；非 TTY 保持无 ANSI 的纯文本输出。
- Agent、Provider、Session、trace、取消和 `/status` 的语义不改变。

### M1.10 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M110-001 | TTY 直接回答 | 显示助手标记、正文和英文 `Done` | `artifacts/verification/m1.10/manual-acceptance.txt` |
| AC-M110-002 | TTY 读取 README.md | 按顺序显示工具名、相对路径和结果字节数，不泄露原始参数或正文 | `artifacts/verification/m1.10/manual-acceptance.txt` |
| AC-M110-003 | 重定向输入并检查输出 | 无 ANSI，完成标记为 `Done` | `artifacts/verification/m1.10/full-check.txt` |
| AC-M110-004 | 全量测试、vet、build、diff | 全部退出码为 0 | `artifacts/verification/m1.10/full-check.txt` |

## M1.9：Anthropic Messages Provider

M1.9 增加可选的 Anthropic Messages SSE Provider，验证 Provider 协议差异止于 `internal/llm` 适配层，Agent、工具、会话和终端交互继续复用同一条运行链路。

### 当前范围

- `-provider openai|anthropic` 选择 Provider，默认仍为 `openai`；`DRIFT_PROVIDER` 可提供默认值。
- Anthropic 使用 `ANTHROPIC_API_KEY`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_MODEL`，请求 `/messages`，发送 `x-api-key` 与 `anthropic-version`。
- Anthropic 文本、`tool_use`、`tool_result`、结束原因和流式 usage 映射为现有 `llm` 消息与事件；三个只读工具的 schema 由适配层转换。
- 缺少对应 Key 或 Model、未知 Provider 在发起请求前失败；错误沿用稳定 stage，不记录密钥、Authorization、原始响应或完整请求体。
- 不实现重试、Thinking、服务器工具、MCP、写文件、命令执行或 Provider 专用字段越过 `internal/llm`。

### M1.9 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M19-001 | `go test ./internal/llm/anthropic ./internal/app -count=1` | 请求映射、SSE、工具往返、usage 和配置选择通过 | `artifacts/verification/m1.9/provider-test.txt` |
| AC-M19-002 | 配置 Anthropic 环境变量后运行 `-provider anthropic -p ...` | 能完成文本回答和只读工具调用，Agent 无 Provider 专有分支 | 人工运行与测试日志 | `artifacts/verification/m1.9/manual-acceptance.txt` |
| AC-M19-003 | 缺少 Key、Model 或使用未知 Provider | Provider 请求数为 0，返回可理解错误 | `artifacts/verification/m1.9/provider-test.txt` |
| AC-M19-004 | 检查 JSONL、trace 和错误响应 | 不出现 API Key、请求头、原始响应、绝对路径或完整请求体 | `artifacts/verification/m1.9/manual-acceptance.txt` |
| AC-M19-005 | 全量测试、vet、build、diff | 全部退出码为 0 | `artifacts/verification/m1.9/full-check.txt` |

## M1.8：最小终端输入层

M1.8 为真实 TTY 的 `drift chat` 增加单行输入组件，提供占位文本、受控光标和清晰的取消反馈；重定向和测试输入继续使用无 ANSI 的按行协议。

### 当前范围

- TTY 输入区显示暗色上下分隔线、青色 `❯` 和灰色 `Send a message...` 占位文本；输入后由组件显示单行文本和光标。
- 输入框页脚左侧显示灰色 `Enter 发送 · Ctrl+C 取消`，右侧按终端宽度对齐当前模型名；非 TTY 不显示页脚。
- Enter 提交非空文本；空文本继续等待；`exit`、`/exit`、`quit` 语义保持不变。
- 活动模型/工具轮按 Ctrl+C 时显示 `✖ 当前轮已取消；会话仍可继续`，不显示原始 `context canceled`；Agent 消息、Token 和完整快照继续回滚半轮。
- 空闲输入按 Ctrl+C 仍以退出码 130 结束；非 TTY 使用现有 `bufio.Scanner` 路径，不输出 ANSI 控制符。
- `/status` 保持当前语义颜色、字段对齐和六个字段，不增加 Memory、历史、多行编辑、Alt Screen、输入队列或完整 TUI。

### M1.8 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M18-001 | Windows Terminal 启动 `drift chat -w .` | 空输入显示分隔线、`❯` 和 `Send a message...`，输入时显示光标 | `artifacts/verification/m1.8/manual-acceptance.txt` |
| AC-M18-002 | 输入普通问题并按 Enter | 只发送一次消息，回答与完成耗时继续显示 | `artifacts/verification/m1.8/manual-acceptance.txt` |
| AC-M18-003 | 模型流或工具执行中按 Ctrl+C | 显示安全取消提示，chat 回到新输入框，后续问题可继续 | `artifacts/verification/m1.8/cancel-test.txt` |
| AC-M18-004 | 空闲输入时按 Ctrl+C | chat 退出码为 130 | `artifacts/verification/m1.8/cancel-test.txt` |
| AC-M18-005 | 重定向输入并检查输出 | 继续使用纯文本提示，无 ANSI 控制字符 | `artifacts/verification/m1.8/manual-acceptance.txt` |
| AC-M18-006 | 全量测试、vet、build、diff | 全部退出码为 0 | `artifacts/verification/m1.8/full-check.txt` |

## M1.7：单轮取消

M1.7 将进程生命周期 Context 与每轮请求 Context 分离，使 Ctrl+C 在模型流或只读工具执行期间只取消当前轮。

### 当前范围

- 活动轮次收到 Ctrl+C 时取消 Provider/工具共享的 child Context，chat 继续显示输入提示。
- Agent 对取消轮次做事务式回滚，不保留 user、assistant 或 tool 半轮消息；旧上下文和 Token 统计保持不变。
- 空闲等待输入时 Ctrl+C 结束 chat，退出码为 130。
- 脱敏 Session 只记录 `agent_cancelled` stage，不记录正文、原始参数或文件内容。
- 不实现 TUI、行编辑、多行输入、输入队列、后台任务、暂停/恢复和自动重试。

### M1.7 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M17-001 | Provider 流式输出期间按 Ctrl+C | 当前轮结束，chat 返回新提示符，进程不退出 | `artifacts/verification/m1.7/cancel-test.txt` |
| AC-M17-002 | 只读工具执行期间按 Ctrl+C | 工具收到取消，后续工具不再执行 | `artifacts/verification/m1.7/cancel-test.txt` |
| AC-M17-003 | 取消后检查 Runner/快照 | 半轮消息不保留，旧 Context 可恢复 | `artifacts/verification/m1.7/audit-test.txt` |
| AC-M17-004 | 取消后查看 `/status` | 历史 Context 和 Token 统计不被取消轮污染 | `artifacts/verification/m1.7/cancel-test.txt` |
| AC-M17-005 | 检查 JSONL | 有 `agent_cancelled`，没有正文和原始参数 | `artifacts/verification/m1.7/audit-test.txt` |
| AC-M17-006 | 空闲时按 Ctrl+C | chat 以退出码 130 结束 | `artifacts/verification/m1.7/cancel-test.txt` |
| AC-M17-007 | 全量测试、vet、build、diff | 全部退出码为 0 | `artifacts/verification/m1.7/full-check.txt` |

## M1.6：真实 Token 用量

M1.6 接入 OpenAI Compatible Provider 的真实 token usage，并贯穿 Agent 事件、完整会话快照、脱敏审计和 `/status`。

### 当前范围

- 流式请求带 `stream_options.include_usage=true`，解析 usage-only SSE chunk；缺失 usage 时不报错。
- 每次成功模型请求发出一个 `model_usage` 事件；会话累计输入/输出 token、已报告和未报告请求数，`/clear` 一并清零。
- `/status` 显示真实 `Tokens: <in> in / <out> out`，混合缺失 usage 时显示 `(partial)`；Context 仍是独立的 KB 字节估算。
- JSONL 审计只记录事件类型和数值 usage 字段；旧快照缺少字段时按零值兼容。
- 不实现自动重试、费用计算、第二 Provider、Memory 或 TUI。

### M1.6 验收

| ID | 验证方法 | 通过阈值 | 证据路径 |
| --- | --- | --- | --- |
| AC-M16-001 | OpenAI SSE usage-only chunk 测试 | 解析输入/输出/总 token，且请求包含 `include_usage` | `artifacts/verification/m1.6/provider-usage-test.txt` |
| AC-M16-002 | Agent usage 事件与 chat `/status` 测试 | 每次成功请求一条事件，状态显示完整/不可用/partial | `artifacts/verification/m1.6/chat-usage-test.txt` |
| AC-M16-003 | 会话保存、恢复、clear 测试 | 计数持久化，旧快照兼容，clear 清零 | `artifacts/verification/m1.6/storage-test.txt` |
| AC-M16-004 | 全量测试、vet、build、diff 检查 | 全部退出码为 0 | `artifacts/verification/m1.6/full-check.txt` |

## M1.5：运行时卫生与边界对齐

M1.5 修复 Discovery 目录污染和 DSML 守卫覆盖范围，并把架构文档对齐到当前只读 Runtime 身份。

### 当前范围

- `list_files` 与 `search_text` 遍历时跳过 `.worktrees` 和 `.codex-temp`。
- 所有无原生 `tool_calls` 且以 `stop` 结束的模型最终文本都拒绝 DSML/伪工具格式。
- 不增加写文件、删除文件、命令执行或新的 Provider。

### M1.5 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M15-001 | Discovery 测试包含 `.worktrees`、`.codex-temp` | 两个目录及其文件不会进入遍历结果 | 测试日志 | `artifacts/verification/m1.5/discovery-test.txt` | 临时目录进入模型上下文 |
| AC-M15-002 | 模拟原生工具调用后返回 DSML 文本 | 运行失败并报告伪工具格式，不输出伪工具文本 | 测试日志 | `artifacts/verification/m1.5/dsml-test.txt` | DSML 文本被当作回答输出 |
| AC-M15-003 | 检查架构与当前范围文档 | 工具、只读边界和后续候选与代码一致 | 文件检查 | `artifacts/verification/m1.5/docs.txt` | 文档继续承诺 write/edit/exec |

## M1.4：手动上下文压缩

M1.4 为 `drift chat` 增加 `/status` 和手动 `/compact`，让用户在 1 MiB 上下文上限前压缩旧消息，而不必完全 `/clear`。压缩请求不带工具，失败时保留旧上下文。

### 当前范围

- `/status` 只读取 Runner 内存和启动元数据，显示 Session ID、Model、Context、Tokens、Tools 和 Workspace，不请求 Provider、不写审计、不修改快照；Context 使用十进制 KB 估算，Tokens 在 Provider 未返回 usage 时为 `unavailable`。
- `/compact` 使用当前 Provider 发起一次无 tools 请求，生成摘要并保留最近一组完整消息；成功后原子替换 Runner 上下文。
- Provider 错误、空摘要、DSML 伪工具文本、取消或持久化保存失败时，Runner 消息保持不变，chat 继续运行。
- 持久 chat 压缩成功后更新完整快照；`--resume` 恢复压缩后的摘要上下文。临时模式不写完整快照。
- 新增压缩事件只记录前后字节数、消息数、保留数和错误 stage；Session/Trace 不记录摘要正文。
- `clear`、`status`、`compact` 仍是普通文本提示；`/clear`、`/status`、`/compact` 才执行对应命令。真实终端用分隔线、彩色提示符、助手标记和完成耗时区分交互，非终端输出保持纯文本；压缩不增加文件写入、删除或命令执行能力。
- 摘要可能包含用户输入或文件内容，不是安全擦除证明；不实现自动压缩、后台压缩、摘要树、fork、`/undo`、摘要编辑、加密、云同步或正文搜索。

### M1.4 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M14-001 | chat 输入 `/status` | Provider 请求数为 0，只输出英文状态字段与安全上下文估算 | 测试日志 | `artifacts/verification/m1.4/status-test.txt` | 输出正文或请求模型 |
| AC-M14-002 | chat 输入 `/compact`，检查本地模拟 Provider 请求 | 请求不包含 tools；成功后保留摘要和最近消息 | 测试日志 | `artifacts/verification/m1.4/compact-test.txt` | 执行工具或未替换上下文 |
| AC-M14-003 | 模拟 Provider 超时、空响应、DSML 或取消 | 原上下文不变，chat 可继续，审计只有计数和 stage | 测试日志 | `artifacts/verification/m1.4/compact-error-test.txt` | 失败破坏上下文或泄露摘要 |
| AC-M14-004 | 持久 chat 压缩后退出并 `--resume` | 恢复摘要后的快照，不保存 Provider 配置字段 | 人工运行与文件检查 | `artifacts/verification/m1.4/resume.txt` | 恢复旧正文或快照未更新 |
| AC-M14-005 | 查看压缩后的 Session/Trace | 只出现安全计数、事件名和 stage，不出现摘要正文 | 文件检查 | `artifacts/verification/m1.4/audit.txt` | 审计或 trace 泄露正文 |
| AC-M14-006 | `go test ./... -count=1`、`go vet ./...`、`go build -o .codex-temp\\drift-m14.exe ./cmd/drift`、`git diff --check` | 四条命令退出码均为 0 | 命令日志 | `artifacts/verification/m1.4/` | 任一命令非 0 |
| AC-M14-007 | chat 输入普通 `status`、`compact`，`/status`，并完成一次普通提问 | 普通文本只提示斜杠命令；`/status` 不请求 Provider；终端有分隔线、提示符、助手标记和耗时；非终端输出无 ANSI 控制符 | 测试日志与人工运行 | `artifacts/verification/m1.4/chat-feedback.txt` | 请求模型或输出格式不符合约定 |

## M1.3：会话索引与生命周期管理

M1.3 在 M1.2 单快照恢复基础上增加会话标题、列表限制、删除预览和按时间清理；不改变恢复协议，也不把脱敏 Session 审计当作恢复来源。

### 当前范围

- 快照支持可选用户标题 `title`，最多 120 个 UTF-8 字节，禁止换行、NUL 和控制字符；不自动复制首条提示词。
- `conversation list [--limit N]` 按 `updated_at` 倒序展示元数据，默认限制 50 条；不输出消息正文。
- `conversation rename <id> <title>` 只更新指定快照的标题和更新时间，不请求 Provider。
- `conversation prune --before <RFC3339>` 只预览候选；增加 `--yes` 后才删除更新时间早于阈值的精确快照。
- `conversation prune` 不删除 `.drift/sessions/*.jsonl`，不跨 workspace，不递归删除目录。
- 所有 `conversation` 命令在 `.env` 加载和 Provider 创建前分发，无 API Key 也可运行。
- 不实现正文历史、fork、树状导航、`/undo`、自动摘要、正文搜索、导出、加密、云同步或后台自动清理。

### M1.3 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M13-001 | `go test ./internal/conversation -run 'TestStore(Rename|Before|Prune)' -count=1` | 标题保存/加载、非法标题、旧快照兼容、时间筛选和过期清理通过 | 测试日志 | `artifacts/verification/m1.3/store-test.txt` | 任一 Store 边界失败 |
| AC-M13-002 | `go test ./internal/app -run 'TestConversation(Rename|Limit|Prune)' -count=1` | rename、limit、预览不删除、`--yes` 精确清理通过 | 测试日志 | `artifacts/verification/m1.3/command-test.txt` | 命令错误删除或输出正文 |
| AC-M13-003 | 无 API Key 执行 `conversation list/show/rename/prune` | 命令成功或按输入返回 2，Provider 请求计数为 0 | 人工运行日志 | `artifacts/verification/m1.3/no-key-commands.txt` | 加载 Provider 或要求 API Key |
| AC-M13-004 | 创建新旧会话后手工运行 list、rename、prune 预览和确认 | 列表限制生效；标题可见；预览不删除；确认只删阈值前快照，Session 保留 | 人工运行日志与文件检查 | `artifacts/verification/m1.3/manual-acceptance.txt` | 误删新会话或 Session |
| AC-M13-005 | `go test ./... -count=1`、`go vet ./...`、`go build -o .codex-temp\\drift-m13.exe ./cmd/drift`、`git diff --check` | 四条命令退出码均为 0 | 命令日志 | `artifacts/verification/m1.3/` | 任一命令非 0 |

## M1.2：本地对话持久化

M1.2 让 `drift chat` 默认保存本地完整上下文，并通过显式 `--resume` 恢复；`--no-session` 用于不保存完整正文的临时对话。完整会话与 M0.9 脱敏审计分离，审计 JSONL 永远不是恢复来源。

### 当前范围

- 完整快照保存到 workspace 内 `.drift/conversations/<id>.json`；创建目录/文件使用 `0700`/`0600` 目标权限，并通过临时文件替换保存。
- `chat --resume` 恢复当前 workspace 最近快照，`chat --resume <id>` 恢复指定快照；文件型 `-w`、跨 workspace 和 `--no-session` 组合均拒绝。
- 默认 chat 启动显示 Session ID 和完整上下文提示；每轮成功后保存 Runner 消息，Provider 或工具失败的半轮不保存。
- `/clear` 在持久模式先写空快照，成功后才清理 Runner；保存失败时不清理当前内存上下文。临时模式仍只保留脱敏审计。
- `conversation list/show/delete <id> --yes` 只查看/删除元数据与指定快照，不请求 Provider；`show` 不回显正文。
- 快照可能包含提示词、回答和工具结果，不承诺脱敏，不应上传或共享；不保存 API Key、Authorization、Provider URL、模型名或 workspace 绝对路径。
- 本阶段不实现 fork、树状历史、自动摘要、正文搜索、导出、加密、云同步和跨 workspace 恢复。

### M1.2 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M12-001 | `go test ./internal/conversation -count=1` | 快照保存/加载保留 tool message；非法 ID、坏 JSON、排序、精确删除通过 | 测试日志 | `artifacts/verification/m1.2/conversation-test.txt` | 任一存储边界失败 |
| AC-M12-002 | `go test ./internal/app -run 'TestConversation|TestSession' -count=1` | list/show/delete 只输出元数据，不要求 API Key，Session 行为不回归 | 测试日志 | `artifacts/verification/m1.2/command-test.txt` | 输出正文或误加载 Provider |
| AC-M12-003 | `go test ./internal/app -run 'TestChat(Resume|NoSession|PersistentClear|RejectsPersistence)' -count=1` | 恢复请求包含旧上下文；临时模式无完整快照；clear 保存空快照；冲突参数退出码 2 | 测试日志 | `artifacts/verification/m1.2/chat-persistence-test.txt` | 任一生命周期约束失败 |
| AC-M12-004 | 人工运行 `chat`、`--resume`、`--no-session`、`/clear`、`conversation show` | 默认保存并可恢复；show 不泄露正文；no-session 只产生审计；clear 后旧消息消失 | 人工运行日志 | `artifacts/verification/m1.2/manual-acceptance.txt` | 恢复跨 workspace 或展示正文 |
| AC-M12-005 | 检查 `.drift/conversations/` 与 `.drift/sessions/` 内容 | 完整快照可恢复；Session 仍只有脱敏摘要；不出现 API Key/Authorization/Provider URL/绝对 workspace | 文件检查 | `artifacts/verification/m1.2/storage-inspection.txt` | 敏感配置落盘或审计被恢复使用 |
| AC-M12-006 | `go test ./... -count=1`、`go vet ./...`、`go build -o .codex-temp\\drift-m12.exe ./cmd/drift` | 三条命令退出码均为 0 | 命令日志 | `artifacts/verification/m1.2/` | 任一命令非 0 |
| AC-M12-007 | `git diff --check` 与计划/文档链接检查 | 无空白错误，M1.2 文档、README、Process 和计划与实现一致 | 命令日志 | `artifacts/verification/m1.2/docs.txt` | 文档描述过期或链接失效 |

## M1.1：交互上下文管理

M1.1 为 `drift chat` 增加进程内上下文硬上限和 `/clear`，避免长时间交互无限累积消息，同时不引入自动摘要或 Session 恢复。

### 当前范围

- Runner 按 UTF-8 字节估算 system 指令、消息、tool call、tool result、reasoning content 和工具 schema；初始上限为 1 MiB，不等同于 token 数。
- 每次 Provider 请求前检查上下文预算。超过上限时不发 Provider 请求，写入 `stage=agent_context_limit` 的安全错误事件。
- `drift chat` 支持 `/clear`，只清空当前 Runner 的 user、assistant、tool 消息，保留 workspace、focus、Provider 和工具注册表。
- 上下文超限只结束当前输入轮次，chat 继续运行并提示输入 `/clear`；Provider、工具、输出和取消错误沿用 M1.0 退出行为。
- 不自动截断旧消息、不调用模型生成摘要、不从 `.drift/sessions/*.jsonl` 恢复上下文。

### M1.1 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M11-001 | 运行 `go test ./internal/app -run TestChat -count=1` | `/clear` 不发请求；清理后下一轮请求不携带旧消息；超限后可继续对话 | 测试日志 | `artifacts/verification/m1.1/go-test.txt` | 任一聚焦测试失败，或 chat 在超限后退出 |
| AC-M11-002 | 运行 `go test ./internal/agent -run 'TestRunnerContext' -count=1` | 上下文估算包含消息与 schema；超过 1 MiB 时 Provider 请求计数为 0，stage 为 `agent_context_limit` | 测试日志 | `artifacts/verification/m1.1/go-test.txt` | 仍发起超限请求，或错误 stage 不匹配 |
| AC-M11-003 | 运行 `go test ./... -count=1`、`go vet ./...`、`go build -o .codex-temp\\drift-m11.exe ./cmd/drift`、`git diff --check` | 四条命令退出码均为 0 | 命令日志 | `artifacts/verification/m1.1/` | 任一命令非 0 |
| AC-M11-004 | 使用构建产物输入 `hello`、`/clear`、`hello`、`exit`；查看最新 `.drift/sessions/*.jsonl` | 退出码为 0；清理后第二轮成功；审计仅保留脱敏摘要、相对路径和字节数 | 人工运行日志与审计检查 | `artifacts/verification/m1.1/chat-clear.txt`<br>`artifacts/verification/m1.1/session-list.txt` | `/clear` 后不能继续，或审计出现提示词、回答正文、文件内容或绝对路径 |

## M1.0：交互式只读对话

M1.0 在同一进程内复用 Agent Runner，让用户可以连续输入问题并共享本轮已经获得的模型上下文；Session 仍然只是脱敏审计，不参与上下文恢复。

### 当前范围

- 新增 `drift chat` 入口；`-w`、`--trace`、`-model` 和 `-base-url` 与单次模式一致，`-p` 与 chat 互斥。
- 同一 chat 进程只创建一次 Provider、workspace、工具注册表和 Session Writer；每行非空输入复用同一个 `agent.Runner`。
- 后续问题携带此前的 user、assistant、tool 消息；首个问题仍带系统约束和三个只读工具 schema。
- 空行忽略，`exit`、`/exit`、`quit` 和 EOF 正常退出；取消返回 130，Provider/工具/输出错误返回 1。
- Provider 以 `stop` 结束但没有文本时返回 `agent_empty_response`，写入错误审计并结束本次 chat，不把空回答当成成功。
- 每轮仍受 M0.4 的 4 次模型请求、6 次工具调用、512 KiB 累计结果和 128 KiB 单文件读取限制。
- 对话上下文只保存在内存；进程结束后丢失，不从 `.drift/sessions/*.jsonl` 恢复，不把审计正文发送给模型。

### M1.0 验收

- 连续两个问题共享同一个 Runner 上下文，第二次请求包含第一次的 user/assistant 消息。
- `exit`、`/exit`、`quit`、EOF 和空行不产生无意义的 Provider 请求；chat 只生成一个安全审计文件。
- 单次模式行为保持兼容；stdout 仍只输出回答，trace 仍写 stderr，Session 不保存提示词、回答和文件内容。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.9：安全 Session 审计与查看器

M0.9 明确 `.drift/sessions/*.jsonl` 是本地审计记录，而不是可以直接发送给 LLM 的上下文；同时提供不需要 API Key 的只读查看命令。

### 当前范围

- 新写入 JSONL 不保存用户提示词、原始工具 arguments、工具结果内容或模型回答正文；只保存 `<redacted>` 占位符、字节数、工具名、事件类型、时间和错误 `stage`。
- 已知只读工具调用可额外保存经过校验的相对 `path`；绝对路径、`..`、`.env`/`.env.*` 和控制字符路径会被省略，原始 arguments 仍不保存。
- 运行完成或异常结束时可保存安全的 `finish_reason`；该字段只表示 `stop`、`length` 等稳定原因，不保存 Provider 响应正文。
- `session list` 查看当前目录下的会话文件和事件数量；`session show <path>` 查看事件摘要、工具名、字节数和错误阶段。
- 查看器不请求 Provider、不读取 `.env`、不修改会话文件，也不回显旧 JSONL 中可能存在的正文内容。
- 读取兼容旧 JSONL；旧记录中的 `text`、`arguments` 和 `result` 只用于解析，不会被查看器输出。
- 本阶段不实现 Session 恢复给 LLM；恢复上下文需要未来单独的显式授权设计。

### M0.9 验收

- 新运行的 JSONL 不包含测试提示词、工具参数和文件正文，且保存对应 `text_bytes`、`argument_bytes`、`result_bytes`。
- 合法的只读工具调用会保存相对 `path`；绝对路径、`..` 和 dotenv 路径不会落盘。
- `go run ./cmd/drift session list` 能列出会话；`go run ./cmd/drift session show <path>` 能输出安全摘要。
- `session list/show` 在缺少 API Key 或 Provider 不可用时仍可运行；输出不出现旧记录正文、密钥或绝对路径。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.8：Trace 运行可观测性

M0.8 复用现有 Runtime Event，增加可选的 `--trace` 诊断输出，让用户可以观察 Agent 的关键执行阶段，同时保持 stdout 的最终回答契约和 M0.7 的只读边界不变。

### 当前范围

- 传入 `--trace` 时，运行摘要写入 stderr；不传时现有 stdout/stderr 行为保持不变。
- Trace 输出 `run_started`、已注册工具名、工具结果字节数、错误 `stage` 和 `run_finished`；不输出提示词、文件内容、原始工具 arguments、API Key、Authorization、Provider 响应正文或绝对路径。
- `text_delta` 不把回答正文复制到 trace；最终回答仍只写 stdout，会话仍写入脱敏 JSONL。
- Trace 是 best effort 诊断输出，stderr 写入失败不改变 Agent 主流程。

### M0.8 验收

- `go run ./cmd/drift --trace -p "你好"` 时 stdout 只有最终回答，stderr 至少包含 `run_started` 和 `run_finished`。
- 包含工具调用的运行会在 stderr 显示工具名与结果字节数；失败运行显示稳定的 `stage`。
- trace 不出现回答正文、文件内容、API Key、原始 arguments 或本地绝对路径。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.7：Provider 诊断与读取保护

M0.7 保持 M0.6 的 workspace、工具、预算和只读边界，补齐真实联调中 Provider 失败的可定位信息，并拒绝将含 NUL 字节的二进制内容送进模型上下文。

### 当前范围

- Provider 失败使用安全的固定中文提示，同时在错误事件中记录稳定的 `stage`：连接、超时、HTTP 状态、非 SSE 响应、SSE 无效 JSON、服务端 SSE 错误、事件/单行过大、读取失败与未收到 `[DONE]` 分别可区分。
- JSONL 的 `error` 事件新增可选 `stage` 字段；不记录 Provider 响应正文、请求体、API Key 或本地绝对路径。
- `read_file` 全文读取或页内读取到 NUL 字节时拒绝该文件为二进制文件；分页成功结果增加实际行号范围，例如 `read_file: lines 21-40`。
- 仍不引入重试、退避、Provider 专用配置、多 Provider 或自动恢复；这些需要真实的失败统计后再决定。

### M0.7 验收

- 本地 SSE 模拟服务验证无效 JSON 会以 `provider_sse_invalid_json` 写入会话错误事件；超时和超长 SSE 单行有可区分的终端错误文本。
- `read_file` 拒绝含 NUL 字节的文件；分页结果包含行号范围与下一页 offset 提示。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.6：分页读取与 Discovery 目录忽略

M0.6 解决大文件读取和大型项目发现结果过于嘈杂的问题，同时保持 M0.5 的 workspace、Focus 和只读边界。

### 当前范围

- `read_file` 保持 `path` 必填，并增加可选的 `offset`（0-based 行偏移）和 `limit`（读取行数）参数。传入任一分页参数时，默认 `offset=0`、`limit=2000`；页结果仍受单次 128 KiB 上限保护。
- 不传分页参数时，小文件继续全文读取；超过 128 KiB 的文件返回使用 `offset`/`limit` 分页的提示，不再把完整大文件放入模型上下文。
- 分页结果在达到 `limit` 且后面仍有内容时追加下一页 offset 提示，模型可以继续读取后续范围。
- `list_files` 与 `search_text` 递归发现时跳过 `.git`、`.foxcode`、`.codex`、`.claude`、`.drift`、`node_modules`、`.venv`、`__pycache__`、`.tox` 和 `.mypy_cache`。忽略只作用于 discovery，不影响用户明确调用 `read_file` 读取普通文件。
- workspace 相对路径、符号链接、dotenv、特殊文件、Agent 请求/工具/累计结果预算和 JSONL 脱敏规则保持 M0.5 行为。

### M0.6 验收

- 分页读取覆盖 offset、limit、最后一页、非法参数和大文件提示；read_file schema 对模型公开分页参数。
- list/search 不返回默认忽略目录下的文件，明确 read_file 仍按 workspace 安全规则工作。
- 真实项目目录的发现结果规模明显收敛，`README.md` 等普通文件可以继续被模型读取和解释。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.5：显式 Workspace 与 Focus 文件

M0.5 增加 `-w` 目标选择，同时保持模型工具只能使用 workspace 内的相对路径。`-w` 指向目录时，该目录是 workspace 且没有 focus；指向普通文件时，文件父目录是 workspace，文件名是 focus；不传 `-w` 时保持启动命令当前目录作为 workspace。

文件 focus 只追加到首轮系统提示，不会自动调用 `read_file`，不增加模型请求、工具调用或读取预算。模型仍自行决定是否使用 `list_files`、`search_text` 或 `read_file`，工具参数继续使用如 `{"path":"README.md"}` 的相对路径。

`-w` 目标必须存在且是实际目录或普通文件；目标本身为符号链接、特殊文件、缺失路径或 `.env`/`.env.*` 文件时拒绝。解析失败在 Provider 请求前返回退出码 2，错误不暴露不必要的本地绝对路径。`.env` 仍从启动目录加载，会话 JSONL 改写入选定 workspace 的 `.drift/sessions/`。

### M0.5 验收

- 目录目标的工具读取和会话审计落在指定目录；文件目标使用父目录并在首轮提示注入相对 focus，且没有隐式读取。
- 不传 `-w` 的 M0.4 多轮行为、工具边界、请求/工具/读取预算、stdout 和 JSONL 脱敏规则保持兼容。
- 无效目标和 Provider 请求前失败路径由测试覆盖；绝对工具路径、`..`、符号链接、dotenv、目录和特殊文件继续拒绝。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.4：受限多轮只读探索 Agent

M0.4 将原来的固定两轮 `read_file` 流程扩展为受限多轮 Agent Loop。模型可以根据上一轮工具结果继续选择下一步，但运行仍只允许读取和搜索，不允许产生文件系统副作用。

### 当前范围

- 默认 Registry 按固定顺序提供三个原生只读工具：`list_files`、`search_text`、`read_file`。Agent 通过 Registry 查找工具，不硬编码工具分派。
- 单次运行最多发起 4 次模型请求、最多执行 6 次工具调用；达到工具调用或累计工具结果上限后，如果还有请求预算，会追加一次不带工具 schema 的最终说明请求。
- 所有成功工具结果进入当前对话的累计大小最多 512 KiB；`read_file` 单文件最多 128 KiB。
- `list_files` 从 workspace 根目录或给定的相对目录递归列出普通文件，最多返回 200 条并附带截断标记。
- `search_text` 搜索普通文本文件，最多扫描 200 个文件、返回 100 个匹配，输出最多 32 KiB 并附带截断标记；二进制文件会跳过。
- 工具路径必须是 workspace 内的相对路径；拒绝绝对路径、`..`、符号链接、目录、特殊文件以及路径中任意 `.env`/`.env.*` 段。不会写文件、删除文件、编辑文件、执行 shell 或运行程序。
- 工具调用按模型返回顺序串行执行；首轮工具前导文本不输出，CLI stdout 只输出最终回答。Runtime 事件继续追加到 `.drift/sessions/<run-id>.jsonl`，每个事件独立成行并即时 Flush。

### 调用与事件流程

```text
用户提示
  -> app 创建 Provider、默认 Registry、Session Writer
  -> agent 请求模型（首轮附带三个工具 schema）
  -> 聚合并校验 tool_calls
  -> 按顺序执行 list/search/read，追加 tool_result
  -> 未结束时再次请求模型（最多 4 次）
  -> stop 且没有 tool_calls：流式输出 text_delta，写入 run_finished
```

典型 JSONL 事件顺序如下；`text_delta` 的持久化内容仍为脱敏占位符：

```json
{"version":1,"type":"run_started","text":"探索当前项目"}
{"version":1,"type":"tool_call","tool_call_id":"call-list","tool":"list_files","arguments":"{\"path\":\"\"}"}
{"version":1,"type":"tool_result","tool_call_id":"call-list","tool":"list_files","result":"cmd/drift/main.go\\nREADME.md"}
{"version":1,"type":"tool_call","tool_call_id":"call-read","tool":"read_file","arguments":"{\"path\":\"README.md\"}"}
{"version":1,"type":"tool_result","tool_call_id":"call-read","tool":"read_file","result":"<redacted>"}
{"version":1,"type":"text_delta","text":"<redacted>"}
{"version":1,"type":"run_finished"}
```

### M0.4 验收

- 直接回答只请求一次模型；`list_files -> search_text -> read_file -> 最终回答` 等多轮路径按顺序携带 assistant/tool 消息，stdout 只保留最终回答。
- 测试覆盖 4 次模型请求、6 次工具调用、512 KiB 累计结果、128 KiB 单文件、200 条列表、200 个搜索文件、100 个搜索匹配和 32 KiB 搜索输出边界。
- 未知工具、非法参数、路径遍历、符号链接、dotenv、目录、特殊文件、工具错误、取消和 Provider 协议错误均返回受控结果；不执行 shell 或伪工具文本。
- JSONL 事件顺序、即时落盘和脱敏规则保持有效，不写入 API Key、Authorization、dotenv 内容或本地绝对路径。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.3：Runtime 核心与 JSONL 会话审计

- Agent 新增 provider-independent 的 Runtime Event：`run_started`、`tool_call`、`tool_result`、`text_delta`、`error` 和 `run_finished`；CLI 仍只把最终文本写入 stdout。
- 工具通过 Tool Registry 提供 schema 和按名称查找；当前默认 Registry 只有 `read_file`，不增加写入、删除、编辑、shell 或 exec 能力。
- 每次成功启动的运行在当前 workspace 的 `.drift/sessions/<run-id>.jsonl` 追加脱敏审计事件；该目录被 Git 忽略，当前只支持记录，不支持会话恢复、加载或上下文压缩。
- 会话记录不包含 API Key、Authorization header、`.env` 内容或本地绝对路径；JSONL 每行可独立解码，单行追加后立即 Flush。
- 为避免跨流式分片重建敏感内容，JSONL 中的 `text_delta` 仅保存 `<redacted>` 占位符；CLI stdout 仍保留完整最终回答。
- M0.2.1/M0.2.2 的四文件、单文件 128 KiB、总量 512 KiB、最多两次模型请求和 DSML 兼容性边界保持不变。

## M0.3 验收

- 直接回答仍只请求一次 Provider；工具路径仍按调用顺序执行，并且第二轮不携带工具 schema。
- fake tool 可以通过 Registry 注入并执行；重复工具名和未知工具返回受控错误。
- 本地 SSE 模拟服务验证 stdout 只包含最终文本，`.drift/sessions` 中包含运行开始、工具调用、工具结果、文本和结束事件。
- 每个 JSONL 行可被标准 JSON 解码器读取；错误、取消和工具失败不会把密钥、绝对路径或 dotenv 内容写入文件。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过。

## M0.2.2：环境配置与原生工具兼容性

- `.env` 位于当前工作目录时会被读取；缺少该文件是正常情况。可用 `Copy-Item .env.example .env` 创建本地模板。`.env` 被 Git 忽略，`.env.example` 只含空的 `OPENAI_API_KEY`，密钥不得提交、打印或写入日志。
- 配置优先级为命令行 `-model`/`-base-url` > 进程环境变量 > `.env` > 内置默认值。`OPENAI_API_KEY` 没有命令行参数，只从进程环境或 `.env` 读取；配置解析失败或缺少必要配置时，请求不会发出。
- 首轮请求包含唯一的原生 `read_file` schema 和只读系统指令；`run_command`、shell、exec 不可用，也没有新增工具。四文件、单文件 128 KiB、总量 512 KiB 和最多两次模型请求的 M0.2.1 限制保持不变。
- `<｜｜DSML｜｜ calls>` 与 `<|DSML|>` 是模型输出的文本标记，不是 OpenAI 原生 `tool_calls` 事件。当前兼容性守卫只检查首轮没有原生 `tool_calls` 且 `finish_reason` 为 `stop` 时缓存的首轮文本；该范围内会将标记报告为不兼容伪工具调用，既不执行也不向 stdout 输出。伴随原生工具调用的 DSML 文本和第二轮文本不在此守卫的检查范围内。

## M0.2.1：受限多文件只读 Agent Loop

- 使用 Go 1.26+ 和标准库实现本地 `drift` CLI，继续使用 OpenAI Compatible Chat Completions SSE。
- `drift -p "解释 README.md 的项目作用"` 以当前工作目录为只读 workspace。M0.2 的单文件调用仍支持；M0.2.1 允许模型在首轮最多请求四次 `read_file` 来读取其中的常规文件。
- 每次运行最多两次模型请求：第一轮提供唯一的 `read_file` schema；若模型直接完成则立即结束，若模型调用该工具则按调用顺序把全部结果带入同一个第二轮请求，第二轮不再提供工具且必须给出最终回答。
- `read_file` 仅接受 workspace 内的相对路径，拒绝绝对路径、`..`、符号链接（包括工作区内软链接）、目录、非常规文件、dotenv 凭据文件（`.env` 与 `.env.*`）和超过 128 KiB 的内容。单次任务成功读取内容合计最多 512 KiB；超过四个文件或总量上限会产生清晰的受限上限错误。工具错误以脱敏结果交给第二轮模型，不泄露本地路径或内容。
- 没有写入文件、删除文件、修改目录、执行 shell 命令、运行程序或其他工具的能力。模型只会看到一个只读文件工具。
- 历史 M0.2.1 行为保留 `OPENAI_API_KEY`、`OPENAI_MODEL`、`OPENAI_BASE_URL` 和 `-model`、`-base-url`；在 M0.2.2 中，API Key 可从进程环境或当前目录 `.env` 读取，模型及地址的命令行值覆盖两者（详见上方当前配置规则）。
- 第一轮直接回答仅在 `stop` 后写入 stdout；工具路径只流式写入第二轮文字，第一轮文本、推理和工具过程不输出。成功回答末尾没有换行时补一个换行；若第二轮失败，已流出的部分回答保留在 stdout，诊断写入 stderr，退出码为 1。
- Ctrl+C 取消请求并返回 130；单次网络请求时限为五分钟。正常完成（包括读取失败作为 tool result 后第二轮正常结束）返回 0，参数或配置错误返回 2，网络、协议、无效 Agent 状态或输出错误返回 1。

## 验收

- 本地 SSE 模拟服务验证首轮携带一个 `read_file` schema；M0.2 单文件和 M0.2.1 最多四文件调用的每个匹配 tool result 会按调用顺序构成同一个第二轮上下文。第二轮不携带工具，最终文本只写 stdout，且整个命令最多两次请求。
- 验证 SSE 中同一 `tool_calls` 的 `function.arguments` 按 `index` 分片聚合为完整调用；首轮 assistant 的 `reasoning_content` 与 tool call 一起回传至第二轮 assistant message。
- 验证直接回答仅在 `stop` 且没有 tool call 时只发起一轮请求；首轮 `stop` 加 tool call 仍进入只读路径处理，第二轮含任意 tool call、未知工具、非 `stop` 完成和流式协议错误都会失败。
- 验证读取边界：参数格式、路径遍历、符号链接、目录、非常规文件、单文件 128 KiB、总量 512 KiB、最多四个文件和工具错误脱敏。
- 缺少配置在请求前报错，`-h` 不需要密钥，Ctrl+C 保持退出码 130，错误或诊断不会泄露 API Key。
- `go test ./...`、`go vet ./...`、`go build ./cmd/drift` 与 `git diff --check` 通过；有有效环境变量时，手工运行 README 解释命令可确认真实只读联调。

## M0.2.2 验收

- `.env` 可选且忽略；进程环境变量覆盖 `.env`，命令行模型和地址覆盖两者；模板不含密钥，错误和测试输出不泄露 API Key。
- 首轮携带原生 `read_file` schema 和禁止伪工具的系统指令；只有首轮无原生工具调用且以 `stop` 结束的缓存文本含 DSML 标记时才清晰失败，不打印、不执行，不增加 `run_command`。伴随原生工具调用或第二轮文本中的 DSML 标记不由当前守卫拒绝。
- 只读边界仍为最多四个文件、每个 128 KiB、成功读取合计 512 KiB，且一次命令最多两次模型请求。
- `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 通过；无 `OPENAI_API_KEY` 时不宣称已完成真实 DeepSeek 联调。
