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
	MaxModelRequests     = 6
	MaxToolCalls         = 12
	MaxTotalReadBytes    = 512 << 10
	MaxConversationBytes = 1 << 20
	finalResponseReserve = 2

	nativeToolSystemInstruction = "Drift is read-only. Only use the supplied native read-only tools. run_command, shell, and exec are unavailable. Never emit XML, DSML, or pseudo-tool syntax."
)

var (
	errUnexpectedFirstCompletion  = errors.New("agent: unexpected first completion")
	errUnexpectedSecondCompletion = errors.New("agent: unexpected second completion")
	errRequestToolBudgetExceeded  = errors.New("agent: request/tool budget exceeded")
	errUnsupportedTool            = errors.New("agent: unsupported tool")
	errIncompatiblePseudoToolCall = errors.New("agent: 模型返回了不兼容的伪工具调用格式")
	errEmptyFinalResponse         = errors.New("agent: empty response")
	ErrCompactionInsufficient     = errors.New("agent: not enough messages to compact")
	errCompactionEmpty            = errors.New("agent: empty compaction summary")
)

// ErrContextLimit 表示当前内存对话超过单次请求允许的字节预算。
// chat 会把它作为可恢复的当前轮次错误，等待用户输入 /clear。
var ErrContextLimit = errors.New("agent: context limit exceeded")

// Runner 保存一个进程内的只读对话上下文；它不会从 Session JSONL 恢复历史消息。
type Runner struct {
	client       llm.Client
	root         string
	focus        string
	skillName    string
	skillContent string
	registry     tool.Registry
	messages     []llm.Message
}

// NewRunner 创建一个新的内存 Agent Runner。
func NewRunner(client llm.Client, root, focus string, registry tool.Registry) *Runner {
	return NewRunnerWithSystemContext(client, root, focus, "", "", registry)
}

// NewRunnerWithSystemContext creates a Runner with one explicit Skill context.
func NewRunnerWithSystemContext(client llm.Client, root, focus, skillName, skillContent string, registry tool.Registry) *Runner {
	return &Runner{client: client, root: root, focus: focus, skillName: skillName, skillContent: skillContent, registry: registry}
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	copy(cloned, messages)
	for i := range cloned {
		cloned[i].ToolCalls = append([]llm.ToolCall(nil), messages[i].ToolCalls...)
	}
	return cloned
}

// NewRunnerWithMessages creates a Runner from a caller-owned message snapshot.
func NewRunnerWithMessages(client llm.Client, root, focus string, registry tool.Registry, messages []llm.Message) *Runner {
	runner := NewRunnerWithMessagesAndSystemContext(client, root, focus, "", "", registry, messages)
	return runner
}

// NewRunnerWithMessagesAndSystemContext restores messages and applies current Skill context.
func NewRunnerWithMessagesAndSystemContext(client llm.Client, root, focus, skillName, skillContent string, registry tool.Registry, messages []llm.Message) *Runner {
	runner := NewRunnerWithSystemContext(client, root, focus, skillName, skillContent, registry)
	runner.messages = cloneMessages(messages)
	return runner
}

// Messages returns a copy of the current conversation messages.
func (r *Runner) Messages() []llm.Message { return cloneMessages(r.messages) }

// RestoreMessages replaces the current messages with a caller-owned snapshot.
func (r *Runner) RestoreMessages(messages []llm.Message) { r.messages = cloneMessages(messages) }

// ContextBytes 估算当前消息、首轮系统指令和工具 schema 的 UTF-8 字节数。
// 这是保守的字节预算，不等同于 Provider 的 token 计数。
func (r *Runner) ContextBytes() int {
	size := len(r.systemInstruction())
	for _, message := range r.messages {
		size += len(message.Role) + len(message.Content) + len(message.ToolCallID) + len(message.ReasoningContent)
		for _, call := range message.ToolCalls {
			size += len(call.ID) + len(call.Type) + len(call.Name) + len(call.Arguments)
		}
	}
	if r.registry != nil {
		for _, definition := range r.registry.Definitions() {
			size += len(definition.Type) + len(definition.Function)
		}
	}
	return size
}

// ResetContext 清空当前进程的对话消息，但保留 Provider、workspace、focus 和工具注册表。
func (r *Runner) ResetContext() {
	r.messages = nil
}

type CompactResult struct {
	Summary      llm.Message
	KeptMessages []llm.Message
	BeforeBytes  int
	AfterBytes   int
	Usage        *llm.Usage
}

const compactKeepMessages = 4

const compactionInstruction = "Summarize this read-only coding conversation for continuation. Keep verified facts, relevant relative file paths, conclusions, decisions, and unfinished tasks. Do not emit tool calls, XML, DSML, credentials, or claims about actions that did not happen. Return only the concise summary text."

