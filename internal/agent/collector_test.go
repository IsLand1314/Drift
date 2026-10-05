package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/IsLand1314/Drift/internal/llm"
)

func TestCollectorBuildsTurnResultFromEvents(t *testing.T) {
	c := NewCollector()
	for _, event := range []llm.Event{
		llm.TextDelta{Text: "inspect"},
		llm.ThinkingComplete{Thinking: "reason", Signature: "sig", EncryptedContent: "encrypted"},
		llm.ToolCallStart{Index: 0, ID: "call-1", Name: "ReadFile"},
		llm.ToolCallDelta{Index: 0, Arguments: `{"path":"README.md"}`},
		llm.ToolCallComplete{Index: 0, ID: "call-1", Name: "ReadFile", Arguments: `{"path":"README.md"}`},
		llm.StreamEnd{FinishReason: "tool_calls", Usage: &llm.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7}},
	} {
		if err := c.Accept(event); err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.Result()
	if err != nil {
		t.Fatal(err)
	}
	if got.Assistant.Content != "inspect" || len(got.Assistant.ToolCalls) != 1 {
		t.Fatalf("assistant = %+v", got.Assistant)
	}
	if got.Assistant.ReasoningSignature != "sig" || got.Assistant.EncryptedReasoning != "encrypted" {
		t.Fatalf("reasoning metadata = %#v", got.Assistant)
	}
	if got.FinishReason != "tool_calls" || got.Usage == nil || got.Usage.TotalTokens != 7 {
		t.Fatalf("result = %+v", got)
	}
}

type terminalClient struct {
	err error
}

func (c terminalClient) StreamEvents(_ context.Context, _ llm.Request, emit func(llm.Event) error) error {
	_ = emit(llm.StreamEnd{Status: llm.StreamFailed, Error: llm.StreamErrorFrom(c.err, false)})
	return c.err
}

func TestCollectTurnSynthesizesFailedTerminalEvent(t *testing.T) {
	var got []Event
	err := errors.New("connection reset")
	_, gotErr := collectTurn(context.Background(), terminalClient{err: err}, llm.Request{}, func(event Event) error {
		got = append(got, event)
		return nil
	})
	if !errors.Is(gotErr, err) {
		t.Fatalf("collectTurn() error = %v, want %v", gotErr, err)
	}
	if len(got) != 1 || got[0].Type != EventStreamEnded || got[0].StreamStatus != string(llm.StreamFailed) {
		t.Fatalf("agent events = %#v, want one failed terminal event", got)
	}
	terminal := llm.StreamEnd{Status: llm.StreamFailed, Error: llm.StreamErrorFrom(err, false)}
	if terminal.Status != llm.StreamFailed || terminal.Error == nil || terminal.Error.Kind != llm.StreamErrorUnknown {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func TestCollectorRejectsEventsAfterTerminal(t *testing.T) {
	c := NewCollector()
	if err := c.Accept(llm.StreamEnd{Status: llm.StreamCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(llm.TextDelta{Text: "late"}); err == nil {
		t.Fatal("Accept() after StreamEnd succeeded, want protocol error")
	}
}

func TestCollectorRejectsIncompleteToolCall(t *testing.T) {
	c := NewCollector()
	if err := c.Accept(llm.ToolCallComplete{Index: 0, ID: "call-1", Name: "ReadFile", Arguments: ""}); err == nil {
		t.Fatal("incomplete tool call was accepted")
	}
	if err := c.Accept(llm.ToolCallComplete{Index: 0, ID: "call-1", Name: "ReadFile", Arguments: `{`}); err == nil {
		t.Fatal("invalid tool arguments were accepted")
	}
}

func TestCollectorRejectsDuplicateToolCallCompletion(t *testing.T) {
	c := NewCollector()
	call := llm.ToolCallComplete{Index: 0, ID: "call-1", Name: "ReadFile", Arguments: `{}`}
	if err := c.Accept(call); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(call); err == nil {
		t.Fatal("duplicate tool call completion was accepted")
	}
}

func TestCollectorBindsToolFragmentsByIDAndIndex(t *testing.T) {
	c := NewCollector()
	if err := c.Accept(llm.ToolCallStart{Index: 0, ID: "call-a", Name: "ReadFile"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(llm.ToolCallDelta{Index: 7, ID: "call-a", Arguments: `{}`}); err == nil {
		t.Fatal("same tool call changing index was accepted")
	}

	c = NewCollector()
	for _, event := range []llm.Event{
		llm.ToolCallStart{Index: 0, ID: "call-a", Name: "ReadFile"},
		llm.ToolCallStart{Index: 1, ID: "call-b", Name: "Grep"},
		llm.ToolCallDelta{Index: -1, ID: "call-b", Arguments: `{"q":"b"}`},
		llm.ToolCallDelta{Index: -1, ID: "call-a", Arguments: `{"path":"a"}`},
		llm.ToolCallComplete{Index: 1, ID: "call-b", Name: "Grep", Arguments: `{"q":"b"}`},
		llm.ToolCallComplete{Index: 0, ID: "call-a", Name: "ReadFile", Arguments: `{"path":"a"}`},
		llm.StreamEnd{Status: llm.StreamCompleted},
	} {
		if err := c.Accept(event); err != nil {
			t.Fatal(err)
		}
	}
	result, err := c.Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assistant.ToolCalls) != 2 || result.Assistant.ToolCalls[0].ID != "call-a" || result.Assistant.ToolCalls[1].ID != "call-b" {
		t.Fatalf("tool order = %#v", result.Assistant.ToolCalls)
	}
}

func TestCollectorDoesNotProduceTurnResultForFailedStream(t *testing.T) {
	c := NewCollector()
	if err := c.Accept(llm.TextDelta{Text: "partial"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Accept(llm.StreamEnd{Status: llm.StreamFailed}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Result(); err == nil {
		t.Fatal("failed stream produced a normal TurnResult")
	}
}
