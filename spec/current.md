# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构边界见 [`doc/architecture.md`](../doc/architecture.md)。当前版本为 M3.28。M3.17 及更早阶段记录见 [`docs/spec-history.md`](../docs/spec-history.md)。

## M3.28：任务 Worktree 绑定

完整目标与验收标准见 [`m3.28-task-worktree.md`](m3.28-task-worktree.md)。任务状态现在可以持久化一个受控 `.worktrees/<name>` 绑定路径；本阶段不自动创建、删除或合并 worktree。

## M3.27：Git Worktree 隔离最小闭环

完整目标与验收标准见 [`m3.27-git-worktree.md`](m3.27-git-worktree.md)。本阶段补齐与现有 GitRevert 不同的 Agent 隔离能力：在 workspace 的 `.worktrees/<name>` 下创建、列出和删除受控 linked worktree。创建和删除必须显式 `--yes`；名称、Git revision 和 `.worktrees` 路径均经过校验；删除交给 Git 执行，脏 worktree 不会被强制删除。

## M3.26：MCP Client 生命周期收口

完整目标与验收标准见 [`m3.26-mcp-client-hardening.md`](m3.26-mcp-client-hardening.md)。

## M3.25：任务状态持久化与会话恢复

完整目标与验收标准见 [`m3.25-task-persistence.md`](m3.25-task-persistence.md)。

## M3.24：会话内任务管理最小闭环

完整目标与验收标准见 [`m3.24-task-tools.md`](m3.24-task-tools.md)。

## M3.23：MCP 集成验收与失败路径收口

完整目标与验收标准见 [`m3.23-mcp-acceptance.md`](m3.23-mcp-acceptance.md)。

## M3.22：本地 stdio MCP 安全闭环

M3.22 的完整目标、边界与验收标准见 [`m3.22-mcp.md`](m3.22-mcp.md)。本阶段只支持用户显式连接的本地 stdio MCP，不支持远程 transport、OAuth、自动安装或持久化 MCP 授权。

## M3.21：控制工具最小闭环

M3.21 只新增会话内澄清与工具发现：`AskUserQuestion` 和 `ToolSearch`。不实现 MCP、任务队列、Git worktree、Skill 安装或多 Agent。

- chat 初始仅向模型暴露两个控制工具；`ToolSearch` 只返回名称、类别和描述，模型必须在结果中明确选择工具后，完整 schema 才在下一次模型请求中加载；
- 内建的 `Glob`、`Grep`、`ReadFile`、`WriteFile`、`EditFile`、`DeleteFile`、`Bash` 均可被检索，未加载工具调用按未知工具拒绝；
- `AskUserQuestion` 支持单选、多选或自由文本；回答写入当前对话的 tool result。取消写入 `cancelled/question_cancelled` 审计状态，不等同于审批拒绝或工具失败；
- 两个控制工具均不修改权限模式、持久化授权、沙箱策略、workspace 或 change set。

验收要求：

