package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"gitee.com/island0920/drift/internal/agent"
	"gitee.com/island0920/drift/internal/config"
	"gitee.com/island0920/drift/internal/llm"
	"gitee.com/island0920/drift/internal/llm/openai"
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
	model := flags.String("model", lookup("OPENAI_MODEL"), "模型名称（默认 OPENAI_MODEL）")
	baseURL := flags.String("base-url", base, "API 根地址，包含 /v1，不含 /chat/completions")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(stderr, "用法：drift -p \"你好\" [-model 模型名] [-base-url API根地址]")
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
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	var lastText string
	// Agent 只把最终文本交给 emit；工具调用过程不会直接写入 stdout。
	err = agent.Run(ctx, modelClient{Client: client, model: *model}, root, *prompt, func(text string) error {
		lastText = text
		_, err := io.WriteString(out, text)
		return err
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "已取消")
			return 130
		}
		fmt.Fprintln(stderr, "错误：", err)
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
