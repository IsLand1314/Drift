package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitee.com/island0920/drift/internal/llm"
)

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
	if !reflect.DeepEqual(request.Messages, []llm.Message{{Role: "user", Content: "answer directly"}}) {
		t.Fatalf("first request messages = %#v", request.Messages)
	}
	if len(request.Tools) != 1 {
		t.Fatalf("first request tools = %d, want 1", len(request.Tools))
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
	if len(second.Tools) != 0 {
		t.Fatalf("second request tools = %#v, want empty", second.Tools)
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
				{completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"}},
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

func TestRunRejectsUnsupportedToolCallStates(t *testing.T) {
	tests := []struct {
		name        string
		calls       []llm.ToolCall
		finish      string
		wantError   string
		wantRequest int
	}{
		{
			name: "multiple calls",
			calls: []llm.ToolCall{
				{ID: "one", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`},
				{ID: "two", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`},
			},
			finish: "tool_calls", wantError: "exactly one", wantRequest: 1,
		},
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

func TestRunRejectsSecondTurnToolCalls(t *testing.T) {
	call := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "partial final"}}, completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "tool_calls"}},
	}}
	var output []string
	err := Run(context.Background(), client, t.TempDir(), "test", func(text string) error {
		output = append(output, text)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected second completion") {
		t.Fatalf("Run() error = %v, want unexpected second completion", err)
	}
	if !reflect.DeepEqual(output, []string{"partial final"}) {
		t.Fatalf("output = %q, want streamed second-turn text", output)
	}
}

func TestRunReturnsOutputCallbackFailure(t *testing.T) {
	want := errors.New("output unavailable")
	client := &scriptedClient{steps: []scriptedStep{{
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"},
	}}}
	if err := Run(context.Background(), client, t.TempDir(), "test", func(string) error { return want }); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
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

func TestRunPreservesReasoningContentForSecondRequest(t *testing.T) {
	call := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ReasoningContent: "I need the readme", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"}},
	}}
	if err := Run(context.Background(), client, t.TempDir(), "test", func(string) error { return nil }); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := client.requests[1].Messages[1].ReasoningContent; got != "I need the readme" {
		t.Fatalf("second request reasoning content = %q, want preserved value", got)
	}
}
