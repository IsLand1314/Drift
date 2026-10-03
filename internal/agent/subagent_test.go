package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

type childClient struct{ wait bool }

func (c childClient) Stream(ctx context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	if c.wait {
		<-ctx.Done()
		return llm.Completion{}, ctx.Err()
	}
	if err := emit(llm.StreamEvent{Text: "child complete"}); err != nil {
		return llm.Completion{}, err
	}
	return llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "child complete"}, FinishReason: "stop"}, nil
}

func TestChildManagerCompletesAndRejectsConcurrentStart(t *testing.T) {
	manager := &ChildManager{}
	root := t.TempDir()
	var events []Event
	first, err := manager.Start(context.Background(), childClient{}, "task-1", root, "work", 0, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(context.Background(), childClient{wait: true}, "task-2", root, "work", 0, nil); err == nil {
		t.Fatal("second child accepted")
	}
	result, err := first.Wait(context.Background())
	if err != nil || result.State != ChildCompleted || result.Output != "child complete" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var childEvents []Event
	for _, event := range events {
		if event.TaskID == "task-1" {
			childEvents = append(childEvents, event)
		}
	}
	if len(childEvents) != 2 || childEvents[0].ExecutionStatus != string(ChildRunning) || childEvents[1].ChildState != string(ChildCompleted) {
		t.Fatalf("lifecycle events=%+v", childEvents)
	}
	if _, err := manager.Start(context.Background(), childClient{}, "task-2", root, "work", 0, nil); err != nil {
		t.Fatalf("manager not released: %v", err)
	}
}

func TestChildManagerCancellationAndTimeout(t *testing.T) {
	manager := &ChildManager{}
	root := t.TempDir()
	cancelCtx, cancel := context.WithCancel(context.Background())
	handle, err := manager.Start(cancelCtx, childClient{wait: true}, "task-cancel", root, "work", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	result, _ := handle.Wait(context.Background())
	if result.State != ChildCancelled || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancel result=%+v", result)
	}
	handle, err = manager.Start(context.Background(), childClient{wait: true}, "task-timeout", root, "work", 10*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, _ = handle.Wait(context.Background())
	if result.State != ChildTimeout || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("timeout result=%+v", result)
	}
}
