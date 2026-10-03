package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/message"
)

func TestAgentMessageToolsSendListAndSummarize(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	root := t.TempDir()
	if _, err := search.Execute(context.Background(), root, `{"query":"message","load":["AgentMessageSend","AgentMessageList","AgentSummary"]}`); err != nil {
		t.Fatal(err)
	}
	send, _ := registry.Lookup("AgentMessageSend")
	if got, err := send.Execute(context.Background(), root, `{"from":"child-1","to":"main","task_id":"task-1","kind":"result","text":"done"}`); err != nil || !strings.Contains(got, "message sent") {
		t.Fatalf("send=%q err=%v", got, err)
	}
	list, _ := registry.Lookup("AgentMessageList")
	if got, err := list.Execute(context.Background(), root, `{"task_id":"task-1"}`); err != nil || !strings.Contains(got, "child-1 -> main") || !strings.Contains(got, "task-1") {
		t.Fatalf("list=%q err=%v", got, err)
	}
	summary, _ := registry.Lookup("AgentSummary")
	if got, err := summary.Execute(context.Background(), root, `{"task_id":"task-1"}`); err != nil || strings.Count(got, "done") != 1 {
		t.Fatalf("summary=%q err=%v", got, err)
	}
}

func TestMessageRegistryCanShareBusWithChildManager(t *testing.T) {
	registry := NewChatRegistry()
	bus := message.NewBus()
	registry.(MessageRegistry).SetMessageBus(bus)
	if registry.(MessageRegistry).MessageBus() != bus {
		t.Fatal("message bus was not replaced")
	}
}
