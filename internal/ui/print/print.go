package print

import (
	"context"
	"io"

	"gitee.com/island0920/drift/internal/llm"
)

func Run(ctx context.Context, client llm.Client, req llm.Request, out io.Writer) error {
	var last string
	err := client.Stream(ctx, req, func(text string) error {
		_, err := io.WriteString(out, text)
		last = text
		return err
	})
	if last != "" && last[len(last)-1] != '\n' {
		_, newlineErr := io.WriteString(out, "\n")
		if err == nil {
			err = newlineErr
		}
	}
	return err
}
