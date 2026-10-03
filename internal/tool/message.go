package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/message"
)

type MessageRegistry interface {
	Registry
	MessageBus() *message.Bus
	SetMessageBus(*message.Bus)
}

type agentMessageSendTool struct{ bus *message.Bus }
type agentMessageListTool struct{ bus *message.Bus }
type agentSummaryTool struct{ bus *message.Bus }

func (agentMessageSendTool) Name() string { return "AgentMessageSend" }
func (agentMessageListTool) Name() string { return "AgentMessageList" }
func (agentSummaryTool) Name() string     { return "AgentSummary" }

func (agentMessageSendTool) Definition() llm.ToolDefinition {
	return controlDefinition("AgentMessageSend", "Send progress, issue, or result to another Agent.", map[string]any{
		"from": map[string]any{"type": "string"}, "to": map[string]any{"type": "string"}, "task_id": map[string]any{"type": "string"},
		"kind": map[string]any{"type": "string", "enum": []string{"progress", "issue", "result"}}, "text": map[string]any{"type": "string"},
	}, []string{"from", "to", "task_id", "kind", "text"})
}

func (agentMessageListTool) Definition() llm.ToolDefinition {
	return controlDefinition("AgentMessageList", "List messages for a task in chronological order.", map[string]any{"task_id": map[string]any{"type": "string"}}, []string{"task_id"})
}

func (agentSummaryTool) Definition() llm.ToolDefinition {
	return controlDefinition("AgentSummary", "Summarize deduplicated Agent messages for a task.", map[string]any{"task_id": map[string]any{"type": "string"}}, []string{"task_id"})
}

func (t agentMessageSendTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		From   string `json:"from"`
		To     string `json:"to"`
		TaskID string `json:"task_id"`
		Kind   string `json:"kind"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("decode AgentMessageSend arguments: %w", err)
	}
	item, err := t.bus.Send(args.From, args.To, args.TaskID, args.Kind, args.Text)
	if err != nil {
		return "", err
	}
	return "message sent: " + item.ID, nil
}

func (t agentMessageListTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("decode AgentMessageList arguments: %w", err)
	}
	items := t.bus.List(strings.TrimSpace(args.TaskID))
	if len(items) == 0 {
		return "AgentMessageList: no messages", nil
	}
	var out strings.Builder
	for i, item := range items {
		if i > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "%s %s -> %s [%s] (%s): %s", item.Time.Format("2006-01-02T15:04:05Z07:00"), item.From, item.To, item.TaskID, item.Kind, item.Text)
	}
	return out.String(), nil
}

func (t agentSummaryTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("decode AgentSummary arguments: %w", err)
	}
	return t.bus.Summary(strings.TrimSpace(args.TaskID)), nil
}
