# M0.4 Multi-turn Read Agent Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 将 M0.3 的固定两轮只读 Agent 扩展为最多四次模型请求、最多六次工具调用的多轮代码探索 Agent，并增加受限的 list_files 与 search_text 工具。

**Architecture:** 保留 llm.Client、tool.Tool、tool.Registry、Runtime Event 和 JSONL Writer。工具通过现有 Registry 注入；Agent 每轮聚合 Provider 返回的完整 tool calls，串行执行并追加上下文，最终无工具回答或触达预算后结束。文件遍历统一在 workspace 根目录内完成，不跟随符号链接，不读取 dotenv。

**Tech Stack:** Go 1.26+、标准库 encoding/json、os.Root、io/fs、bufio、testing、httptest；不新增第三方依赖。

**Spec:** doc/m0.4-multiturn-read-agent.md

## Global Constraints

- 保持 Go 1.26+ 和标准库实现，不新增依赖。
- 默认工具只有 list_files、search_text、read_file，不提供写入、删除、编辑、shell 或 exec。
- 单次运行最多 4 次模型请求、最多 6 次工具调用。
- read_file 单文件最大 128 KiB；所有成功工具结果进入模型上下文的总量最大 512 KiB。
- workspace 内只接受相对路径；拒绝 ..、符号链接、目录（list_files 的目标目录除外）、特殊文件、.env 和 .env.*。
- 工具调用按模型返回顺序串行执行，不并发。
- JSONL 事件仍逐行即时 Flush；text_delta 仍保存 <redacted>，stdout 只输出最终回答。
- 所有提交使用中文提交信息，提交作者为 island。

---

### Task 1: 建立受限 workspace 遍历基础

**Files:**
- Create: internal/tool/walk.go
- Test: internal/tool/walk_test.go
- Modify: internal/tool/read.go
- Test: internal/tool/read_test.go

**Interfaces:**
- Consumes: 现有 Read 的 workspace、路径和符号链接约束。
- Produces: validateRelativePath(path string, allowEmpty bool) error、openWorkspace(root string) (*os.Root, error)、walkRegularFiles(workspace *os.Root, relative string, fn func(path string, entry fs.DirEntry) error) error，供 list_files 和 search_text 使用。

- [ ] **Step 1: 写失败测试**

在 walk_test.go 中建立包含普通文件、子目录、.env、符号链接和特殊路径的临时 workspace，测试路径校验和遍历行为：

~~~go
func TestWalkRegularFilesRejectsUnsafeDirectory(t *testing.T) {
    for _, path := range []string{"../outside", "/tmp", ".env"} {
        if err := validateRelativePath(path, true); err == nil {
            t.Fatalf("validateRelativePath(%q) = nil, want error", path)
        }
    }
}
~~~

在现有 read_test.go 增加回归测试，确认 Read 继续拒绝路径遍历和符号链接；测试必须保持在 Windows 上可运行，符号链接权限不足时沿用现有跳过规则。

- [ ] **Step 2: 运行失败测试**

~~~powershell
go test ./internal/tool -run "TestWalkRegularFilesRejectsUnsafeDirectory|TestRead" -count=1
~~~

预期：失败，因为共享遍历函数尚未存在。

- [ ] **Step 3: 实现最小共享基础**

在 walk.go 中实现：

~~~go
func validateRelativePath(path string, allowEmpty bool) error
func openWorkspace(root string) (*os.Root, error)
func walkRegularFiles(workspace *os.Root, relative string, fn func(string, fs.DirEntry) error) error
~~~

实现要求：

- 将反斜杠规范化为 / 后拒绝绝对路径和任意 .. 段；空路径只在 allowEmpty 为 true 时转换为 .；
- 以 os.OpenRoot 打开 workspace；
- 使用 fs.WalkDir 遍历相对目录；请求的起始路径必须存在且是目录；遇到符号链接目录或符号链接文件直接跳过；
- 只把普通文件回调给 fn；目录回调只用于继续遍历；
- 任何 .env 或 .env.* 文件都跳过；
- 让 Read 复用相对路径校验，但保持其最终文件检查和 128 KiB 限制，不为了复用而放宽现有边界。

- [ ] **Step 4: 运行测试确认通过**

~~~powershell
go test ./internal/tool -run "TestWalkRegularFilesRejectsUnsafeDirectory|TestRead" -count=1
~~~

预期：PASS。

- [ ] **Step 5: 提交**

~~~powershell
git add internal/tool/walk.go internal/tool/walk_test.go internal/tool/read.go internal/tool/read_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "重构：统一只读工作区遍历边界"
~~~

### Task 2: 增加 list_files 工具

