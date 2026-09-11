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

const wantNativeToolSystemInstruction = "Drift is read-only. Only use the supplied native read_file tool. run_command, shell, and exec are unavailable. Never emit XML, DSML, or pseudo-tool syntax."

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
	if len(request.Tools) != 1 {
		t.Fatalf("first request tools = %d, want 1", len(request.Tools))
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

func TestRunUsesNativeToolSystemInstruction(t *testing.T) {
	client := &scriptedClient{steps: []scriptedStep{{
		completion: llm.Completion{
			Assistant:    llm.Message{Role: "assistant"},
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
	for _, required := range []string{"read_file", "run_command", "DSML", "pseudo-tool"} {
		if !strings.Contains(system.Content, required) {
			t.Fatalf("system instruction = %q, want %q", system.Content, required)
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
	if len(second.Tools) != 0 {
		t.Fatalf("second request tools = %#v, want none", second.Tools)
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

func TestRunRejectsTooManyToolCalls(t *testing.T) {
	calls := make([]llm.ToolCall, 5)
	for i := range calls {
		calls[i] = llm.ToolCall{ID: string(rune('a' + i)), Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	}
	client := &scriptedClient{steps: []scriptedStep{{
		completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"},
	}}}
	err := Run(context.Background(), client, t.TempDir(), "read files", func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "最多读取 4 个文件") {
		t.Fatalf("Run() error = %v, want maximum files error", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
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
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"}},
	}}
	if err := Run(context.Background(), client, root, "read the files", func(string) error { return nil }); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
	second := client.requests[1]
	if len(second.Messages) != 6 {
		t.Fatalf("second request messages = %d, want 6", len(second.Messages))
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

func TestRunSanitizesDotEnvReadFailureForSecondTurn(t *testing.T) {
	root := t.TempDir()
	const secret = "synthetic-openai-key-must-not-reach-model"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("OPENAI_API_KEY="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "call-env", Type: "function", Name: "read_file", Arguments: `{"path":".env"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"}},
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

func TestRunRejectsSecondStopWithToolCall(t *testing.T) {
	call := llm.ToolCall{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}
	client := &scriptedClient{steps: []scriptedStep{
		{completion: llm.Completion{Assistant: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}},
		{events: []llm.StreamEvent{{Text: "partial final"}}, completion: llm.Completion{
			Assistant:    llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
			FinishReason: "stop",
		}},
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
		t.Fatalf("output = %q, want streamed second-turn text preserved", output)
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
