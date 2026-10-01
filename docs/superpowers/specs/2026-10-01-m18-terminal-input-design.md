# M1.8：最小终端输入层设计

## 目标

在不引入全屏界面、不改变只读 Agent 能力的前提下，让 `drift chat` 的真实终端交互具备清晰的输入边界：空输入框有占位提示、输入时有由程序控制的光标、取消当前轮有明确结果提示。

这解决的是 `bufio.Scanner` 把输入绘制完全交给宿主终端的问题；它无法显示占位符、重绘编辑行或可靠控制光标。

## 方案选择

采用最小 Bubble Tea 输入层：仅接管 TTY 的单行输入区，使用其成熟的 Windows 终端键盘、光标和重绘能力。非 TTY（重定向、测试注入）仍保持现有纯文本 `> ` 提示和按行输入路径。

不采用手写原始终端模式：虽然依赖更少，但需要自行处理 Windows 控制台、光标、退格和重绘，总代码与兼容风险更高。

不采用完整 FoxCode TUI：本阶段没有多行编辑、历史列表、滚动 viewport、Alt Screen、输入队列或后台任务需求。

## 范围

### TTY 输入框

- 输入区上、下各有一条暗色分隔线。
- 空输入显示暗灰色 `Send a message...` 占位符。
- 输入后显示青色 `❯`、用户文本和受控光标。
- Enter 提交一条非空单行消息；空输入继续等待。
- `exit`、`/exit`、`quit` 的现有退出语义不变。
- 非 TTY 不输出 ANSI 控制字符，不显示占位符，不改变脚本/测试输入协议。

### Ctrl+C

- 模型流或工具执行中：取消当前轮，保留 chat；输出一条带 `✖` 的红/紫色取消提示，不暴露 Provider 的 `context canceled` 原始文本。
- 空闲输入时：维持 M1.7 行为，整个 chat 以退出码 130 结束。
- 取消轮继续回滚 Agent 消息和 Token 用量，完整会话快照不保存半轮内容。

### `/status`

- 保持当前 Drift 的语义颜色：暗灰字段名、默认值、青色 workspace、黄色 `unavailable`。
- 保持既有两个空格缩进、固定值列和分隔线；不改成 FoxCode 的全灰输出。
- 不增加 `Memory`：当前没有独立长期记忆系统。继续显示 `Session ID`、`Model`、`Context`、`Tokens`、`Tools`、`Workspace`。

## 调用流程

```text
chat 启动
  -> stdout 为 TTY？
     -> 是：终端输入组件绘制输入框并返回一条已编辑消息
     -> 否：复用 Scanner/测试输入的纯文本路径
  -> chat 命令分派（/status、/clear、/compact 或普通提问）
  -> 普通提问创建 M1.7 的 turn Context
  -> Ctrl+C：活动轮取消并显示安全的取消反馈；空闲则退出 130
  -> 回到输入组件等待下一条消息
```

输入组件不拥有 Provider、Agent、会话快照或工具；它只把用户确认提交的文本交给现有 chat 循环。

## 错误与兼容

- 无法初始化 TTY 输入组件时回退到现有纯文本输入，而不是阻止 chat 启动。
- Provider、工具、持久化失败继续沿用当前受控错误行为。
- `--no-session`、`--resume`、`/clear`、`/compact`、`/status`、`--trace` 的语义不变。
- 不把用户输入、模型回答或光标控制序列写进脱敏 JSONL 审计。

## 验收

1. 在 Windows Terminal 运行 `go run ./cmd/drift chat -w .`：空输入区显示分隔线、青色提示和灰色 `Send a message...`。
2. 输入文字时，文字位于分隔线之间，光标由输入组件显示；Enter 后只发送一次消息。
3. 流式回答期间按 Ctrl+C：显示 `✖ 当前轮已取消；会话仍可继续`，出现新输入框；`/status` 不受取消轮污染。
4. 空闲输入时按 Ctrl+C：退出码为 130。
5. `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/drift` 和 `git diff --check` 均通过。

## 非目标

不实现 Alt Screen、鼠标、上下历史、多行输入、输入队列、后台任务、全屏日志 viewport、主题配置或 FoxCode 全量 TUI。
