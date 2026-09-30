// Package agent runs the bounded read-only agent loop.
package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

const (
	MaxModelRequests  = 4
	MaxToolCalls      = 6
	MaxTotalReadBytes = 512 << 10

	nativeToolSystemInstruction = "Drift is read-only. Only use the supplied native read-only tools. run_command, shell, and exec are unavailable. Never emit XML, DSML, or pseudo-tool syntax."
)

var (
	errUnexpectedFirstCompletion  = errors.New("agent: unexpected first completion")
	errUnexpectedSecondCompletion = errors.New("agent: unexpected second completion")
	errRequestToolBudgetExceeded  = errors.New("agent: request/tool budget exceeded")
	errUnsupportedTool            = errors.New("agent: unsupported tool")
	errIncompatiblePseudoToolCall = errors.New("agent: 模型返回了不兼容的伪工具调用格式")
	errEmptyFinalResponse         = errors.New("agent: empty response")
)

// Runner 保存一个进程内的只读对话上下文；它不会从 Session JSONL 恢复历史消息。
type Runner struct {
	client   llm.Client
	root     string
	focus    string
	registry tool.Registry
	messages []llm.Message
}

// NewRunner 创建一个新的内存 Agent Runner。
func NewRunner(client llm.Client, root, focus string, registry tool.Registry) *Runner {
	return &Runner{client: client, root: root, focus: focus, registry: registry}
}

// Run 执行受限 Agent Loop，并只把最终文本交给 emitText。
func Run(ctx context.Context, client llm.Client, root, prompt string, emitText func(string) error) error {
	return RunEvents(ctx, client, root, prompt, func(event Event) error {
		if event.Type == EventTextDelta {
			return emitText(event.Text)
		}
		return nil
	})
}

// RunEvents 执行 Agent Loop，并把稳定的 Runtime 事件交给 sink。
func RunEvents(ctx context.Context, client llm.Client, root, prompt string, sink EventSink) error {
	return RunEventsWithRegistry(ctx, client, root, prompt, "", tool.NewDefaultRegistry(), sink)
}

// RunEventsWithRegistry 使用调用方提供的工具注册表执行受限多轮 Agent Loop。
func RunEventsWithRegistry(ctx context.Context, client llm.Client, root, prompt, focus string, registry tool.Registry, sink EventSink) error {
	return NewRunner(client, root, focus, registry).RunEvents(ctx, prompt, sink)
}