**Files:**
- Create: internal/tool/list.go
- Test: internal/tool/list_test.go

**Interfaces:**
- Consumes: walkRegularFiles、Tool、llm.ToolDefinition。
- Produces: List(root, rawArguments string) (string, error)、ListDefinition() llm.ToolDefinition、默认注册可用的 list_files 工具。

- [ ] **Step 1: 写失败测试**

覆盖以下行为：

- 空路径列出 workspace 下普通文件；
- 指定相对目录只列出该目录下的文件；
- 结果按相对路径排序；
- .env、符号链接和特殊文件不出现；
- ..、绝对路径和文件作为目录参数返回错误；
- 超过 MaxListEntries = 200 时返回前 200 项并带明确截断标记；
- schema 名称为 list_files，路径参数可选且禁止未知字段。

测试核心断言：

~~~go
got, err := List(root, "{}")
if err != nil || got != "README.md\ngo.mod\n" {
    t.Fatalf("List() = %q, %v", got, err)
}
~~~

- [ ] **Step 2: 运行失败测试**

~~~powershell
go test ./internal/tool -run TestList -count=1
~~~

预期：失败，因为 List 尚未定义。

- [ ] **Step 3: 实现最小工具**

定义：

~~~go
const MaxListEntries = 200

type listArguments struct {
    Path string
}

func List(root, rawArguments string) (string, error)
func ListDefinition() llm.ToolDefinition
~~~

Path 字段使用 JSON tag path。使用严格 JSON 解码（DisallowUnknownFields），空参数路径代表 .。遍历结果使用 / 作为输出分隔符，排序后每行一个相对路径；超过 200 条时保留前 200 条并附加 list_files: results truncated at 200 entries。目录本身不作为结果返回。

实现 listFilesTool，其 Name 返回 list_files、Definition 返回 ListDefinition、Execute 委托给 List。

- [ ] **Step 4: 运行测试确认通过**

~~~powershell
go test ./internal/tool -run TestList -count=1
~~~

预期：PASS。

- [ ] **Step 5: 提交**

~~~powershell
git add internal/tool/list.go internal/tool/list_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：增加受限文件列表工具"
~~~

### Task 3: 增加 search_text 工具

**Files:**
- Create: internal/tool/search.go
- Test: internal/tool/search_test.go

**Interfaces:**
- Consumes: walkRegularFiles、Tool、llm.ToolDefinition。
- Produces: Search(root, rawArguments string) (string, error)、SearchDefinition() llm.ToolDefinition、默认注册可用的 search_text 工具。

- [ ] **Step 1: 写失败测试**

覆盖以下行为：

- 在 workspace 普通文本文件中按字面量搜索 query；
- path 参数限制搜索范围；
- 输出包含相对路径、行号和截断到 240 字节的匹配行；
- .env、符号链接、二进制文件和特殊文件被跳过；
- 空 query、未知字段、.. 和绝对路径返回错误；
- 最多扫描 200 个文件、返回 100 个匹配，输出最大 32 KiB，超限时带明确截断标记；
- schema 名称为 search_text，必需参数为 query。

测试示例：

~~~go
got, err := Search(root, "{\"query\":\"RunEvents\",\"path\":\"internal\"}")
if err != nil || !strings.Contains(got, "internal/agent/agent.go:42:") {
    t.Fatalf("Search() = %q, %v", got, err)
}
~~~

- [ ] **Step 2: 运行失败测试**

~~~powershell
go test ./internal/tool -run TestSearch -count=1
~~~

预期：失败，因为 Search 尚未定义。

- [ ] **Step 3: 实现最小工具**

定义：

~~~go
const (
    MaxSearchFiles   = 200
    MaxSearchMatches = 100
    MaxSearchBytes   = 32 << 10
)

type searchArguments struct {
    Query string
    Path  string
}

func Search(root, rawArguments string) (string, error)
func SearchDefinition() llm.ToolDefinition
~~~

Query 和 Path 使用对应 JSON tags。逐个普通文件打开并使用 bufio.Scanner 按行读取；单行缓冲上限为 64 KiB。先读取有限前缀检测 NUL 字节，检测为二进制时跳过。匹配使用 strings.Contains，不引入正则表达式；结果超过任一上限立即停止并追加截断标记。输出只使用相对路径，不拼接 workspace 绝对路径。

实现 searchTextTool，Execute 委托给 Search。

- [ ] **Step 4: 运行测试确认通过**

~~~powershell
go test ./internal/tool -run TestSearch -count=1
~~~

预期：PASS。

- [ ] **Step 5: 提交**

~~~powershell
git add internal/tool/search.go internal/tool/search_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：增加受限文本搜索工具"
~~~

