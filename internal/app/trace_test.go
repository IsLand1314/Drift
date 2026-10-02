package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestTraceSinkWritesSafeEventSummaries(t *testing.T) {
	var output bytes.Buffer
	sink := newTraceSink(&output)
	for _, event := range []agent.Event{
		{Type: agent.EventRunStarted},
		{Type: agent.EventToolCall, ToolName: "ReadFile", Arguments: `{"path":"README.md","api_key":"secret"}`},
		{Type: agent.EventToolResult, ToolName: "ReadFile", Result: "file contents"},
		{Type: agent.EventError, Error: "模型连接失败", Stage: "provider_transport"},
		{Type: agent.EventRunFinished},
		{Type: agent.EventCompactionFinished, Text: "secret summary", BeforeBytes: 200, AfterBytes: 80, KeptMessages: 4},
	} {
		if err := sink(event); err != nil {
			t.Fatal(err)
		}
	}
	text := output.String()
	if !strings.Contains(text, "tool_call ReadFile") || !strings.Contains(text, "tool_result ReadFile bytes=13") || !strings.Contains(text, "error stage=provider_transport") {
		t.Fatalf("trace = %q", text)
	}
	if strings.Contains(text, "file contents") || strings.Contains(text, "secret") || strings.Contains(text, "README.md") || strings.Contains(text, "summary") {
		t.Fatalf("trace leaked sensitive event data: %q", text)
	}
	if !strings.Contains(text, "compaction_finished before_bytes=200 after_bytes=80 kept_messages=4") {
		t.Fatalf("trace missed compaction counters: %q", text)
	}
}