| ID | 验证方法 | 通过阈值 | 失败判定 |
| --- | --- | --- | --- |
| AC-M321-001 | `go test ./internal/tool -run 'Test(ChatRegistryStartsWithOnlyControlSchemas|ToolSearchLoadsOnlySelectedMatchingSchemas|AskUserQuestionParsesStructuredArguments)' -count=1` | 初始 schema、选择性加载和参数边界通过 | 未加载工具可直接执行，或 schema 全量泄露 |
| AC-M321-002 | `go test ./internal/agent -run 'TestAskUserQuestion|TestToolSearchMakesLoadedSchemaAvailableOnNextRequest' -count=1` | 回答进入上下文；取消状态可区分；下一模型请求携带已加载 schema | 回答丢失、取消误报失败或 schema 不刷新 |
| AC-M321-003 | `go test ./internal/app -run TestParseQuestionAnswerAcceptsChoicesFreeTextAndCancellation -count=1` | 编号选项、自由文本和取消输入可解析 | 无法作答或误执行权限操作 |
| AC-M321-004 | `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 全部通过 | 任一命令失败 |

## M3.19–M3.20：安全收口与上下文管理

这两个阶段先于 ToolSearch、MCP 和多 Agent。它们不改变当前工具名称、权限模式、沙箱模式或 Git 回滚边界。

### M3.19：安全层与交互收口

M3.19 只收口当前已实现能力：提交并回归验证 Windows 命令提示、用户输入可见性、`off/auto/required` 沙箱结果、权限/审批/审计和 Git 回滚；不新增安全后端，不引入 Git 专用沙箱。

验收要求：

- Bash 在 Windows 下优先生成 `cmd.exe` 兼容命令，不再因模型生成 POSIX `ls/head/cat` 导致交互轮直接退出；
- Agent 轮失败后 chat 仍可继续输入；用户已提交的文本在失败轮中仍可见；
- `auto` 成功时记录实际后端和 `sandboxed=true`，不可用时明确记录 `sandboxed=false`，`required` 不可用时拒绝执行；
- 审批结果、权限模式、沙箱拒绝、工具失败和 Git 回滚失败在审计中保持可区分；
- 通过现有 TDD、全量测试、静态检查、构建和 diff 检查。

### M3.20：上下文管理最小闭环（已交付）

M3.20 复用现有 `Runner.Compact`、会话 JSONL 和工具输出限制，补齐上下文预算和历史可恢复性，不引入向量数据库、长期记忆服务或新的外部依赖。上下文达到 80% 字节预算时，chat 会在发送下一轮前自动压缩；也可继续使用 `/compact` 手动压缩。Bash 超限结果保留头尾并标记中间内容被裁剪；会话历史可通过 `drift session search <关键词>` 或 chat 内 `/search <关键词>` 检索，结果只返回会话和消息位置，不返回正文。

范围：

1. 在上下文接近预算时提供可预测的压缩提示，并保留最近消息、已验证事实、相对路径、决策和未完成任务；
2. 压缩失败时原子保持原上下文，成功后同步会话快照和结构化审计；
3. 对超长工具结果进行头尾保留和字节上限裁剪，并在上下文中标记裁剪原因；
4. 支持按会话元数据和关键词检索历史，返回摘要/位置，不把完整历史自动灌入当前上下文；
5. 历史、摘要、工具结果均不得写入凭据、完整命令输出或未经脱敏的绝对路径。

验收要求：

| ID | 验证方法 | 通过阈值 | 失败判定 |
| --- | --- | --- | --- |
| AC-M320-001 | Agent 压缩单测和失败注入测试 | 保留策略稳定；模型失败、空摘要、伪工具调用时原上下文不变 | 上下文丢失或产生不可执行摘要 |
| AC-M320-002 | 工具结果裁剪测试 | 超过预算时输出受限、含裁剪标记，未超限结果保持原文 | 结果无界增长或静默截断 |
| AC-M320-003 | Session 历史检索测试 | 只返回匹配会话的摘要/位置，不泄露正文和凭据 | 跨 workspace、全文注入或敏感信息泄露 |
| AC-M320-004 | Chat 集成测试 | 压缩、继续提问、恢复会话后上下文语义连续 | 恢复后工具、权限或工作区状态错乱 |
| AC-M320-005 | `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 全部通过 | 任一命令失败 |

ToolSearch/MCP/多 Agent 均以后续阶段单独立项，不属于 M3.20。

## M3.18：工具能力复核与补缺

M3.18 不重复实现已有的 `Glob`、`Grep` 和写入前文件状态校验，只补齐边界回归与临时 workspace 功能验收。Git 专用沙箱不在本阶段范围内。

### 验收

