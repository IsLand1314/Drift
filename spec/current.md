# 当前交付范围

本文件是当前版本范围与验收标准的唯一事实来源；架构演进建议见 `doc/architecture.md`。

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