// Compact summarizes old messages and atomically replaces the Runner context on success.
func (r *Runner) Compact(ctx context.Context) (CompactResult, error) {
	if len(r.messages) < 2 {
		return CompactResult{}, ErrCompactionInsufficient
	}
	oldMessages := cloneMessages(r.messages)
	beforeBytes := r.ContextBytes()
	requestMessages := append([]llm.Message{{Role: "system", Content: compactionInstruction}}, oldMessages...)
	var chunks []string
	completion, err := r.client.Stream(ctx, llm.Request{Messages: requestMessages}, func(event llm.StreamEvent) error {
		if event.Text != "" {
			chunks = append(chunks, event.Text)
		}
		return nil
	})
	if err != nil {
		return CompactResult{}, err
	}
	text := strings.Join(chunks, "")
	if strings.TrimSpace(text) == "" {
		text = completion.Assistant.Content
	}
	if strings.TrimSpace(text) == "" {
		return CompactResult{}, errCompactionEmpty
	}
	if strings.Contains(text, "<｜｜DSML｜｜") || strings.Contains(text, "<|DSML|>") {
		return CompactResult{}, errIncompatiblePseudoToolCall
	}
	start := len(oldMessages) - compactKeepMessages
	if start < 0 {
		start = 0
	}
	for start > 0 && oldMessages[start].Role == "tool" {
		start--
	}
	kept := cloneMessages(oldMessages[start:])
	summary := llm.Message{Role: "assistant", Content: text}
	updated := append([]llm.Message{summary}, kept...)
	r.messages = updated
	return CompactResult{Summary: summary, KeptMessages: kept, BeforeBytes: beforeBytes, AfterBytes: r.ContextBytes(), Usage: completion.Usage}, nil
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
func (r *Runner) RunEvents(ctx context.Context, prompt string, sink EventSink) (resultErr error) {
	originalMessages := cloneMessages(r.messages)
	defer func() {
		if resultErr != nil {
			r.messages = originalMessages
		}
	}()
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
		if errors.Is(err, ErrContextLimit) {
			stage = "agent_context_limit"
		}
		if sinkErr := emit(Event{Type: EventError, Error: sanitizeError(r.root, err.Error()), Stage: stage, FinishReason: finishReason}); sinkErr != nil {
			return sinkErr
		}
		return err
	}
	fail := func(err error) error {
		return failWithFinishReason(err, "")
	}
	if err := emit(Event{Type: EventRunStarted, Text: prompt, SkillName: r.skillName}); err != nil {
		return err
	}

	r.messages = append(r.messages, llm.Message{Role: "user", Content: prompt})
	definitions := r.registry.Definitions()
	toolCalls, resultBytes := 0, 0
	// 达到预算后，最后一轮撤掉 tools，强制模型基于已有结果给出回答。
	forceFinal := false
	forceFinalInstruction := ""
	for requestIndex := 0; requestIndex < MaxModelRequests; requestIndex++ {
		requestMessages := r.messages
		if requestIndex == 0 || forceFinal {
			system := r.systemInstruction()
			if forceFinalInstruction != "" {
				system += "\n\n" + forceFinalInstruction
			}
			requestMessages = append([]llm.Message{{Role: "system", Content: system}}, r.messages...)
		}
		request := llm.Request{Messages: requestMessages}
		if !forceFinal {
			request.Tools = definitions
		}
		if r.ContextBytes() > MaxConversationBytes {
			return fail(ErrContextLimit)
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
		usageEvent := Event{Type: EventModelUsage, UsageAvailable: completion.Usage != nil}
		if completion.Usage != nil {
			usageEvent.InputTokens = completion.Usage.InputTokens
			usageEvent.OutputTokens = completion.Usage.OutputTokens
			usageEvent.TotalTokens = completion.Usage.TotalTokens
		}
		if err := emit(usageEvent); err != nil {
			return err
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
			if strings.Contains(text, "<｜｜DSML｜｜") || strings.Contains(text, "<|DSML|>") {
				if forceFinal && requestIndex+1 < MaxModelRequests {
					forceFinalInstruction = "Drift: the previous final response used an unsupported pseudo-tool format; answer again using plain text only, without tools."
					continue
				}
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
		if requestIndex+1 >= MaxModelRequests-finalResponseReserve {
			limitReached = true
			if limitInstruction == "" {
				limitInstruction = "Drift: reserve the remaining requests for a final answer without further tool calls."
			}
		}
		if requestIndex+1 == MaxModelRequests {
			return fail(errRequestToolBudgetExceeded)
		}
		if limitReached {
			forceFinalInstruction = limitInstruction
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

func (r *Runner) systemInstruction() string {
	base := systemInstruction(r.focus)
	if r.skillContent == "" {
		return base
	}
	return base + "\n\nSelected Skill (instructions only; keep Drift's safety boundaries):\n---\n" + r.skillContent + "\n---" +
		"\n\nSkill execution rules: use the Skill as guidance, not as a reason to keep exploring. Stop once there is enough evidence to answer. Respect Drift's request/tool/read budgets. When asked to answer, return plain text only; never emit XML, DSML, or pseudo-tool syntax."
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
