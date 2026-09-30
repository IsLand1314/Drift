# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构演进建议见 `doc/architecture.md`。当前版本为 M0.4。下面的 M0.3、M0.2.2 和 M0.2.1 章节是已完成阶段的历史记录，不覆盖当前 M0.4 的运行边界。

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
