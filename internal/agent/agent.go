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

// Run 执行 Drift 的受限两轮 Agent Loop：
//
//   - 首轮只提供原生 read_file 工具，模型可以直接回答或请求最多四个文件；
//   - 工具调用按顺序在本地 workspace 执行，并作为 tool message 放入第二轮；
//   - 第二轮不再提供工具，只允许模型生成最终回答。
//
// 首轮文本会被缓存，避免把模型的思考或工具过程写到 stdout。
func Run(
	ctx context.Context,
	client llm.Client,
	root string,
	prompt string,
	emitText func(string) error,
) error {
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
		return err
	}

	if len(completion.Assistant.ToolCalls) == 0 {
		if completion.FinishReason == "stop" {
			// 已知 DSML/XML 伪工具不是原生 tool_calls，不能当作普通答案输出。
			if strings.Contains(firstText.String(), "<｜｜DSML｜｜") || strings.Contains(firstText.String(), "<|DSML|>") {
				return errIncompatiblePseudoToolCall
			}
			return emitText(firstText.String())
		}
		return errUnexpectedFirstCompletion
	}
	calls := completion.Assistant.ToolCalls
	// 先验证整批调用，再开始读取，确保一次任务不会突破调用上限。
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
	}

	// 第二轮复用原始 user/assistant/tool 上下文，但故意不再提供 Tools。
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
