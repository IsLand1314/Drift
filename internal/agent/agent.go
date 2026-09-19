// Package agent runs the bounded two-turn read_file agent loop.
package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
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

// Run 执行受限两轮 Agent Loop，并只把最终文本交给 emitText。
// 首轮文本会被缓存，避免把模型的思考或工具过程写到 stdout。
func Run(
	ctx context.Context,
	client llm.Client,
	root string,
	prompt string,
	emitText func(string) error,
) error {
	return RunEvents(ctx, client, root, prompt, func(event Event) error {
		if event.Type != EventTextDelta {
			return nil
		}
		return emitText(event.Text)
	})
}

// RunEvents 执行 Agent Loop，并把稳定的 Runtime 事件交给 sink。
// 当前阶段仍直接使用内建 read_file；工具注册表注入在后续阶段接入。
func RunEvents(
	ctx context.Context,
	client llm.Client,
	root string,
	prompt string,
	sink EventSink,
) error {
	emit := func(event Event) error {
		if sink == nil {
			return nil
		}
		return sink(event)
	}
	fail := func(err error) error {
		if err == nil {
			return nil
		}
		if sinkErr := emit(Event{Type: EventError, Error: sanitizeError(root, err.Error())}); sinkErr != nil {
			return sinkErr
		}
		return err
	}

	if err := emit(Event{Type: EventRunStarted, Text: prompt}); err != nil {
		return err
	}

	messages := []llm.Message{{Role: "user", Content: prompt}}
	var firstText strings.Builder
	// 首轮同时发送只读 system 指令、用户问题和唯一的 read_file schema。
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
		return fail(err)
	}

	if len(completion.Assistant.ToolCalls) == 0 {
		if completion.FinishReason == "stop" {
			// 已知 DSML/XML 伪工具不是原生 tool_calls，不能当作普通答案输出。
			if strings.Contains(firstText.String(), "<｜｜DSML｜｜") || strings.Contains(firstText.String(), "<|DSML|>") {
				return fail(errIncompatiblePseudoToolCall)
			}
			if err := emit(Event{Type: EventTextDelta, Text: firstText.String()}); err != nil {
				return err
			}
			if err := emit(Event{Type: EventRunFinished}); err != nil {
				return err
			}
			return nil
		}
		return fail(errUnexpectedFirstCompletion)
	}
	calls := completion.Assistant.ToolCalls
	// 先验证整批调用，再开始读取，确保一次任务不会突破调用上限。
	if len(calls) > MaxToolCalls {
		return fail(errTooManyToolCalls)
	}
	for _, call := range calls {
		if call.Name != "read_file" {
			return fail(errUnsupportedTool)
		}
	}

	messages = append(messages, completion.Assistant)
	readBytes := 0
	for _, call := range calls {
		if err := emit(Event{
			Type:       EventToolCall,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Arguments:  call.Arguments,
		}); err != nil {
			return err
		}
		// 工具错误会被替换成脱敏结果；模型只能知道读取失败，不能看到本地路径细节。
		content, readErr := tool.Read(root, call.Arguments)
		if readErr != nil {
			content = "read_file failed: unable to read requested file"
		} else if readBytes+len(content) > MaxTotalReadBytes {
			content = "read_file failed: total read limit exceeded"
		} else {
			readBytes += len(content)
		}
		messages = append(messages, llm.Message{Role: "tool", Content: content, ToolCallID: call.ID})
		if err := emit(Event{
			Type:       EventToolResult,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Result:     content,
		}); err != nil {
			return err
		}
	}

	// 第二轮复用原始 user/assistant/tool 上下文，但故意不再提供 Tools。
	completion, err = client.Stream(ctx, llm.Request{Messages: messages}, func(event llm.StreamEvent) error {
		if event.Text == "" {
			return nil
		}
		return emit(Event{Type: EventTextDelta, Text: event.Text})
	})
	if err != nil {
		return fail(err)
	}
	if completion.FinishReason != "stop" || len(completion.Assistant.ToolCalls) != 0 {
		return fail(errUnexpectedSecondCompletion)
	}
	if err := emit(Event{Type: EventRunFinished}); err != nil {
		return err
	}
	return nil
}

func sanitizeError(root, message string) string {
	if root == "" {
		return message
	}
	return strings.ReplaceAll(message, root, "<workspace>")
}
