# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构演进建议见 `doc/architecture.md`。当前版本为 M1.2。下面的 M1.1、M1.0、M0.9、M0.8、M0.7、M0.6、M0.5、M0.4、M0.3、M0.2.2 和 M0.2.1 章节是已完成阶段的历史记录，不覆盖当前 M1.2 的运行边界。

## M1.2：本地对话持久化

M1.2 让 `drift chat` 默认保存本地完整上下文，并通过显式 `--resume` 恢复；`--no-session` 用于不保存完整正文的临时对话。完整会话与 M0.9 脱敏审计分离，审计 JSONL 永远不是恢复来源。

### 当前范围

- 完整快照保存到 workspace 内 `.drift/conversations/<id>.json`；创建目录/文件使用 `0700`/`0600` 目标权限，并通过临时文件替换保存。
- `chat --resume` 恢复当前 workspace 最近快照，`chat --resume <id>` 恢复指定快照；文件型 `-w`、跨 workspace 和 `--no-session` 组合均拒绝。
- 默认 chat 启动显示会话 ID和完整上下文提示；每轮成功后保存 Runner 消息，Provider 或工具失败的半轮不保存。
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