### Task 4: 扩展默认 Registry

**Files:**
- Modify: internal/tool/registry.go
- Test: internal/tool/read_test.go

**Interfaces:**
- Consumes: listFilesTool、searchTextTool、readFileTool。
- Produces: NewDefaultRegistry() Registry 按稳定顺序导出 list_files、search_text、read_file。

- [ ] **Step 1: 写失败测试**

把默认 Registry 测试从“只有一个 read_file”改为验证三个定义、三个名称顺序和三个 Lookup 成功；重复名称测试保持不变。

- [ ] **Step 2: 运行失败测试**

~~~powershell
go test ./internal/tool -run "TestDefaultRegistry|TestNewRegistry" -count=1
~~~

预期：失败，因为默认 Registry 仍只有 read_file。

- [ ] **Step 3: 实现 Registry 组装**

只修改 NewDefaultRegistry：

~~~go
return NewRegistry(listFilesTool{}, searchTextTool{}, readFileTool{})
~~~

保留 Registry 的名称校验、稳定顺序和 schema 拷贝逻辑。

- [ ] **Step 4: 运行测试确认通过**

~~~powershell
go test ./internal/tool -run "TestDefaultRegistry|TestNewRegistry" -count=1
~~~

预期：PASS。

- [ ] **Step 5: 提交**

~~~powershell
git add internal/tool/registry.go internal/tool/read_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：注册多轮只读探索工具"
~~~

### Task 5: 将 Agent Loop 改为受限多轮

**Files:**
- Modify: internal/agent/agent.go
- Test: internal/agent/agent_test.go

**Interfaces:**
- Consumes: tool.Registry.Definitions、tool.Registry.Lookup、所有默认工具。
- Produces: 现有 Run、RunEvents、RunEventsWithRegistry 签名保持不变；新增常量 MaxModelRequests = 4，将工具调用上限调整为 MaxToolCalls = 6。

- [ ] **Step 1: 写失败测试**

在 agent_test.go 增加：

- 三轮脚本：第一轮 list_files，第二轮 search_text，第三轮 read_file，第四轮最终文本；断言调用顺序、请求数、上下文消息和最终 stdout 只包含答案；
- 第二轮仍携带三个工具 schema，最终回答轮仍携带三个 schema；
- 第 4 次模型请求返回工具调用时不再发起第 5 次请求，返回受控上限错误；
- 第 6 次工具调用之后，若仍有请求预算，追加受控限制结果并发起一次无工具 schema 的最终请求；
- 直接回答仍只请求一次；首轮 DSML 守卫仍只对无原生 tool call 的首轮 stop 文本生效；
- 工具错误继续以受控 tool result 进入下一轮；未知工具和取消仍不执行越界动作；
- Runtime Event 顺序覆盖多轮 tool_call/tool_result，并且最终 run_finished 只出现一次。

测试脚本继续复用现有 scriptedClient，不要新建第二套 fake Provider。

- [ ] **Step 2: 运行失败测试**

~~~powershell
go test ./internal/agent -run "TestRunMultiTurn|TestRun.*Budget|TestRunDirect|TestRunEvents" -count=1
~~~

预期：新增多轮和预算测试失败；现有两轮测试中“第二轮无工具 schema”的断言也会失败，说明测试需要按 M0.4 契约更新。

- [ ] **Step 3: 实现最小多轮循环**

将 RunEventsWithRegistry 的固定两次 client.Stream 改为最多 4 次的循环：

~~~go
const (
    MaxModelRequests = 4
    MaxToolCalls     = 6
)
~~~

实现规则：

- 保留 messages 为不含 system 的会话消息；第一次请求临时在请求副本前插入只读 system 指令；后续请求复用 user/assistant/tool 消息；
- 每次普通探索请求携带 registry.Definitions；只有预算耗尽后的最后说明请求不携带 Tools；
- 每轮把 Provider 返回的 assistant message 追加到 messages；完整 tool call 先验证名称，再按顺序执行；
- 用累计 toolCalls 计数限制 6 次；超出部分不执行，追加受控 tool result；
- 用累计成功工具结果字节数限制 512 KiB；即将超限的结果替换为受控限制结果，不把超限内容加入上下文；
- 每次工具调用发出 EventToolCall 和 EventToolResult；工具错误继续使用当前统一脱敏错误文本；
- 首轮无工具且 stop 时执行现有 DSML 兼容性守卫并输出首轮缓存文本；后续无工具且 stop 时输出该轮文本；
- 触达工具或上下文预算时，如果还有请求预算，追加受控限制结果并执行一次无 Tools 的最终请求；没有请求预算则返回 agent: request/tool budget exceeded；
- Provider 错误、sink 错误、取消和非 stop 无工具完成保持现有错误路径；
- 成功结束只发出一次 EventRunFinished。

