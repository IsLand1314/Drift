package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

const wantNativeToolSystemInstruction = "Drift is read-only. Only use the supplied native read-only tools. run_command, shell, and exec are unavailable. Never emit XML, DSML, or pseudo-tool syntax."

type scriptedStep struct {
	events     []llm.StreamEvent
	completion llm.Completion
	err        error
	beforeDone func()
}

type scriptedClient struct {
	steps    []scriptedStep
	requests []llm.Request
}

func (c *scriptedClient) Stream(ctx context.Context, request llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	c.requests = append(c.requests, request)
	if err := ctx.Err(); err != nil {
		return llm.Completion{}, err
	}
	if len(c.steps) == 0 {
		return llm.Completion{}, errors.New("unexpected stream request")
	}
	step := c.steps[0]
	c.steps = c.steps[1:]
	for _, event := range step.events {
		if err := emit(event); err != nil {
			return llm.Completion{}, err
		}
	}
	if step.beforeDone != nil {
		step.beforeDone()
	}
	return step.completion, step.err
}

func TestRunDirectStopBuffersFirstTurnText(t *testing.T) {
	var output []string
	client := &scriptedClient{steps: []scriptedStep{{
		events: []llm.StreamEvent{{Text: "direct answer"}},
		completion: llm.Completion{
			Assistant:    llm.Message{Role: "assistant", Content: "direct answer"},
			FinishReason: "stop",
		},
		beforeDone: func() {
			if len(output) != 0 {
				t.Fatalf("first-turn output emitted before stop: %q", output)
			}
		},
	}}}

	err := Run(context.Background(), client, t.TempDir(), "answer directly", func(text string) error {
		output = append(output, text)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(output, []string{"direct answer"}) {
		t.Fatalf("output = %q, want direct answer", output)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	request := client.requests[0]
	if !reflect.DeepEqual(request.Messages, []llm.Message{
		{Role: "system", Content: wantNativeToolSystemInstruction},
		{Role: "user", Content: "answer directly"},
	}) {
		t.Fatalf("first request messages = %#v", request.Messages)
	}
	if len(request.Tools) != 3 {
		t.Fatalf("first request tools = %d, want 3", len(request.Tools))
	}
}

func TestRunnerPreservesConversationAcrossTurns(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{
		{events: []llm.StreamEvent{{Text: "first answer"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "first answer"}, FinishReason: "stop"}},
		{events: []llm.StreamEvent{{Text: "second answer"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "second answer"}, FinishReason: "stop"}},
	}}
	runner := NewRunner(client, t.TempDir(), "", tool.NewDefaultRegistry())
	for _, prompt := range []string{"first question", "second question"} {
		if err := runner.RunEvents(context.Background(), prompt, nil); err != nil {
			t.Fatalf("RunEvents(%q) error = %v", prompt, err)
		}
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	second := client.requests[1].Messages
	if len(second) != 4 || second[0].Role != "system" || second[1].Content != "first question" || second[2].Role != "assistant" || second[2].Content != "first answer" || second[3].Content != "second question" {
		t.Fatalf("second request messages = %#v, want previous turn context", second)
	}
}

func TestRunnerContextCanReset(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{{
		events:     []llm.StreamEvent{{Text: "first"}},
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "first"}, FinishReason: "stop"},
	}}}
	runner := NewRunner(client, t.TempDir(), "", tool.NewDefaultRegistry())
	definitionsBefore := runner.registry.Definitions()
	if err := runner.RunEvents(context.Background(), "question", nil); err != nil {
		t.Fatal(err)
	}
	if runner.ContextBytes() == 0 {
		t.Fatal("ContextBytes() = 0 after a turn")
	}
	runner.ResetContext()
	if runner.ContextBytes() == 0 {
		t.Fatal("system/tool overhead should remain after reset")
	}
	if !reflect.DeepEqual(runner.registry.Definitions(), definitionsBefore) {
		t.Fatal("ResetContext changed tool definitions")
	}
}

