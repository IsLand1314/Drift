// Package agent runs the bounded two-turn read_file agent loop.
package agent

import (
	"context"
	"errors"
	"strings"

	"gitee.com/island0920/drift/internal/llm"
	"gitee.com/island0920/drift/internal/tool"
)

var (
	errUnexpectedFirstCompletion  = errors.New("agent: unexpected first completion")
	errUnexpectedSecondCompletion = errors.New("agent: unexpected second completion")
	errExpectedOneToolCall        = errors.New("agent: expected exactly one tool call")
	errUnsupportedTool            = errors.New("agent: unsupported tool")
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
		Messages: messages,
		Tools:    []llm.ToolDefinition{tool.ReadDefinition()},
	}, func(event llm.StreamEvent) error {
		firstText.WriteString(event.Text)
		return nil
	})
	if err != nil {
		return err
	}

	if len(completion.Assistant.ToolCalls) == 0 {
		if completion.FinishReason == "stop" {
			return emitText(firstText.String())
		}
		return errUnexpectedFirstCompletion
	}
	if len(completion.Assistant.ToolCalls) != 1 {
		return errExpectedOneToolCall
	}

	call := completion.Assistant.ToolCalls[0]
	if call.Name != "read_file" {
		return errUnsupportedTool
	}
	messages = append(messages, completion.Assistant)
	content, readErr := tool.Read(root, call.Arguments)
	if readErr != nil {
		content = "read_file failed: unable to read requested file"
	}
	messages = append(messages, llm.Message{Role: "tool", Content: content, ToolCallID: call.ID})

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
