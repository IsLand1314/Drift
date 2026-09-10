package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"gitee.com/island0920/drift/internal/llm"
	"gitee.com/island0920/drift/internal/llm/openai"
	"gitee.com/island0920/drift/internal/ui/print"
)

func Run(ctx context.Context, args []string, getenv func(string) string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("drift", flag.ContinueOnError)
	flags.SetOutput(stderr)
	base := getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	prompt := flags.String("p", "", "发送一次提示词并流式输出回复")
	model := flags.String("model", getenv("OPENAI_MODEL"), "模型名称（默认 OPENAI_MODEL）")
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
	key := strings.TrimSpace(getenv("OPENAI_API_KEY"))
	if key == "" || strings.TrimSpace(*model) == "" {
		fmt.Fprintln(stderr, "请设置 OPENAI_API_KEY，并通过 OPENAI_MODEL 或 -model 指定模型")
		return 2
	}
	client, err := openai.New(*baseURL, key)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	err = print.Run(ctx, client, llm.Request{Model: *model, Messages: []llm.Message{{Role: "user", Content: *prompt}}}, out)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "已取消")
			return 130
		}
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	return 0
}
