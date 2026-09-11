// Package agent runs the bounded two-turn read_file agent loop.
package agent

import (
	"context"
	"errors"
	"strings"

	"gitee.com/island0920/drift/internal/llm"
	"gitee.com/island0920/drift/internal/tool"
)

const (
	MaxToolCalls      = 4
	MaxTotalReadBytes = 512 << 10

	nativeToolSystemInstruction = "Drift is read-only. Only use the supplied native read_file tool. run_command, shell, and exec are unavailable. Never emit XML, DSML, or pseudo-tool syntax."
)

var (
	errUnexpectedFirstCompletion  = errors.New("agent: unexpected first completion")
	errUnexpectedSecondCompletion = errors.New("agent: unexpected second completion")
	errTooManyToolCalls           = errors.New("agent: 最多读取 4 个文件")
	errUnsupportedTool            = errors.New("agent: unsupported tool")
	errIncompatiblePseudoToolCall = errors.New("agent: 模型返回了不兼容的伪工具调用格式")
)

// Run completes either a direct answer or one read_file tool round trip.
func Run(
	ctx context.Context,
	client llm.Client,
	root string,
	prompt string,
	emitText func(string) error,
) error {
	messages := []llm.Message{{Role: "user", Content: prompt}}
	var firstText strings.Builder
	completion, err := client.Stream(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: nativeToolSystemInstruction},
			{Role: "user", Content: prompt},
		},
		Tools: []llm.ToolDefinition{tool.ReadDefinition()},
	}, func(event llm.StreamEvent) error {
		firstText.WriteString(event.Text)
		return nil
	})
	if err != nil {
		return err
	}

	if len(completion.Assistant.ToolCalls) == 0 {
		if completion.FinishReason == "stop" {
			if strings.Contains(firstText.String(), "<｜｜DSML｜｜") || strings.Contains(firstText.String(), "<|DSML|>") {
				return errIncompatiblePseudoToolCall
			}
			return emitText(firstText.String())
		}
		return errUnexpectedFirstCompletion
	}
	calls := completion.Assistant.ToolCalls
	if len(calls) > MaxToolCalls {
		return errTooManyToolCalls
	}
	for _, call := range calls {
		if call.Name != "read_file" {
			return errUnsupportedTool
		}
	}

	messages = append(messages, completion.Assistant)
	readBytes := 0
	for _, call := range calls {
		content, readErr := tool.Read(root, call.Arguments)
		if readErr != nil {
			content = "read_file failed: unable to read requested file"
		} else if readBytes+len(content) > MaxTotalReadBytes {
			content = "read_file failed: total read limit exceeded"
		} else {
			readBytes += len(content)
		}
		messages = append(messages, llm.Message{Role: "tool", Content: content, ToolCallID: call.ID})
	}

	completion, err = client.Stream(ctx, llm.Request{Messages: messages}, func(event llm.StreamEvent) error {
		if event.Text == "" {
			return nil
		}
		return emitText(event.Text)
	})
	if err != nil {
		return err
	}
	if completion.FinishReason != "stop" || len(completion.Assistant.ToolCalls) != 0 {
		return errUnexpectedSecondCompletion
	}
	return nil
}