| ID | 验证方法 | 通过阈值 | 失败判定 |
| --- | --- | --- | --- |
| AC-M318-001 | `go test ./internal/tool -run 'TestGlob|TestGrep|TestSearch|ExternalChangeAfterPreview|TestWrite|TestEdit|TestDelete' -count=1` | Glob/Grep 边界、取消、限制和三类 stale preview 测试通过 | 越界、保护目录、结果上限或外部修改处理错误 |
| AC-M318-002 | `go test ./internal/app -run 'TestRunMultiTurnExplorationRoundTripAndSessionAudit|TestRunReadRoundTripMultipleFiles' -count=1` | 临时 workspace 的 Glob/Grep/ReadFile 组合链路通过 | 工具组合或会话审计回归 |
| AC-M318-003 | `go test ./internal/tool -run 'TestPublicToolNames|TestDefinitionsUsePublicToolNames' -count=1`、`go test ./internal/session -run 'TestReadEntries|TestListFiles' -count=1` | 当前公开工具名注册正确，历史 JSONL 可读取 | 旧记录损坏或生成旧工具名 |
| AC-M318-004 | `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 全量测试、静态检查、构建和 diff 检查通过 | 任一命令失败 |

## M3.16：安全闭环收口

M3.16 统一四种用户可见权限模式（`default/acceptEdits/plan/bypassPermissions`）、内部策略决策（`allow/ask/deny`）与人工审批结果（`allow_once/allow_persistent/deny/cancelled`），把 required 沙箱拒绝和工具失败写入结构化审计，并验证 partial change set 不会被不安全地恢复。`ask` 不是权限模式，只表示需要进入人工审批。

### 验收

| ID | 验证方法 | 通过阈值 | 失败判定 |
| --- | --- | --- | --- |
| AC-M316-001 | `go test ./internal/app -run 'TestPermission|TestApproval' -count=1` | 模式、精确规则、一次允许、持久化允许、拒绝和取消结果明确分离 | 权限模式或审批结果混淆 |
| AC-M316-002 | `go test ./internal/agent -run 'TestRunDeniedPreviewDoesNotExecuteWrite|TestRunEventsEmitsStructuredPermissionOutcome|TestRunEventsRecordsRequiredSandboxRefusal' -count=1` | 权限拒绝、沙箱拒绝和成功结果分别记录 | 允许绕过、拒绝原因丢失或工具误执行 |
| AC-M316-003 | `go test ./internal/session -run TestJSONLWriterRecordsStructuredPermissionAndSandboxOutcome -count=1` | 审计含结构化字段且正文/结果继续脱敏 | 审计泄露正文或缺少沙箱字段 |
| AC-M316-004 | `go test ./internal/changes -run 'TestRestore(CreateAndEdit|RefusesStaleTargetWithoutChanges|RefusesPartialChangeSetWithoutTouchingTarget|DeletedFile)' -count=1` | stale/partial change set 整体拒绝且不覆盖当前文件 | 发生部分恢复或覆盖外部修改 |
| AC-M316-005 | `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、Windows 交叉编译 | 全量测试、静态检查、构建和 Windows 目标编译通过 | 任一命令失败 |

## M3.17：安全 Git 回滚

M3.17 增加 `drift git revert [-w <workspace>] <commit> --yes`，只对已提交的普通 commit 创建 `git revert --no-edit` 反向提交。它与 Drift change set restore 独立，禁止 `git reset --hard`、`git clean`、强制 checkout、rebase、merge、远程 push 和自动 abort。

### 验收

| ID | 验证方法 | 通过阈值 | 失败判定 |
| --- | --- | --- | --- |
| AC-M317-001 | Git 预检测试 | 干净仓库解析唯一普通 commit 并生成预览 | 目标解析错误或未检查工作区 |
| AC-M317-002 | 回滚执行测试 | 生成新的反向 commit，文件内容恢复 | 使用 reset 或丢失未提交内容 |
| AC-M317-003 | dirty/invalid/merge 测试 | dirty、未跟踪、冲突、非法或 merge 目标执行前拒绝 | Git 工作树被改变 |
| AC-M317-004 | 权限/沙箱/失败测试 | 未确认、沙箱拒绝、取消、冲突和 hook 失败均不误报成功 | 失败状态丢失或自动清理用户现场 |
| AC-M317-005 | 审计测试 | 记录 commit、权限、沙箱、状态和失败原因，不含绝对路径、凭据和完整输出 | 审计泄露敏感信息 |
| AC-M317-006 | 全量回归 | `go test ./...`、`go vet ./...`、build、Windows 目标编译和 staged diff check 通过 | 任一命令失败 |

## M3.15-C：Windows AppContainer 正式执行

M3.15-C 在 M3.15-B 的原生探针之上，把 `Bash` 正式接入 AppContainer 进程生命周期；只有探针完整通过时，`required` 才能执行并记录 `sandboxed=true`。

### 当前范围