func TestRunnerContextBytesIncludesMessageParts(t *testing.T) {
	runner := NewRunner(nil, t.TempDir(), "focus.md", tool.NewDefaultRegistry())
	base := runner.ContextBytes()
	runner.messages = append(runner.messages,
		llm.Message{Role: "user", Content: "question"},
		llm.Message{Role: "assistant", ReasoningContent: "reasoning", ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
		llm.Message{Role: "tool", Content: "tool result", ToolCallID: "call-1"},
	)
	if got := runner.ContextBytes(); got <= base {
		t.Fatalf("ContextBytes() = %d, want greater than base %d", got, base)
	}
}

func TestRunnerRestoresCopiedMessages(t *testing.T) {
	original := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read_file"}}}}
	runner := NewRunnerWithMessages(nil, t.TempDir(), "", tool.NewDefaultRegistry(), original)
	original[0].ToolCalls[0].Name = "mutated"
	if got := runner.Messages()[0].ToolCalls[0].Name; got != "read_file" {
		t.Fatalf("restored tool name = %q", got)
	}
}

func TestRunnerMessagesReturnsCopy(t *testing.T) {
	runner := NewRunnerWithMessages(nil, t.TempDir(), "", tool.NewDefaultRegistry(), []llm.Message{{Role: "user", Content: "hello"}})
	messages := runner.Messages()
	messages[0].Content = "mutated"
	if got := runner.Messages()[0].Content; got != "hello" {
		t.Fatalf("stored message content = %q", got)
	}
}

func TestRunnerCompactUsesNoToolsAndKeepsRecentMessages(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{{
		events:     []llm.StreamEvent{{Text: "summary"}},
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "summary"}, FinishReason: "stop"},
	}}}
	runner := NewRunnerWithMessages(client, t.TempDir(), "", tool.NewDefaultRegistry(), []llm.Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "middle question"},
		{Role: "assistant", Content: "middle answer"},
		{Role: "user", Content: "recent question"},
		{Role: "assistant", Content: "recent answer"},
	})
	result, err := runner.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || len(client.requests[0].Tools) != 0 {
		t.Fatalf("requests=%d tools=%d, want one request without tools", len(client.requests), len(client.requests[0].Tools))
	}
	if result.Summary.Content != "summary" || len(result.KeptMessages) != 4 {
		t.Fatalf("result=%+v", result)
	}
	messages := runner.Messages()
	if len(messages) != 5 || messages[0].Content != "summary" || messages[1].Content != "middle question" || messages[4].Content != "recent answer" {
		t.Fatalf("messages=%#v", messages)
	}
}

func TestRunnerCompactFailurePreservesMessages(t *testing.T) {
	original := []llm.Message{{Role: "user", Content: "one"}, {Role: "assistant", Content: "two"}}
	client := &scriptedClient{steps: []scriptedStep{{err: errors.New("provider failed")}}}
	runner := NewRunnerWithMessages(client, t.TempDir(), "", tool.NewDefaultRegistry(), original)
	_, err := runner.Compact(context.Background())
	if err == nil || !reflect.DeepEqual(runner.Messages(), original) {
		t.Fatalf("err=%v messages=%#v, want original messages", err, runner.Messages())
	}
}

