package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/config"
	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/llm/openai"
	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

type modelClient struct {
	llm.Client
	model string
}

// Stream 给底层 Provider 请求补上命令行解析后的模型名。
// Agent 不需要知道具体 Provider，只依赖 llm.Client 接口。
func (c modelClient) Stream(ctx context.Context, request llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	request.Model = c.model
	return c.Client.Stream(ctx, request, emit)
}

// Run 是 CLI 的应用编排层：加载配置 → 校验参数 → 创建 Provider → 启动 Agent。
// getenv、out 和 stderr 都通过参数注入，方便测试时使用模拟环境和 HTTP 服务。
func Run(ctx context.Context, args []string, getenv func(string) string, out, stderr io.Writer) int {
	return RunWithSignals(ctx, args, getenv, os.Stdin, out, stderr, nil)
}

// RunWithInput 与 Run 相同，但允许交互式命令注入 stdin，便于测试和嵌入。
func RunWithInput(ctx context.Context, args []string, getenv func(string) string, in io.Reader, out, stderr io.Writer) int {
	return RunWithSignals(ctx, args, getenv, in, out, stderr, nil)
}

// RunWithSignals 允许命令入口注入 Ctrl+C 信号，使 chat 能区分取消当前轮和退出进程。
func RunWithSignals(ctx context.Context, args []string, getenv func(string) string, in io.Reader, out, stderr io.Writer, interrupts <-chan os.Signal) int {
	runCtx := ctx
	var coordinator *interruptCoordinator
	if interrupts != nil {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithCancel(ctx)
		coordinator = newInterruptCoordinator(interrupts, cancel)
		defer coordinator.start()()
	}
	if len(args) > 0 && args[0] == "session" {
		return runSessionCommand(args[1:], out, stderr)
	}
	if len(args) > 0 && args[0] == "conversation" {
		return runConversationCommand(args[1:], out, stderr)
	}
	chat := len(args) > 0 && args[0] == "chat"
	var persistenceOptions chatPersistenceOptions
	if chat {
		args = args[1:]
		var parseErr error
		args, persistenceOptions, parseErr = parseChatPersistenceArgs(args)
		if parseErr != nil {
			fmt.Fprintln(stderr, "错误：", parseErr)
			return 2
		}
	}
	flags := flag.NewFlagSet("drift", flag.ContinueOnError)
	flags.SetOutput(stderr)
	// .env 是可选的本地配置；LoadDotEnv 找不到文件时返回空配置，不影响启动。
	dotenv, err := config.LoadDotEnv(".env")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// lookup 实现“非空进程环境变量覆盖 .env”的优先级。
	lookup := func(key string) string { return config.MergeLookup(dotenv, getenv, key) }
	base := lookup("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	prompt := flags.String("p", "", "发送一次提示词并流式输出回复")
	workspaceTarget := flags.String("w", "", "要分析的目录或文件（默认当前目录）")
	trace := flags.Bool("trace", false, "将运行过程摘要输出到 stderr")
	model := flags.String("model", lookup("OPENAI_MODEL"), "模型名称（默认 OPENAI_MODEL）")
	baseURL := flags.String("base-url", base, "API 根地址，包含 /v1，不含 /chat/completions")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (!chat && strings.TrimSpace(*prompt) == "") || (chat && strings.TrimSpace(*prompt) != "") {
		if chat {
			fmt.Fprintln(stderr, "用法：drift chat [-w 路径] [--trace] [-model 模型名] [-base-url API根地址]")
		} else {
			fmt.Fprintln(stderr, "用法：drift -p \"你好\" [-w 路径] [--trace] [-model 模型名] [-base-url API根地址]")
		}
		return 2
	}
	launchDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取启动目录")
		return 1
	}
	selection, err := resolveWorkspace(launchDir, *workspaceTarget)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 目标无效")
		return 2
	}
	if chat && persistenceOptions.resume && selection.Focus != "" {
		fmt.Fprintln(stderr, "错误：--resume 不能与文件型 -w 同时使用")
		return 2
	}
	key := strings.TrimSpace(lookup("OPENAI_API_KEY"))
	if key == "" || strings.TrimSpace(*model) == "" {
		fmt.Fprintln(stderr, "请设置 OPENAI_API_KEY，并通过 OPENAI_MODEL 或 -model 指定模型")
		return 2
	}
	// Provider 只负责 HTTP/SSE；workspace 读取和工具循环由 Agent 层负责。
	client, err := openai.New(*baseURL, key)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	sessionPath := filepath.Join(selection.Root, ".drift", "sessions", fmt.Sprintf("run-%d.jsonl", time.Now().UTC().UnixNano()))
	sessionWriter, err := session.NewJSONLWriterWithSecrets(sessionPath, selection.Root, key)
	if err != nil {
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	var traceSink agent.EventSink
	if *trace {
		traceSink = newTraceSink(stderr)
	}
	var persistence *chatPersistence
	var runner *agent.Runner
	registry := tool.NewDefaultRegistry()
	if chat {
		store := conversation.NewStore(selection.Root)
		if persistenceOptions.resume {
			var snapshot conversation.Snapshot
			if persistenceOptions.resumeID == "" {
				snapshot, err = store.Latest()
			} else {
				snapshot, err = store.Load(persistenceOptions.resumeID)
			}
			if err != nil {
				fmt.Fprintln(stderr, "错误：无法恢复完整会话：", err)
				return 2
			}
			if snapshot.Focus != "" {
				if _, statErr := os.Stat(filepath.Join(selection.Root, filepath.FromSlash(snapshot.Focus))); statErr != nil {
					fmt.Fprintln(stderr, "错误：恢复会话的 focus 文件不存在")
					return 2
				}
			}
			runner = agent.NewRunnerWithMessages(modelClient{Client: client, model: *model}, selection.Root, snapshot.Focus, registry, snapshot.Messages)
			persistence = &chatPersistence{store: store, snapshot: snapshot, persistent: true}
			persistence.usage = usageTotals{InputTokens: snapshot.InputTokens, OutputTokens: snapshot.OutputTokens, ReportedRequests: snapshot.ReportedRequests, UnreportedRequests: snapshot.UnreportedRequests}
			fmt.Fprintf(stderr, "Session ID: %s\n注意：此会话会保存完整本地上下文，可能包含用户输入和读取结果；使用 --no-session 可关闭\n", snapshot.ID)
		} else if !persistenceOptions.noSession {
			snapshot, createErr := store.Create(selection.Focus)
			if createErr != nil {
				fmt.Fprintln(stderr, "错误：无法创建完整会话：", createErr)
				return 1
			}
			runner = agent.NewRunner(modelClient{Client: client, model: *model}, selection.Root, selection.Focus, registry)
			persistence = &chatPersistence{store: store, snapshot: snapshot, persistent: true}
			fmt.Fprintf(stderr, "Session ID: %s\n注意：此会话会保存完整本地上下文，可能包含用户输入和读取结果；使用 --no-session 可关闭\n", snapshot.ID)
		} else {
			runner = agent.NewRunner(modelClient{Client: client, model: *model}, selection.Root, selection.Focus, registry)
			persistence = &chatPersistence{persistent: false}
			fmt.Fprintln(stderr, "已禁用完整会话保存（--no-session）；仍保留脱敏审计")
		}
	} else {
		runner = agent.NewRunner(modelClient{Client: client, model: *model}, selection.Root, selection.Focus, registry)
	}
	if chat {
		status := chatStatus{Model: *model, Workspace: selection.Root, ToolCount: len(registry.Definitions())}
		code := runChatLoopWithPersistence(runCtx, runner, sessionWriter, traceSink, persistence, status, coordinator, in, out, stderr)
		if closeErr := sessionWriter.Close(); closeErr != nil && code == 0 {
			fmt.Fprintln(stderr, "错误：", closeErr)
			return 1
		}
		return code
	}
	var lastText string
	// Event Sink 先追加脱敏审计记录，再把最终文本事件写到 stdout。
	err = runner.RunEvents(runCtx, *prompt, func(event agent.Event) error {
		if err := sessionWriter.Append(event); err != nil {
			return err
		}
		if traceSink != nil {
			_ = traceSink(event)
		}
		if event.Type != agent.EventTextDelta {
			return nil
		}
		lastText = event.Text
		_, err := io.WriteString(out, event.Text)
		return err
	})
	closeErr := sessionWriter.Close()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "已取消")
			return 130
		}
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	if closeErr != nil {
		fmt.Fprintln(stderr, "错误：", closeErr)
		return 1
	}
	// CLI 输出约定：成功文本末尾补一个换行，便于回到 PowerShell 提示符。
	if lastText != "" && !strings.HasSuffix(lastText, "\n") {
		if _, err := io.WriteString(out, "\n"); err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
	}
	return 0
}