- Linux 继续使用已通过 M3.13/M3.14 runtime 验收的 `bwrap`。
- Windows 临时 workspace 探针验证 workspace 写入、`.drift/.git` 拒绝、网络隔离和进程树终止；正式 `Bash` 通过 AppContainer、ACL 和 Job Object 执行，任一验证失败时 `required` 拒绝、`auto` 继续。
- macOS 明确返回空能力，不调用 `sandbox-exec`；`auto` 记录未启用，`required` 拒绝执行。
- Windows AppContainer 使用临时 profile、显式文件 ACL 和 Job Object；不使用全局防火墙规则。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M315A-001 | Windows: `go test ./internal/tool -run TestWindowsSandboxBackendIsUnavailableBeforeAppContainerProbe -count=1` | Windows 后端为空且 `Reliable=false` | TDD RED/GREEN 日志 | `artifacts/verification/m3.15-a/tdd-red-green.txt` | 错误报告 Windows 沙箱可靠 |
| AC-M315A-002 | Windows/macOS: `GOOS=windows/darwin GOARCH=amd64 go test -c ./internal/tool` | 两个平台目标均可编译，macOS 测试断言无后端 | 交叉编译日志 | `artifacts/verification/m3.15-a/cross-compile.txt` | 任一目标编译失败 |
| AC-M315A-003 | `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.15-a/regression.txt` | 任一命令失败 |

| AC-M315C-001 | Windows: AppContainer lifecycle integration tests | Bash output, workspace write and `.drift` rejection pass; result records `sandboxed=true` | Windows native integration test | AppContainer bypass or protected path writable |
| AC-M315C-002 | Windows: AppContainer probe and timeout tests | Probe and timeout cleanup pass without child/marker residue | Windows native integration test | Probe failure or timeout leak |
| AC-M315C-003 | Windows: cross-compile, full test, vet and build | All commands exit successfully | Command log | Any command fails |

## M3.14：沙箱进程树取消与超时

M3.14 将已验证的 Linux `bwrap` 沙箱接入命令取消和超时验收，不增加新的沙箱后端。

### 当前范围

- `required` 模式下 Ctrl+C 取消命令时，`bwrap` 及其 shell 子进程一起终止。
- 命令超时后子进程不能继续运行或在 workspace 写入残留文件。
- 结果继续准确区分 `cancelled`、`timeout` 和 `failed`。
- Windows 沙箱、Git worktree 和 Node runtime 不在本阶段范围内。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M314-001 | Linux: `go test ./internal/tool -run 'TestRequiredBwrap(WritesOnlyWorkspace|CancellationStopsChildProcess|TimeoutStopsChildProcess)$' -count=1` | workspace 边界、取消和超时测试全部通过，子进程不产生残留文件 | Linux 集成测试日志 | 子进程存活或状态错误 |
| AC-M314-002 | Windows: `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 本地全量回归通过 | 命令日志 | 任一命令失败 |

## M3.13：Linux bwrap 运行时验收

M3.13 不扩大沙箱策略，只把已实现的 Linux `bwrap` 包装从“参数和能力探针”推进到可重复的运行时验收。

### 当前范围

- Linux 集成测试仅在 `DetectSandbox()` 报告可靠 `bwrap` 时执行；其他平台不伪造通过，明确跳过并保留跨平台编译检查。
- `required` 模式下命令可写入 workspace，workspace 外部路径不能落盘，`.drift` 和 `.git` 内容不能被修改。
- `--unshare-net` 运行时没有可用路由；`--die-with-parent`、workspace bind 和保护目录隔离继续由参数测试覆盖。
- 不新增远程服务器验证、不引入 Node runtime、不改变 Windows `auto/required` 的既有 fail-open/fail-closed 契约。
- 2026-10-02 已在临时 Linux 环境实际执行 AC-M313-001 并通过；测试二进制、临时目录和临时安装的 `bubblewrap` 均已清理。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M313-001 | Linux: `go test ./internal/tool -run TestRequiredBwrapWritesOnlyWorkspace -count=1` | bwrap 可用时 workspace 写入成功，外部、`.drift`、`.git` 不落盘，网络路由为空 | 集成测试日志 | 任一边界被绕过 |
| AC-M313-002 | Windows: `GOOS=linux GOARCH=amd64 go test -c ./internal/tool` | Linux-only 集成测试可交叉编译；Windows 不误报 runtime 通过 | 构建日志 | 编译失败或伪造通过 |
| AC-M313-003 | `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 全部退出码为 0 | 命令日志 | 任一命令失败 |

## M3.12：写入前文件状态校验

M3.12 为 WriteFile、EditFile、DeleteFile 增加预览到提交之间的文件状态校验，防止用户审批等待期间外部修改被覆盖或删除。

### 当前范围

