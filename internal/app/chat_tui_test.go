package app

import (
	"context"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestTTYChatViewKeepsTranscriptAndSingleFooter(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Model: "test-model"}, nil)
	m.width, m.height = 80, 20
	m.lines = []string{"❯ first request", "● first answer", "Done - 0.1s", "❯ second request", "● second answer", "Done - 0.2s"}

	view := m.View()
	if strings.Count(view, "first request") != 1 || strings.Count(view, "second request") != 1 {
		t.Fatalf("transcript was not preserved: %q", view)
	}
	if strings.Count(view, "Enter 发送 · Ctrl+C 取消") != 1 {
		t.Fatalf("expected one persistent footer: %q", view)
	}
	if strings.Count(view, "test-model") != 1 {
		t.Fatalf("expected one model label: %q", view)
	}
}

func TestTTYChatToolResultDoesNotRenderProbeFailure(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{}, nil)
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ToolName: "read_file", Path: "missing.txt", ErrorSummary: "tool execution failed"})
	if len(m.lines) != 0 {
		t.Fatalf("probe failure should stay out of transcript: %#v", m.lines)
	}
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ToolName: "read_file", Path: "ok.txt", Result: "hello", AfterBytes: 5})
	if len(m.lines) != 1 || !strings.Contains(m.lines[0], "ok.txt") {
		t.Fatalf("successful tool result missing: %#v", m.lines)
	}
}

func TestTTYChatTextEventsSurviveBubbleTeaModelCopies(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{}, nil)
	model, _ := m.Update(tuiAgentEvent{event: agent.Event{Type: agent.EventTextDelta, Text: "first"}})
	model, _ = model.Update(tuiAgentEvent{event: agent.Event{Type: agent.EventTextDelta, Text: " second"}})
	modelValue := model.(ttyChatModel)
	got := modelValue.stream
	if got != "first second" {
		t.Fatalf("stream = %q, want %q", got, "first second")
	}
}

func TestTTYChatLayoutUsesFullWidthRulesAndWrappedInput(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Model: "deepseek-v4-flash"}, nil)
	m.width, m.height = 24, 12
	m.textarea.SetWidth(22)
	m.textarea.SetValue(strings.Repeat("x", 30))
	resizeTTYTextarea(&m)
	if m.textarea.Height() < 2 {
		t.Fatalf("wrapped input height = %d, want at least 2", m.textarea.Height())
	}
	m.lines = []string{"❯ /resume"}
	view := m.View()
	if !strings.Contains(view, strings.Repeat("─", 24)) {
		t.Fatalf("separator did not span terminal width: %q", view)
	}
	if !strings.Contains(view, "/resume") {
		t.Fatalf("submitted command missing from transcript: %q", view)
	}
}
