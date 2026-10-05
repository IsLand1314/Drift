package session

import (
	"testing"

	"github.com/IsLand1314/Drift/internal/llm"
)

func TestNormalizeToolPairingDropsOrphansAndFillsInterruptedCalls(t *testing.T) {
	got := NormalizeToolPairing([]llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a", Name: "ReadFile"}, {ID: "b", Name: "Grep"}}},
		{Role: "tool", ToolCallID: "a", Content: "ok"},
		{Role: "tool", ToolCallID: "a", Content: "duplicate"},
		{Role: "tool", ToolCallID: "orphan", Content: "drop"},
	})
	if len(got) != 3 || got[1].ToolCallID != "a" || got[2].ToolCallID != "b" {
		t.Fatalf("messages = %+v", got)
	}
	if got[2].Content != "tool execution interrupted" {
		t.Fatalf("missing result = %+v", got[2])
	}
}

func TestNormalizeToolPairingReordersResultsAndDropsShells(t *testing.T) {
	input := []llm.Message{
		{},
		{Role: "tool", ToolCallID: "b", Content: "B"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a", Name: "ReadFile"}, {ID: "b", Name: "Grep"}}},
		{Role: "user", Content: "next"},
		{Role: "tool", ToolCallID: "a", Content: "A"},
		{Role: "tool", ToolCallID: "a", Content: "duplicate"},
		{Role: "tool", ToolCallID: "orphan", Content: "orphan"},
	}
	original := cloneMessages(input)
	got := NormalizeToolPairing(input)
	if len(got) != 4 || got[0].Role != "assistant" || got[1].ToolCallID != "a" || got[2].ToolCallID != "b" || got[3].Content != "next" {
		t.Fatalf("messages = %+v", got)
	}
	if got[1].Content != "A" || got[2].Content != "B" {
		t.Fatalf("results not reordered next to owner: %+v", got)
	}
	if !sameMessages(input, original) {
		t.Fatalf("input was mutated: before=%+v after=%+v", original, input)
	}
}

func TestNormalizeToolPairingDeepCopiesMessagesAndDuplicateUses(t *testing.T) {
	input := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "same", Name: "ReadFile"}}}, {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "same", Name: "ReadFile"}}}}
	got := NormalizeToolPairing(input)
	if len(got) != 4 || got[1].Content != "tool execution interrupted" || got[3].Content != "tool execution interrupted" {
		t.Fatalf("duplicate tool_use pairing = %+v", got)
	}
	got[0].ToolCalls[0].Name = "changed"
	if input[0].ToolCalls[0].Name != "ReadFile" {
		t.Fatal("normalized result aliases input tool calls")
	}
}

func sameMessages(a, b []llm.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content || a[i].ToolCallID != b[i].ToolCallID || a[i].ReasoningContent != b[i].ReasoningContent || len(a[i].ToolCalls) != len(b[i].ToolCalls) {
			return false
		}
		for j := range a[i].ToolCalls {
			if a[i].ToolCalls[j] != b[i].ToolCalls[j] {
				return false
			}
		}
	}
	return true
}