- Preview 携带目标文件在预览时的存在状态和原始内容快照。
- 提交前重新读取并比较快照；目标新增、删除、替换或变为 symlink/非普通文件时拒绝提交。
- 拒绝时不创建 change set、不写入、不删除，保留外部修改后的内容。
- 当前采用内存字节比较，不做持久化缓存、跨进程锁或多 Agent 协调。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M312-001 | `go test ./internal/tool -run 'ExternalChangeAfterPreview' -count=1` | 写入、编辑、删除在外部修改后全部拒绝 | 测试日志 | 外部内容被覆盖/删除 |
| AC-M312-002 | 全量工具与 change set 测试 | 正常审批提交仍成功，失败不产生半个 change set | 测试日志 | 正常路径回归或审计污染 |
| AC-M312-003 | `go test ./...`、`go vet ./...`、build、diff check | 全部退出码为 0 | 命令日志 | 任一命令失败 |

## M3.11：Glob 与 Grep

M3.11 将两个只读发现工具升级为模式匹配和正则搜索，不扩大 workspace 边界或读取预算。

### 当前范围

- `Glob(pattern, path)` 支持 `*.go`、`**/*.go`、`internal/**/*.go`；只返回普通文件，跳过受保护/缓存目录，最多返回 200 条。
- `Grep(pattern, path, include)` 使用 Go 正则表达式；`include` 按文件名 glob 过滤，输出相对路径、行号和匹配行。
- 非法正则或非法 include glob 返回明确工具错误；0 次匹配返回空结果，不视为工具失败。
- 旧 `Glob` 的 `path` 列目录参数和旧 `Grep` 的 `query` 参数不兼容；旧名称/参数不自动改写。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M311-001 | `go test ./internal/tool -run 'TestGlob|TestGrep' -count=1` | 模式、正则、include、非法输入和限制测试通过 | 测试日志 | 任一边界错误 |
| AC-M311-002 | 临时 workspace 功能测试 | Glob/Grep 结果、行号、过滤和 0 匹配符合预期；越界/受保护目录拒绝 | 功能测试日志 | 访问越界或结果错误 |
| AC-M311-003 | 旧记录检查 | 历史 JSONL/change set 可读取；不再生成旧公开工具名 | 测试日志 | 旧记录被破坏或新记录混用旧名 |
| AC-M311-004 | `go test ./...`、`go vet ./...`、build、diff check | 全部退出码为 0 | 命令日志 | 任一命令失败 |

## M3.10：工具公开名称统一

M3.10 统一模型可见的内建工具名称，不改变工具行为、路径边界、审批和沙箱策略。

### 当前范围

- 只读工具：`Glob`、`Grep`、`ReadFile`。
- chat 变更工具：`WriteFile`、`EditFile`、`DeleteFile`、`Bash`。
- `-p` 仍只暴露 `Glob`、`Grep`、`ReadFile`；chat 才暴露全部七个工具。
- 旧名称 `list_files`、`search_text`、`read_file`、`write_file`、`edit_file`、`delete_file`、`run_command` 不再注册或兼容；旧会话中的旧工具调用必须按未知工具处理，不自动改写。
- 变更记录中的内部 operation（例如 `create_file`、`edit_file`、`run_command`）保持不变，避免破坏 change set 恢复语义。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M310-001 | `go test ./internal/tool -run 'TestPublicToolNames|TestDefinitionsUsePublicToolNames' -count=1` | 两个 registry 仅暴露新名称，旧名称 Lookup 全部失败 | 测试日志 | 旧名称仍可调用 |
| AC-M310-002 | `go test ./... -count=1` | Agent、权限、审计、Provider 和 TUI 均使用新名称 | 测试日志 | 任一回归失败 |
| AC-M310-003 | `go vet ./...`、`go build ./cmd/drift`、`git diff --check` | 全部退出码为 0 | 命令日志 | 任一命令失败 |

## M3.9：会话权限模式

M3.9 增加会话级权限模式，复用现有审批回调和工具安全校验，不改变 workspace 边界。

### 当前范围

