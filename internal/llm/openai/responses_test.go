package openai

import (
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/llm/transport"
)

func TestResponsesRequestUsesResponsesToolShape(t *testing.T) {
	request := responsesRequest(llm.Request{
		Model:    "gpt-test",
		Messages: []llm.Message{{Role: "user", Content: "inspect"}},
		Tools:    []llm.ToolDefinition{{Type: "function", Function: []byte(`{"name":"ReadFile","description":"read","parameters":{"type":"object"}}`)}},
	})
	if request.Model != "gpt-test" || len(request.Input) != 1 || len(request.Tools) != 1 {
		t.Fatalf("request = %#v", request)
	}
	if request.Tools[0].Type != "function" || request.Tools[0].Name != "ReadFile" {
		t.Fatalf("tool = %#v", request.Tools[0])
	}
}

func TestResponsesRequestReplaysReasoningItem(t *testing.T) {
	request := responsesRequest(llm.Request{
		Model: "gpt-test",
		Messages: []llm.Message{
			{Role: "assistant", Content: "answer", ReasoningContent: "check", EncryptedReasoning: "enc"},
		},
	})
	if len(request.Input) != 2 {
		t.Fatalf("input = %#v", request.Input)
	}
	if request.Input[0]["type"] != "reasoning" || request.Input[0]["encrypted_content"] != "enc" {
		t.Fatalf("reasoning item = %#v", request.Input[0])
	}
	if request.Input[1]["type"] != "message" {
		t.Fatalf("assistant item = %#v", request.Input[1])
	}
}

func TestReadResponsesEventsMapsTextAndToolArguments(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`, "",
		`data: {"type":"response.function_call_arguments.delta","item_id":"item-1","call_id":"call-1","name":"ReadFile","delta":"{\"path\":\"README.md\"}"}`, "",
		`data: {"type":"response.function_call_arguments.done","item_id":"item-1","call_id":"call-1","name":"ReadFile","arguments":"{\"path\":\"README.md\"}"}`, "",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`, "",
	}, "\n")
	var events []llm.Event
	if err := readResponsesEvents(transport.NewSSESanitizer(strings.NewReader(input)), func(event llm.Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %#v", events)
	}
	if text, ok := events[0].(llm.TextDelta); !ok || text.Text != "hello" {
		t.Fatalf("text event = %#v", events[0])
	}
	call, ok := events[2].(llm.ToolCallComplete)
	if !ok || call.ID != "item-1" || call.Name != "ReadFile" || call.Arguments == "" {
		t.Fatalf("tool event = %#v", events[2])
	}
	end, ok := events[3].(llm.StreamEnd)
	if !ok || end.Status != llm.StreamCompleted || end.Usage == nil || end.Usage.TotalTokens != 5 {
		t.Fatalf("end event = %#v", events[3])
	}
}

func TestReadResponsesEventsPreservesReasoningMetadata(t *testing.T) {
	input := "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"check\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc\",\"summary\":[{\"text\":\"check\"}]}}\n\n"
	var events []llm.Event
	if err := readResponsesEvents(transport.NewSSESanitizer(strings.NewReader(input)), func(event llm.Event) error {
		events = append(events, event)
		return nil
	}); err == nil {
		t.Fatal("missing terminal event was accepted")
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	complete, ok := events[1].(llm.ThinkingComplete)
	if !ok || complete.Thinking != "check" || complete.EncryptedContent != "enc" {
		t.Fatalf("thinking event = %#v", events[1])
	}
}
