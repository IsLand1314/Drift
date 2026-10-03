package app

import (
	"context"
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

type adapterChildClient struct{}

func (adapterChildClient) Stream(_ context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	if err := emit(llm.StreamEvent{Text: "child result"}); err != nil {
		return llm.Completion{}, err
	}
	return llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "child result"}, FinishReason: "stop"}, nil
}

func TestChildTaskRunnerReturnsAuditedChildResult(t *testing.T) {
	root := t.TempDir()
	manager := &agent.ChildManager{}
	var events []agent.Event
	runner := childTaskRunner(manager, adapterChildClient{}, root, func(event agent.Event) error {
		events = append(events, event)
		return nil
	})
	handle, err := runner(context.Background(), tool.TaskState{ID: "task-1", Worktree: "."}, "inspect", 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handle.Wait(context.Background())
	if err != nil || result.State != "completed" || result.Output != "child result" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	started, finished := false, false
	for _, event := range events {
		if event.TaskID == "task-1" && event.ExecutionStatus == "running" {
			started = true
		}
		if event.TaskID == "task-1" && event.ChildState == "completed" {
			finished = true
		}
	}
	if !started || !finished {
		t.Fatalf("events=%+v", events)
	}
}