- `default`：保持现有行为，写入、编辑、删除和命令默认询问。
- `acceptEdits`：自动允许写入和编辑；删除、命令仍需询问。
- `plan`：允许读取，拒绝所有写入、编辑、删除和命令。
- `bypassPermissions`：自动允许普通变更和命令，但只跳过审批，不绕过 workspace、`.drift`、路径/符号链接/特殊文件校验、危险命令硬拒绝或 `-p` 只读边界。
- 启动参数 `--permission-mode` 只作用于当前 chat；`/permissions mode` 可在当前 chat 查询或切换，不落盘、不跨会话继承。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 失败判定 |
| --- | --- | --- | --- | --- |
| AC-M39-001 | `go test ./internal/app -run TestPermissionMode -count=1` | 四种模式判定和非法值校验通过 | 测试日志 | 模式矩阵错误 |
| AC-M39-002 | chat 使用 `--permission-mode plan` | 读取可用，变更/命令不执行且无审批 | 人工记录、审计 | 发生副作用 |
| AC-M39-003 | chat 使用 `--permission-mode acceptEdits` | 写入/编辑免审批，删除/命令仍审批 | 人工记录、审计 | 权限扩大 |
| AC-M39-004 | chat 使用 `--permission-mode bypassPermissions` | 普通操作免审批；越界、`.drift`、危险命令仍拒绝 | 测试日志、人工记录 | 绕过硬边界 |
| AC-M39-005 | `go test ./...`、`go vet ./...`、build、diff check | 全部退出码为 0 | 命令日志 | 任一命令失败 |

### OS 沙箱契约（第一阶段）

- Drift 启动参数 `--sandbox` 接受 `off`、`auto`、`required`，默认 `off`；沙箱策略由控制层固定，不出现在模型工具参数中。
- `auto` 在没有已验证后端时继续执行，但结果必须记录 `sandboxed=false`；`required` 不得静默降级。
- 当前阶段只做能力检测和 fail-closed 契约，不把 Windows Job Object 当作文件/网络沙箱。
- Linux 已实现 `bwrap` 包装参数和能力探针；当前 Windows 只完成 Linux 目标编译检查，尚未完成 Linux runtime 验证。
- macOS 的 `sandbox-exec` 仍未实现可靠 profile。
- 权限模式 `bypassPermissions` 不得关闭或绕过 OS 沙箱策略。

## M3.8：命令与测试执行收敛

M3.8 在现有 `run_command` 基础上完成命令、测试、取消、权限和审计的自动化验收，不新增重复的测试工具。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M38-001 | `go test ./internal/tool -run Command -count=1` | 成功、非零退出、超时、取消和截断状态准确 | 测试日志 | `artifacts/verification/m3.8/command-tests.txt` | 状态或退出码错误 |
| AC-M38-002 | 命令路径/cwd/保护目录测试 | 越界、符号链接和受保护目录全部拒绝 | 测试日志 | `artifacts/verification/m3.8/command-tests.txt` | 安全边界绕过 |
| AC-M38-003 | `-p` 注册表测试 | 只读模式不暴露 `run_command` | 测试日志 | `artifacts/verification/m3.8/command-tests.txt` | 出现命令工具 |
| AC-M38-004 | 全量回归 | test、vet、build、diff check 全部通过 | 命令日志 | `artifacts/verification/m3.8/final-check.txt` | 任一命令失败 |

## M3.7：Change Set 恢复

M3.7 为现有文件变更记录增加安全恢复能力。它恢复 Drift change set，不执行 Git 回滚。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M37-001 | `go test ./internal/changes -run TestRestoreCreateAndEdit` | 创建文件被撤销，编辑内容恢复 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 任一文件状态错误 |
| AC-M37-002 | `go test ./internal/changes -run TestRestoreCreateAndEdit` | 编辑/覆盖回到变更前内容 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 旧内容缺失或错误 |
| AC-M37-003 | `go test ./internal/changes -run TestRestoreDeletedFile` | 删除前内容恢复 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | `.before` 不存在或恢复错误 |
| AC-M37-004 | `go test ./internal/changes -run TestRestoreRefusesStaleTargetWithoutChanges` | 外部修改时拒绝且不改动目标 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 发生部分恢复 |
| AC-M37-005 | `go test ./... -count=1` | 工具、入口和安全校验全部通过 | 测试日志 | `artifacts/verification/m3.7/tdd-tests.txt` | 任一测试失败 |
| AC-M37-006 | vet、build、diff check | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.7/final-check.txt` | 任一命令失败 |

## M3.6：工作区权限策略持久化

M3.6 在 M3.3–M3.5 的审批基础上，持久化用户明确选择“允许此类操作”的精确授权，同时保持默认 `ask` 和现有工具安全校验。

### 当前范围

- 授权只写入当前 workspace 的 `.drift/permissions.json`，不跨 workspace 或用户目录共享。
- 只持久化第二项 `Yes, and don't ask again for this pattern`；第一项只对当前操作有效，`No` 和取消不写入。
- 匹配必须同时满足工具、操作、相对路径，或命令与 cwd；不支持 glob、前缀、正则和 `allow all`。
- 策略文件使用版本 `1`、目录 `0700`、文件 `0600`，通过同目录临时文件和原子替换保存。
- 缺失、损坏、未知版本、重复规则或保存失败均安全降级为 `ask`，不得自动放行。
- `/permissions` 显示脱敏摘要，`/permissions clear` 清除当前 workspace 的持久化授权。