func TestRunnerCompactSkipsInsufficientMessages(t *testing.T) {
	client := &scriptedClient{}
	runner := NewRunnerWithMessages(client, t.TempDir(), "", tool.NewDefaultRegistry(), []llm.Message{{Role: "user", Content: "one"}})
	if _, err := runner.Compact(context.Background()); !errors.Is(err, ErrCompactionInsufficient) {
		t.Fatalf("Compact() error=%v, want ErrCompactionInsufficient", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("provider requests=%d, want 0", len(client.requests))
	}
}

func TestRunnerContextLimitStopsBeforeProvider(t *testing.T) {
	client := &scriptedClient{}
	runner := NewRunner(client, t.TempDir(), "", tool.NewDefaultRegistry())
	runner.messages = []llm.Message{{Role: "user", Content: strings.Repeat("x", MaxConversationBytes)}}
	var events []Event
	err := runner.RunEvents(context.Background(), "next", func(event Event) error {
		events = append(events, event)
		return nil
	})
	if !errors.Is(err, ErrContextLimit) {
		t.Fatalf("RunEvents() error = %v, want ErrContextLimit", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("provider requests = %d, want 0", len(client.requests))
	}
	if len(events) != 2 || events[1].Type != EventError || events[1].Stage != "agent_context_limit" {
		t.Fatalf("events = %#v, want context limit error", events)
	}
	if strings.Contains(events[1].Error, "xxxx") {
		t.Fatal("context limit error leaked message content")
	}
}

func TestRunRejectsDSMLText(t *testing.T) {
	var output []string
	client := &scriptedClient{steps: []scriptedStep{{
		events: []llm.StreamEvent{{Text: "<｜｜DSML｜｜ calls>"}},
		completion: llm.Completion{
			Assistant:    llm.Message{Role: "assistant", Content: "<｜｜DSML｜｜ calls>"},
			FinishReason: "stop",
		},
	}}}

	err := Run(context.Background(), client, t.TempDir(), "answer directly", func(text string) error {
		output = append(output, text)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "模型返回了不兼容的伪工具调用格式") {
		t.Fatalf("Run() error = %v, want incompatible pseudo-tool error", err)
	}
	if len(output) != 0 {
		t.Fatalf("output = %q, want no text", output)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
}

func TestRunRejectsASCIIDSMLText(t *testing.T) {
	var output []string
	client := &scriptedClient{steps: []scriptedStep{{
		events: []llm.StreamEvent{{Text: "<|DSML|> calls"}},
		completion: llm.Completion{
			Assistant:    llm.Message{Role: "assistant", Content: "<|DSML|> calls"},
			FinishReason: "stop",
		},
	}}}

	err := Run(context.Background(), client, t.TempDir(), "answer directly", func(text string) error {
		output = append(output, text)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "模型返回了不兼容的伪工具调用格式") {
		t.Fatalf("Run() error = %v, want incompatible pseudo-tool error", err)
	}
	if len(output) != 0 {
		t.Fatalf("output = %q, want no text", output)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
}

func TestRunRejectsDSMLTextAfterNativeToolCall(t *testing.T) {
	call := llm.ToolCall{ID: "call-list", Type: "function", Name: "list_files", Arguments: `{}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "<｜｜DSML｜｜ calls>"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "<｜｜DSML｜｜ calls>"}, FinishReason: "stop"}},
	}}
	var output []string
	err := Run(context.Background(), client, t.TempDir(), "inspect", func(text string) error {
		output = append(output, text)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "模型返回了不兼容的伪工具调用格式") {
		t.Fatalf("Run() error = %v, want incompatible pseudo-tool error", err)
	}
	if len(output) != 0 || len(client.requests) != 2 {
		t.Fatalf("output=%q requests=%d, want no output and two requests", output, len(client.requests))
	}
}

func TestRunUsesNativeToolSystemInstruction(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{{
		events: []llm.StreamEvent{{Text: "done"}},
		completion: llm.Completion{
			Assistant:    llm.Message{Role: "assistant", Content: "done"},
			FinishReason: "stop",
		},
	}}}

	if err := Run(context.Background(), client, t.TempDir(), "answer directly", func(string) error { return nil }); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.requests) != 1 || len(client.requests[0].Messages) == 0 {
		t.Fatalf("first request messages = %#v, want system instruction", client.requests)
	}
	system := client.requests[0].Messages[0]
	if system.Role != "system" {
		t.Fatalf("first message role = %q, want system", system.Role)
	}
	for _, required := range []string{"native read-only tools", "run_command", "DSML", "pseudo-tool"} {
		if !strings.Contains(system.Content, required) {
			t.Fatalf("system instruction = %q, want %q", system.Content, required)
		}
	}
	if strings.Contains(system.Content, "read_file") {
		t.Fatalf("system instruction hardcodes read_file: %q", system.Content)
	}
}

func TestRunFocusOnlyAppearsInFirstSystemMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("focus content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "focus-read", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "done"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
	}}
	if err := RunEventsWithRegistry(context.Background(), client, root, "explain it", "README.md", tool.NewDefaultRegistry(), nil); err != nil {
		t.Fatalf("RunEventsWithRegistry() error = %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	first := client.requests[0]
	if len(first.Messages) < 1 || first.Messages[0].Role != "system" || !strings.Contains(first.Messages[0].Content, `"README.md"`) {
		t.Fatalf("first system message = %#v, want quoted focus", first.Messages)
	}
	second := client.requests[1]
	if len(second.Messages) == 0 || second.Messages[0].Role == "system" {
		t.Fatalf("second request unexpectedly contains system focus: %#v", second.Messages)
	}
	if second.Messages[1].Role != "assistant" || second.Messages[1].ToolCalls[0].Arguments != `{"path":"README.md"}` {
		t.Fatalf("second request tool call = %#v", second.Messages[1])
	}
}

func TestRunMultiTurnExplorationPreservesContextAndTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("needle in readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{
		{ID: "call-list", Type: "function", Name: "list_files", Arguments: `{}`},
		{ID: "call-search", Type: "function", Name: "search_text", Arguments: `{"query":"needle"}`},
		{ID: "call-read", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`},
	}
	client := &scriptedClient{steps: []scriptedStep{
		{events: []llm.StreamEvent{{Text: "listing"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "listing", ToolCalls: calls[:1]}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "searching"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "searching", ToolCalls: calls[1:2]}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "reading"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "reading", ToolCalls: calls[2:]}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "final "}, {Text: "answer"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "final answer"}, FinishReason: "stop"}},
	}}
	var output []string
	if err := Run(context.Background(), client, root, "inspect", func(text string) error {
		output = append(output, text)
		return nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(output, []string{"final ", "answer"}) {
		t.Fatalf("output = %#v, want final answer chunks only", output)
	}
	if len(client.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(client.requests))
	}
	for i, request := range client.requests {
		if len(request.Tools) != 3 {
			t.Fatalf("request %d tools = %d, want 3", i+1, len(request.Tools))
		}
	}
	wantMessages := []llm.Message{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", Content: "listing", ToolCalls: calls[:1]},
		{Role: "tool", Content: "README.md", ToolCallID: "call-list"},
		{Role: "assistant", Content: "searching", ToolCalls: calls[1:2]},
		{Role: "tool", Content: "README.md:1: needle in readme", ToolCallID: "call-search"},
		{Role: "assistant", Content: "reading", ToolCalls: calls[2:]},
		{Role: "tool", Content: "needle in readme\n", ToolCallID: "call-read"},
	}
	if !reflect.DeepEqual(client.requests[3].Messages, wantMessages) {
		t.Fatalf("fourth request messages = %#v, want %#v", client.requests[3].Messages, wantMessages)
	}
}

func TestRunRequestBudgetStopsAfterFourthToolCompletion(t *testing.T) {
	call := llm.ToolCall{ID: "call-list", Type: "function", Name: "list_files", Arguments: `{}`}
	steps := make([]scriptedStep, MaxModelRequests)
	for i := range steps {
		steps[i] = scriptedStep{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}}
	}
	client := &scriptedClient{steps: steps}
	err := Run(context.Background(), client, t.TempDir(), "keep exploring", func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "request/tool budget exceeded") {
		t.Fatalf("Run() error = %v, want request budget error", err)
	}
	if len(client.requests) != MaxModelRequests {
		t.Fatalf("requests = %d, want %d", len(client.requests), MaxModelRequests)
	}
}

func TestRunToolBudgetUsesToolFreeFinalRequest(t *testing.T) {
	calls := make([]llm.ToolCall, MaxToolCalls+1)
	for i := range calls {
		calls[i] = llm.ToolCall{ID: string(rune('a' + i)), Type: "function", Name: "list_files", Arguments: `{}`}
	}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "limit explained"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "limit explained"}, FinishReason: "stop"}},
	}}
	var events []Event
	if err := RunEvents(context.Background(), client, t.TempDir(), "inspect", func(event Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("RunEvents() error = %v", err)
	}
	if len(client.requests) != 2 || len(client.requests[1].Tools) != 0 {
		t.Fatalf("requests = %#v, want second tool-free final request", client.requests)
	}
	messages := client.requests[1].Messages
	limitResult := messages[len(messages)-2]
	if limitResult.Role != "tool" || limitResult.ToolCallID != calls[MaxToolCalls].ID || !strings.Contains(limitResult.Content, "budget exceeded") {
		t.Fatalf("limit tool result = %#v", limitResult)
	}
	if instruction := messages[len(messages)-1]; instruction.Role != "user" || !strings.Contains(instruction.Content, "budget exceeded") {
		t.Fatalf("final limit instruction = %#v", instruction)
	}
	toolCalls := 0
	for _, event := range events {
		if event.Type == EventToolCall {
			toolCalls++
		}
	}
	if toolCalls != len(calls) {
		t.Fatalf("tool_call events = %d, want %d", toolCalls, len(calls))
	}
}

