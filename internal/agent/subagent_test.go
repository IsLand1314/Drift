package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/message"
)

type childClient struct{ wait, fail bool }

func (c childClient) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return collectLegacyEvents(ctx, c, request, emit)
}

func (c childClient) Stream(ctx context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (testCompletion, error) {
	if c.wait {
		<-ctx.Done()
		return testCompletion{}, ctx.Err()
	}
	if c.fail {
		return testCompletion{}, errors.New("child failed")
	}
	if err := emit(llm.StreamEvent{Text: "child complete"}); err != nil {
		return testCompletion{}, err
	}
	return testCompletion{Assistant: llm.Message{Role: "assistant", Content: "child complete"}, FinishReason: "stop"}, nil
}

type parallelChildClient struct {
	started chan<- struct{}
	release <-chan struct{}
	fail    bool
}

func (c parallelChildClient) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return collectLegacyEvents(ctx, c, request, emit)
}

func (c parallelChildClient) Stream(ctx context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (testCompletion, error) {
	if c.started != nil {
		c.started <- struct{}{}
	}
	if c.fail {
		return testCompletion{}, errors.New("parallel child failed")
	}
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return testCompletion{}, ctx.Err()
		}
	}
	if err := emit(llm.StreamEvent{Text: "parallel child complete"}); err != nil {
		return testCompletion{}, err
	}
	return testCompletion{Assistant: llm.Message{Role: "assistant", Content: "parallel child complete"}, FinishReason: "stop"}, nil
}

func TestChildManagerRunsUpToLimitInParallelAndRejectsOverflow(t *testing.T) {
	manager := NewChildManager(2)
	root := t.TempDir()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	client := parallelChildClient{started: started, release: release}
	first, err := manager.Start(context.Background(), client, "task-1", root, "one", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Start(context.Background(), client, "task-2", root, "two", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first child did not start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second child did not start in parallel")
	}
	if _, err := manager.Start(context.Background(), client, "task-3", root, "three", 0, nil); err == nil {
		t.Fatal("child over concurrency limit accepted")
	}
	close(release)
	for _, handle := range []*ChildHandle{first, second} {
		result, err := handle.Wait(context.Background())
		if err != nil || result.State != ChildCompleted {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestChildManagerPublishesProgressAndResultMessages(t *testing.T) {
	manager := NewChildManager(1)
	bus := message.NewBus()
	manager.SetMessageBus(bus)
	root := t.TempDir()
	handle, err := manager.Start(context.Background(), childClient{}, "task-1", root, "work", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	items := bus.List("task-1")
	if len(items) != 2 || items[0].Kind != "progress" || items[1].Kind != "result" || items[0].From != "child-task-1" || items[0].To != "main" {
		t.Fatalf("messages=%+v", items)
	}
}

func TestChildManagerFailureDoesNotCancelOtherChildren(t *testing.T) {
	manager := NewChildManager(2)
	root := t.TempDir()
	release := make(chan struct{})
	failed, err := manager.Start(context.Background(), parallelChildClient{fail: true}, "task-fail", root, "fail", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	survivor, err := manager.Start(context.Background(), parallelChildClient{release: release}, "task-ok", root, "ok", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	failure, err := failed.Wait(context.Background())
	if err != nil || failure.State != ChildFailed {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
	close(release)
	result, err := survivor.Wait(context.Background())
	if err != nil || result.State != ChildCompleted {
		t.Fatalf("survivor=%+v err=%v", result, err)
	}
}

func TestChildManagerCancelsOneParallelChildWithoutStoppingAnother(t *testing.T) {
	manager := NewChildManager(2)
	root := t.TempDir()
	release := make(chan struct{})
	first, err := manager.Start(context.Background(), parallelChildClient{release: release}, "task-cancel", root, "cancel", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Start(context.Background(), parallelChildClient{release: release}, "task-keep", root, "keep", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Cancel("task-cancel"); err != nil {
		t.Fatal(err)
	}
	cancelled, err := first.Wait(context.Background())
	if err != nil || cancelled.State != ChildCancelled {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
	close(release)
	kept, err := second.Wait(context.Background())
	if err != nil || kept.State != ChildCompleted {
		t.Fatalf("kept=%+v err=%v", kept, err)
	}
}

func TestChildManagerReportsFailureAndMissingWorktree(t *testing.T) {
	manager := &ChildManager{}
	root := t.TempDir()
	handle, err := manager.Start(context.Background(), childClient{fail: true}, "task-fail", root, "work", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handle.Wait(context.Background())
	if err != nil || result.State != ChildFailed || result.Err == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := manager.Start(context.Background(), childClient{}, "task-missing", root+"-missing", "work", 0, nil); err == nil {
		t.Fatal("missing worktree accepted")
	}
	cleanup, err := manager.Start(context.Background(), childClient{}, "task-after-failure", root, "work", 0, nil)
	if err != nil {
		t.Fatalf("manager not reusable after failure: %v", err)
	}
	if _, err := cleanup.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
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