### 非目标

- 不提供用户级全局授权、跨 workspace 授权、命令白名单、通配符策略、权限继承或永久 `allow all`。
- 不改变现有路径、符号链接、隐藏目录、特殊文件和命令 cwd 安全校验。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M36-001 | 权限策略单元测试与新 workspace chat | 无策略时写入/编辑/删除/命令默认每次 ask | 测试日志、人工记录 | `artifacts/verification/m3.6/permission-policy-tests.txt` | 无策略自动放行 |
| AC-M36-002 | 第一项与第二项审批测试 | 第一项不跨进程保留；第二项成功后可跨进程精确命中 | 测试日志、人工记录 | `artifacts/verification/m3.6/manual-acceptance.md` | 授权范围扩大或失败操作落盘 |
| AC-M36-003 | 精确匹配、损坏配置和清除测试 | 任一字段改变、配置损坏或 clear 后重新 ask | 测试日志 | `artifacts/verification/m3.6/permission-policy-tests.txt` | 错误命中或安全降级失败 |
| AC-M36-004 | 全量回归 | `go test ./...`、`go vet ./...`、build、diff 检查全部退出码为 0 | 命令日志 | `artifacts/verification/m3.6/final-check.txt` | 任一命令失败 |

## M3.5：命令取消与进程树终止

M3.5 在 M3.4 的 `run_command` 基础上，确保取消或超时时命令进程树收敛，当前 chat 轮次安全结束并可继续输入。

### 当前范围

- Ctrl+C 在活动轮次中只取消当前 Agent 轮和命令；空闲时仍按既有语义退出 chat。
- Windows 使用 Job Object，并以 `taskkill /T /F` 作为进程树清理回退；Unix 使用独立 process group 和信号升级。
- 结果明确区分 `cancelled`、`timeout`、`failed`，任何非成功状态都不能报告为成功。
- 取消后的 TUI 显示安全提示，不泄露原始 `context canceled`；下一轮可以继续输入。
- 审计、session 和 changes 不保存完整命令输出或回答正文。

### 非目标

- 不引入永久权限配置、命令白名单、跨 chat 授权、后台任务或并发命令。
- 不改变 M3.4 的 `ask / allow / deny` 及精确命令匹配语义。
- 不承诺 Windows 与 Unix 的 shell 语义完全一致。

### 验收

| ID | 验证方法 | 通过阈值 | 证据类型 | 证据路径 | 失败判定 |
| --- | --- | --- | --- | --- | --- |
| AC-M35-001 | `go test ./internal/tool -run Command -count=1` | 父进程和派生子进程均退出；状态为 `cancelled` | 测试日志 | `artifacts/verification/m3.5/command-cancel-tests.txt` | 进程残留或状态误报 |
| AC-M35-002 | chat + Ctrl+C 人工验收及 TUI 等价测试 | 当前轮取消后 chat 不退出，下一条消息可继续；空闲 Ctrl+C 仍退出 | 测试日志 | `artifacts/verification/m3.5/audit-check.txt` | chat 退出或无法继续 |
| AC-M35-003 | Agent/TUI/审计专项测试 | 安全取消提示、当前轮回滚、审计不含正文 | 测试日志 | `artifacts/verification/m3.5/audit-check.txt` | 泄露原始错误或半轮状态 |
| AC-M35-004 | `go test ./... -count=1`、`go vet ./...`、build、diff 检查 | 全部退出码为 0 | 命令日志 | `artifacts/verification/m3.5/final-check.txt` | 任一命令退出码非 0 |