func TestRunExactToolBudgetAddsLimitInstruction(t *testing.T) {
	calls := make([]llm.ToolCall, MaxToolCalls)
	for i := range calls {
		calls[i] = llm.ToolCall{ID: string(rune('a' + i)), Type: "function", Name: "list_files", Arguments: `{}`}
	}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "done"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
	}}
	callEvents := map[string]int{}
	if err := RunEvents(context.Background(), client, t.TempDir(), "inspect", func(event Event) error {
		if event.Type == EventToolCall {
			callEvents[event.ToolCallID]++
		}
		return nil
	}); err != nil {
		t.Fatalf("RunEvents() error = %v", err)
	}
	final := client.requests[1]
	if len(final.Tools) != 0 {
		t.Fatalf("final request tools = %d, want none", len(final.Tools))
	}
	last := final.Messages[len(final.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "request/tool budget exceeded") {
		t.Fatalf("final limit instruction = %#v", last)
	}
	for _, call := range calls {
		if callEvents[call.ID] != 1 {
			t.Fatalf("tool_call events for %q = %d, want 1", call.ID, callEvents[call.ID])
		}
	}
}

func TestRunReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello from readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output []string
	readCall := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{
			events: []llm.StreamEvent{{Text: "I will inspect it."}},
			completion: llm.Completion{
				Assistant:    llm.Message{Role: "assistant", Content: "I will inspect it.", ToolCalls: []llm.ToolCall{readCall}},
				FinishReason: "tool_calls",
			},
		},
		{
			events:     []llm.StreamEvent{{Text: "The README says hello."}},
			completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "The README says hello."}, FinishReason: "stop"},
		},
	}}

	err := Run(context.Background(), client, root, "read the readme", func(text string) error {
		output = append(output, text)
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(output, []string{"The README says hello."}) {
		t.Fatalf("output = %q, want final answer only", output)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	second := client.requests[1]
	if len(second.Tools) != 3 {
		t.Fatalf("second request tools = %d, want 3", len(second.Tools))
	}
	wantMessages := []llm.Message{
		{Role: "user", Content: "read the readme"},
		{Role: "assistant", Content: "I will inspect it.", ToolCalls: []llm.ToolCall{readCall}},
		{Role: "tool", Content: "hello from readme\n", ToolCallID: "call-1"},
	}
	if !reflect.DeepEqual(second.Messages, wantMessages) {
		t.Fatalf("second request messages = %#v, want %#v", second.Messages, wantMessages)
	}
}

func TestRunMultipleReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"README.md":       "readme\n",
		"go.mod":          "module example.com/drift\n",
		"spec/current.md": "current spec\n",
	} {
		filename := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	calls := []llm.ToolCall{
		{ID: "call-readme", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`},
		{ID: "call-module", Type: "function", Name: "read_file", Arguments: `{"path":"go.mod"}`},
		{ID: "call-spec", Type: "function", Name: "read_file", Arguments: `{"path":"spec/current.md"}`},
	}
	first := llm.Message{Role: "assistant", Content: "I will inspect the files.", ToolCalls: calls}
	client := &scriptedClient{steps: []scriptedStep{
		{events: []llm.StreamEvent{{Text: "I will inspect the files."}}, completion: llm.Completion{Assistant: first, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "The files are consistent."}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "The files are consistent."}, FinishReason: "stop"}},
	}}
	var output []string
	if err := Run(context.Background(), client, root, "inspect the project", func(text string) error {
		output = append(output, text)
		return nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(output, []string{"The files are consistent."}) {
		t.Fatalf("output = %#v, want final text only", output)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	second := client.requests[1]
	if len(second.Tools) != 3 {
		t.Fatalf("second request tools = %d, want 3", len(second.Tools))
	}
	wantMessages := []llm.Message{
		{Role: "user", Content: "inspect the project"},
		first,
		{Role: "tool", Content: "readme\n", ToolCallID: "call-readme"},
		{Role: "tool", Content: "module example.com/drift\n", ToolCallID: "call-module"},
		{Role: "tool", Content: "current spec\n", ToolCallID: "call-spec"},
	}
	if !reflect.DeepEqual(second.Messages, wantMessages) {
		t.Fatalf("second request messages = %#v, want %#v", second.Messages, wantMessages)
	}
}

func TestRunEnforcesAggregateReadBudget(t *testing.T) {
	root := t.TempDir()
	calls := make([]llm.ToolCall, 4)
	for i := range calls {
		path := "file" + string(rune('a'+i)) + ".txt"
		content := strings.Repeat(string(rune('a'+i)), 128<<10)
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		calls[i] = llm.ToolCall{ID: "call-" + string(rune('a'+i)), Type: "function", Name: "read_file", Arguments: `{"path":"` + path + `"}`}
	}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "done"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
	}}
	if err := Run(context.Background(), client, root, "read the files", func(string) error { return nil }); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	second := client.requests[1]
	if len(second.Tools) != 0 {
		t.Fatalf("second request tools = %d, want none", len(second.Tools))
	}
	if len(second.Messages) != 7 {
		t.Fatalf("second request messages = %d, want 7", len(second.Messages))
	}
	total := 0
	for i, call := range calls {
		result := second.Messages[i+2]
		if result.Role != "tool" || result.ToolCallID != call.ID {
			t.Fatalf("tool message %d = %#v, want result for %q", i, result, call.ID)
		}
		if len(result.Content) != 128<<10 {
			t.Fatalf("tool message %d content length = %d, want %d", i, len(result.Content), 128<<10)
		}
		total += len(result.Content)
	}
	if total != 512<<10 {
		t.Fatalf("total read bytes = %d, want %d", total, 512<<10)
	}
	limit := second.Messages[len(second.Messages)-1]
	if limit.Role != "user" || !strings.Contains(limit.Content, "total read limit exceeded") {
		t.Fatalf("final limit instruction = %#v", limit)
	}
}

func TestRunFirstStopWithToolCallUsesReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello from readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{
			events:     []llm.StreamEvent{{Text: "I will inspect it."}},
			completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "I will inspect it.", ToolCalls: []llm.ToolCall{call}}, FinishReason: "stop"},
		},
		{
			events:     []llm.StreamEvent{{Text: "The README says hello."}},
			completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "The README says hello."}, FinishReason: "stop"},
		},
	}}
	var output []string
	if err := Run(context.Background(), client, root, "read the readme", func(text string) error {
		output = append(output, text)
		return nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(output, []string{"The README says hello."}) {
		t.Fatalf("output = %q, want final answer only", output)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
}

func TestRunReadFailuresStillReachSecondTurn(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments string
	}{
		{name: "malformed arguments", arguments: `{"path":`},
		{name: "missing file", arguments: `{"path":"missing.md"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: test.arguments}
			client := &scriptedClient{steps: []scriptedStep{
				{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
				{events: []llm.StreamEvent{{Text: "done"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
			}}

			err := Run(context.Background(), client, t.TempDir(), "read a file", func(string) error { return nil })
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(client.requests) != 2 {
				t.Fatalf("requests = %d, want 2", len(client.requests))
			}
			result := client.requests[1].Messages[2]
			if result.Role != "tool" || result.ToolCallID != "call-1" || len(result.Content) <= len("read_file failed: ") || result.Content[:len("read_file failed: ")] != "read_file failed: " {
				t.Fatalf("tool result = %#v, want read error result", result)
			}
		})
	}
}

func TestRunSanitizesDotEnvReadFailureForSecondTurn(t *testing.T) {
	root := t.TempDir()
	const secret = "synthetic-openai-key-must-not-reach-model"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("OPENAI_API_KEY="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "call-env", Type: "function", Name: "read_file", Arguments: `{"path":".env"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "done"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
	}}

	if err := Run(context.Background(), client, root, "inspect configuration", func(string) error { return nil }); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	result := client.requests[1].Messages[2]
	if result.Content != "read_file failed: unable to read requested file" {
		t.Fatalf("second-turn tool result = %q, want sanitized failure", result.Content)
	}
	if strings.Contains(result.Content, secret) {
		t.Fatalf("second-turn tool result leaked credential: %q", result.Content)
	}
}

func TestRunRejectsUnsupportedToolCallStates(t *testing.T) {
	tests := []struct {
		name        string
		calls       []llm.ToolCall
		finish      string
		wantError   string
		wantRequest int
	}{
		{
			name:        "unknown tool",
			calls:       []llm.ToolCall{{ID: "one", Type: "function", Name: "delete_file", Arguments: `{}`}},
			finish:      "tool_calls",
			wantError:   "unsupported tool",
			wantRequest: 1,
		},
		{
			name:        "unexpected first finish",
			finish:      "length",
			wantError:   "unexpected first completion",
			wantRequest: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &scriptedClient{steps: []scriptedStep{{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: test.calls}, FinishReason: test.finish}}}}
			err := Run(context.Background(), client, t.TempDir(), "test", func(string) error { return nil })
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Run() error = %v, want containing %q", err, test.wantError)
			}
			if len(client.requests) != test.wantRequest {
				t.Fatalf("requests = %d, want %d", len(client.requests), test.wantRequest)
			}
		})
	}
}

func TestRunRecordsUnexpectedFinishReason(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{{
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "length"},
	}}}
	var events []Event
	err := RunEvents(context.Background(), client, t.TempDir(), "test", func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err == nil {
		t.Fatal("RunEvents() error = nil, want unexpected completion error")
	}
	if len(events) != 3 || events[2].Type != EventError || events[2].FinishReason != "length" {
		t.Fatalf("events = %#v, want error finish_reason=length", events)
	}
}

func TestRunReturnsOutputCallbackFailure(t *testing.T) {
	want := errors.New("output unavailable")
	client := &scriptedClient{steps: []scriptedStep{{
		events:     []llm.StreamEvent{{Text: "answer"}},
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"},
	}}}
	if err := Run(context.Background(), client, t.TempDir(), "test", func(string) error { return want }); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
}

func TestRunRejectsEmptyFinalResponse(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{{
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"},
	}}}
	var events []Event
	err := RunEvents(context.Background(), client, t.TempDir(), "test", func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("RunEvents() error = %v, want empty response error", err)
	}
	if len(events) != 3 || events[2].Type != EventError || events[2].Stage != "agent_empty_response" {
		t.Fatalf("events = %#v, want agent_empty_response error", events)
	}
}

func TestRunReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &scriptedClient{}
	if err := Run(ctx, client, t.TempDir(), "test", func(string) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
}

func TestRunStopsWhenToolExecutionCancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	registry, err := tool.NewRegistry(cancelingTool{cancel: cancel})
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "cancel", Type: "function", Name: "cancel_tool", Arguments: `{}`}
	client := &scriptedClient{steps: []scriptedStep{{
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"},
	}}}
	err = RunEventsWithRegistry(ctx, client, t.TempDir(), "cancel", "", registry, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunEventsWithRegistry() error = %v, want context canceled", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want no request after cancellation", len(client.requests))
	}
}

func TestRunPreservesReasoningContentForSecondRequest(t *testing.T) {
	call := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ReasoningContent: "I need the readme", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "done"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}},
	}}
	if err := Run(context.Background(), client, t.TempDir(), "test", func(string) error { return nil }); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := client.requests[1].Messages[1].ReasoningContent; got != "I need the readme" {
		t.Fatalf("second request reasoning content = %q, want preserved value", got)
	}
}

func TestRunEventsEmitsOrderedRuntimeEvents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello from readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{
			events:     []llm.StreamEvent{{Text: "I will inspect it."}},
			completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "I will inspect it.", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls", Usage: &llm.Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14}},
		},
		{
			events:     []llm.StreamEvent{{Text: "The "}, {Text: "README says hello."}},
			completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "The README says hello."}, FinishReason: "stop", Usage: &llm.Usage{InputTokens: 20, OutputTokens: 6, TotalTokens: 26}},
		},
	}}

	var events []Event
	err := RunEvents(context.Background(), client, root, "read the readme", func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("RunEvents() error = %v", err)
	}
	if got := []EventType{
		events[0].Type, events[1].Type, events[2].Type, events[3].Type,
		events[4].Type, events[5].Type, events[6].Type, events[7].Type,
	}; !reflect.DeepEqual(got, []EventType{
		EventRunStarted,
		EventModelUsage,
		EventToolCall,
		EventToolResult,
		EventModelUsage,
		EventTextDelta,
		EventTextDelta,
		EventRunFinished,
	}) {
		t.Fatalf("event types = %#v, want ordered runtime events", got)
	}
	if events[0].Text != "read the readme" {
		t.Fatalf("run_started text = %q, want prompt", events[0].Text)
	}
	if events[1].InputTokens != 10 || events[1].OutputTokens != 4 || !events[1].UsageAvailable {
		t.Fatalf("first usage event = %#v", events[1])
	}
	if events[2].ToolCallID != call.ID || events[2].ToolName != call.Name || events[2].Arguments != call.Arguments {
		t.Fatalf("tool_call event = %#v, want call metadata", events[2])
	}
	if events[3].ToolCallID != call.ID || events[3].Result != "hello from readme\n" || strings.Contains(events[3].Result, root) {
		t.Fatalf("tool_result event = %#v, want sanitized file content", events[3])
	}
	if events[4].InputTokens != 20 || events[4].OutputTokens != 6 || !events[4].UsageAvailable {
		t.Fatalf("second usage event = %#v", events[4])
	}
	if events[5].Text != "The " || events[6].Text != "README says hello." {
		t.Fatalf("text events = %#v, want final text chunks", events[5:7])
	}
	if events[7].FinishReason != "stop" {
		t.Fatalf("run_finished finish reason = %q, want stop", events[7].FinishReason)
	}
}

func TestRunCompatibilityWrapperEmitsOnlyFinalText(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{{
		events:     []llm.StreamEvent{{Text: "final"}, {Text: " answer"}},
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "final answer"}, FinishReason: "stop"},
	}}}
	var output []string
	if err := Run(context.Background(), client, t.TempDir(), "answer directly", func(text string) error {
		output = append(output, text)
		return nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(output, []string{"final", " answer"}) {
		t.Fatalf("output = %#v, want final text chunks only", output)
	}
}

func TestRunWithRegistryUsesRegisteredTool(t *testing.T) {
	call := llm.ToolCall{ID: "call-fake", Type: "function", Name: "fake_tool", Arguments: `{}`}
	client := &scriptedClient{steps: []scriptedStep{
		{
			completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"},
		},
		{
			events:     []llm.StreamEvent{{Text: "fake answer"}},
			completion: llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "fake answer"}, FinishReason: "stop"},
		},
	}}
	fake := &registryTestTool{}
	registry, err := tool.NewRegistry(fake)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := RunEventsWithRegistry(context.Background(), client, t.TempDir(), "use fake", "", registry, func(Event) error {
		return nil
	}); err != nil {
		t.Fatalf("RunEventsWithRegistry() error = %v", err)
	}
	if !fake.called {
		t.Fatal("fake tool was not executed")
	}
	if len(client.requests) != 2 || client.requests[0].Tools[0].Function == nil {
		t.Fatalf("requests = %#v, want registered tool schema and two turns", client.requests)
	}
	if strings.Contains(client.requests[0].Messages[0].Content, "read_file") {
		t.Fatalf("custom registry system instruction hardcodes read_file: %q", client.requests[0].Messages[0].Content)
	}
	if got := client.requests[1].Messages[2].Content; got != "fake result" {
		t.Fatalf("tool result = %q, want fake result", got)
	}
}

type registryTestTool struct {
	called bool
}

type cancelingTool struct {
	cancel context.CancelFunc
}

func (t cancelingTool) Name() string { return "cancel_tool" }

func (t cancelingTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Type: "function", Function: []byte(`{"name":"cancel_tool"}`)}
}

func (t cancelingTool) Execute(ctx context.Context, _, _ string) (string, error) {
	t.cancel()
	return "", ctx.Err()
}

func (t *registryTestTool) Name() string {
	return "fake_tool"
}

func (t *registryTestTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Type: "function", Function: []byte(`{"name":"fake_tool"}`)}
}

func (t *registryTestTool) Execute(context.Context, string, string) (string, error) {
	t.called = true
	return "fake result", nil
}