// RunEvents 执行一轮输入，并把结果追加到当前 Runner 的内存上下文。
func (r *Runner) RunEvents(ctx context.Context, prompt string, sink EventSink) error {
	emit := func(event Event) error {
		if sink == nil {
			return nil
		}
		return sink(event)
	}
	failWithFinishReason := func(err error, finishReason string) error {
		stage := llm.ErrorStageOf(err)
		if stage == "" {
			stage = "agent"
		}
		if errors.Is(err, errEmptyFinalResponse) {
			stage = "agent_empty_response"
		}
		if sinkErr := emit(Event{Type: EventError, Error: sanitizeError(r.root, err.Error()), Stage: stage, FinishReason: finishReason}); sinkErr != nil {
			return sinkErr
		}
		return err
	}
	fail := func(err error) error {
		return failWithFinishReason(err, "")
	}
	if err := emit(Event{Type: EventRunStarted, Text: prompt}); err != nil {
		return err
	}

	r.messages = append(r.messages, llm.Message{Role: "user", Content: prompt})
	definitions := r.registry.Definitions()
	toolCalls, resultBytes := 0, 0
	// 达到预算后，最后一轮撤掉 tools，强制模型基于已有结果给出回答。
	forceFinal := false
	for requestIndex := 0; requestIndex < MaxModelRequests; requestIndex++ {
		requestMessages := r.messages
		if requestIndex == 0 {
			requestMessages = append([]llm.Message{{Role: "system", Content: systemInstruction(r.focus)}}, r.messages...)
		}
		request := llm.Request{Messages: requestMessages}
		if !forceFinal {
			request.Tools = definitions
		}
		var chunks []string
		completion, err := r.client.Stream(ctx, request, func(event llm.StreamEvent) error {
			if event.Text != "" {
				chunks = append(chunks, event.Text)
			}
			return nil
		})
		if err != nil {
			return fail(err)
		}

		calls := completion.Assistant.ToolCalls
		if len(calls) == 0 {
			if completion.FinishReason != "stop" {
				if requestIndex == 0 {
					return failWithFinishReason(errUnexpectedFirstCompletion, completion.FinishReason)
				}
				return failWithFinishReason(errUnexpectedSecondCompletion, completion.FinishReason)
			}
			text := strings.Join(chunks, "")
			if requestIndex == 0 && (strings.Contains(text, "<｜｜DSML｜｜") || strings.Contains(text, "<|DSML|>")) {
				return fail(errIncompatiblePseudoToolCall)
			}
			if strings.TrimSpace(text) == "" {
				return fail(errEmptyFinalResponse)
			}
			assistant := completion.Assistant
			assistant.Role = "assistant"
			assistant.Content = text
			r.messages = append(r.messages, assistant)
			for _, chunk := range chunks {
				if err := emit(Event{Type: EventTextDelta, Text: chunk}); err != nil {
					return err
				}
			}
			return emit(Event{Type: EventRunFinished, FinishReason: completion.FinishReason})
		}
		if forceFinal {
			return fail(errRequestToolBudgetExceeded)
		}

		// assistant 的 tool_calls 与随后每条 tool 结果必须一起回传，
		// 否则 Provider 无法把 tool_call_id 对应到本轮调用。
		r.messages = append(r.messages, completion.Assistant)
		for _, call := range calls {
			if _, ok := r.registry.Lookup(call.Name); !ok {
				return fail(errUnsupportedTool)
			}
		}
		limitReached := false
		limitInstruction := ""
		for _, call := range calls {
			if err := emit(Event{Type: EventToolCall, ToolCallID: call.ID, ToolName: call.Name, Arguments: call.Arguments}); err != nil {
				return err
			}
			var content string
			if toolCalls >= MaxToolCalls {
				content = call.Name + " failed: request/tool budget exceeded"
				limitReached = true
				limitInstruction = "Drift: request/tool budget exceeded; provide the final answer without further tool calls."
			} else {
				toolCalls++
				registeredTool, _ := r.registry.Lookup(call.Name)
				result, toolErr := registeredTool.Execute(ctx, r.root, call.Arguments)
				if toolErr != nil && ctx.Err() != nil {
					return fail(ctx.Err())
				}
				switch {
				case toolErr != nil:
					content = toolFailure(call.Name)
				case resultBytes+len(result) > MaxTotalReadBytes:
					content = call.Name + " failed: total read limit exceeded"
					limitReached = true
					limitInstruction = "Drift: total read limit exceeded; provide the final answer without further tool calls."
				default:
					content = result
					resultBytes += len(result)
				}
			}
			r.messages = append(r.messages, llm.Message{Role: "tool", Content: content, ToolCallID: call.ID})
			if err := emit(Event{Type: EventToolResult, ToolCallID: call.ID, ToolName: call.Name, Result: content}); err != nil {
				return err
			}
		}
		if toolCalls >= MaxToolCalls {
			limitReached = true
			if limitInstruction == "" {
				limitInstruction = "Drift: request/tool budget exceeded; provide the final answer without further tool calls."
			}
		}
		if resultBytes >= MaxTotalReadBytes {
			limitReached = true
			if limitInstruction == "" {
				limitInstruction = "Drift: total read limit exceeded; provide the final answer without further tool calls."
			}
		}
		if requestIndex+1 == MaxModelRequests {
			return fail(errRequestToolBudgetExceeded)
		}
		if limitReached {
			r.messages = append(r.messages, llm.Message{Role: "user", Content: limitInstruction})
		}
		forceFinal = limitReached
	}
	return fail(errRequestToolBudgetExceeded)
}

func systemInstruction(focus string) string {
	if focus == "" {
		return nativeToolSystemInstruction
	}
	return nativeToolSystemInstruction +
		"\n\nThe user-selected initial focus target is " + strconv.Quote(focus) +
		". Prioritize answering about it; use read_file only when needed."
}

func toolFailure(name string) string {
	if name == "read_file" {
		return "read_file failed: unable to read requested file"
	}
	return name + " failed: unable to execute requested tool"
}

func sanitizeError(root, message string) string {
	if root == "" {
		return message
	}
	return strings.ReplaceAll(message, root, "<workspace>")
}