保留 Run 兼容包装器，不让 CLI API 变化。

- [ ] **Step 4: 运行 Agent 测试确认通过**

~~~powershell
go test ./internal/agent -count=1
~~~

预期：PASS，且旧的直接回答、工具失败、reasoning content、取消和事件顺序测试继续通过。

- [ ] **Step 5: 提交**

~~~powershell
git add internal/agent/agent.go internal/agent/agent_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "功能：支持受限多轮 Agent Loop"
~~~

### Task 6: 更新 App 集成测试与 JSONL 审计验收

**Files:**
- Modify: internal/app/app_test.go
- Modify: internal/session/jsonl_test.go

**Interfaces:**
- Consumes: app.Run 现有配置、stdout/stderr、Session Writer；多轮 Agent 不改变 App 的组装方式。
- Produces: 本地 httptest SSE 服务能验证多轮请求、工具 schema、上下文消息和审计事件。

- [ ] **Step 1: 写失败测试**

新增 TestRunMultiTurnReadExploration：模拟四个 SSE 响应：

1. list_files；
2. search_text；
3. read_file；
4. 最终文本。

每次请求检查模型名、消息顺序、工具 schema 数量和最后一轮文本；断言 stdout 只为最终回答，.drift/sessions 中逐行存在开始、三组工具调用/结果、文本和结束事件。

把现有 app 测试中“第二轮 len(request.Tools) == 0”改为：探索轮包含三个 schema；只有预算触发的最终说明轮不包含 schema。

- [ ] **Step 2: 运行失败测试**

~~~powershell
go test ./internal/app -run "TestRunMultiTurn|TestRun.*RoundTrip|TestRunWritesSessionAudit" -count=1
~~~

预期：新增多轮测试失败，旧断言因 M0.4 schema 规则需要同步。

- [ ] **Step 3: 实现测试和必要的 App 调整**

只在测试中增加本地 SSE 分支；如果 app.Run 需要暴露工具注册或 Session 路径信息，优先复用当前临时工作目录和 .drift/sessions 扫描，不新增生产配置项。确保每个 SSE 响应都设置 Content-Type: text/event-stream 并以 [DONE] 结束。

- [ ] **Step 4: 运行 App 和 Session 测试**

~~~powershell
go test ./internal/app ./internal/session -count=1
~~~

预期：PASS。

- [ ] **Step 5: 提交**

~~~powershell
git add internal/app/app_test.go internal/session/jsonl_test.go
git -c user.name=island -c user.email=island0920@163.com commit -m "测试：覆盖多轮探索与会话审计"
~~~

### Task 7: 同步规格、文档与最终验证

**Files:**
- Modify: spec/current.md
- Modify: README.md
- Modify: doc/m0.4-multiturn-read-agent.md

**Interfaces:**
- Consumes: 已完成的三项工具、Agent Loop、App 集成行为。
- Produces: 文档描述与代码、测试一致；M0.4 成为当前交付范围。

- [ ] **Step 1: 更新当前规格**

在 spec/current.md 顶部增加 M0.4 当前范围和验收：多轮请求、三个只读工具、4/6/512 KiB 上限、审计和退出行为；明确 M0.2.1 的“最多两次请求、唯一 read_file schema”属于历史阶段，不再作为当前 M0.4 行为。

- [ ] **Step 2: 更新 README 和阶段文档**

更新 README.md 的当前版本说明、工具列表、调用示例和 M0.4 文档链接；在 doc/m0.4-multiturn-read-agent.md 补充实际常量值、真实事件样例和已完成的验收结果。保留 M0.2/M0.3 历史文档，不覆盖旧阶段记录。

- [ ] **Step 3: 运行完整验证**

~~~powershell
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
~~~

预期：全部通过；如果 Windows 临时目录出现 app.test.exe: Access is denied，使用 go test -c ./internal/app -o .codex-temp-app-bin 后显式运行测试二进制，并记录环境限制，不把环境失败误报为代码通过。

- [ ] **Step 4: 检查敏感信息和工作树**

确认测试生成的 .drift/、.codex-temp/ 未被跟踪，搜索新增文档和测试中没有真实 API Key、Authorization header 或绝对 workspace 路径。

- [ ] **Step 5: 提交最终文档与验证结果**

~~~powershell
git add spec/current.md README.md doc/m0.4-multiturn-read-agent.md
git -c user.name=island -c user.email=island0920@163.com commit -m "文档：同步 M0.4 多轮只读探索范围"
~~~
