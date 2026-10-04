package coordinator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/tool"
)

type fakeHandle struct {
	result tool.TaskExecutionResult
	done   chan struct{}
	once   sync.Once
}

func newFakeHandle(result tool.TaskExecutionResult) *fakeHandle {
	return &fakeHandle{result: result, done: make(chan struct{})}
}

func (h *fakeHandle) Cancel() { h.once.Do(func() { close(h.done) }) }
func (h *fakeHandle) Wait(ctx context.Context) (tool.TaskExecutionResult, error) {
	select {
	case <-h.done:
		return h.result, nil
	case <-ctx.Done():
		return tool.TaskExecutionResult{}, ctx.Err()
	}
}

func TestRunExecutesReadyTaskAndReturnsCompletedState(t *testing.T) {
	root := t.TempDir()
	runner := func(context.Context, tool.TaskState, string, time.Duration) (tool.TaskHandle, error) {
		handle := newFakeHandle(tool.TaskExecutionResult{State: "completed", Output: "ok"})
		close(handle.done)
		return handle, nil
	}
	c := New([]tool.TaskState{{ID: "task-1", Subject: "one", Description: "one", Status: "pending"}}, runner, nil, Options{MaxConcurrency: 1})
	got, err := c.Run(context.Background(), root)
	if err != nil || got[0].Status != "completed" || got[0].Result != "ok" {
		t.Fatalf("tasks=%+v err=%v", got, err)
	}
}

func TestRunBlocksTaskWithFailedDependency(t *testing.T) {
	c := New([]tool.TaskState{{ID: "task-1", Status: "failed"}, {ID: "task-2", Status: "pending", DependsOn: []string{"task-1"}}}, nil, nil, Options{MaxConcurrency: 1})
	got, err := c.Run(context.Background(), t.TempDir())
	if err == nil || got[1].Status != "blocked" || got[1].Error != "dependency task-1 failed" {
		t.Fatalf("tasks=%+v err=%v", got, err)
	}
}

func TestRunHonorsConcurrencyLimit(t *testing.T) {
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	runner := func(context.Context, tool.TaskState, string, time.Duration) (tool.TaskHandle, error) {
		started <- struct{}{}
		handle := newFakeHandle(tool.TaskExecutionResult{State: "completed"})
		go func() { <-release; close(handle.done) }()
		return handle, nil
	}
	tasks := []tool.TaskState{{ID: "task-1", Status: "pending"}, {ID: "task-2", Status: "pending"}, {ID: "task-3", Status: "pending"}}
	c := New(tasks, runner, nil, Options{MaxConcurrency: 2})
	done := make(chan struct{})
	go func() { _, _ = c.Run(context.Background(), t.TempDir()); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("expected two tasks to start")
		}
	}
	select {
	case <-started:
		t.Fatal("third task exceeded concurrency limit")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("coordinator did not finish")
	}
}

func TestRunPassesWorkspaceRootToMerger(t *testing.T) {
	root := t.TempDir()
	var gotRoot string
	runner := func(context.Context, tool.TaskState, string, time.Duration) (tool.TaskHandle, error) {
		handle := newFakeHandle(tool.TaskExecutionResult{State: "completed"})
		close(handle.done)
		return handle, nil
	}
	merger := func(_ context.Context, root string, _ tool.TaskState) (tool.TaskMergeResult, error) {
		gotRoot = root
		return tool.TaskMergeResult{Status: "merged", AfterHEAD: "abc"}, nil
	}
	task := tool.TaskState{ID: "task-1", Status: "pending", Worktree: ".worktrees/agent-1"}
	c := New([]tool.TaskState{task}, runner, merger, Options{MaxConcurrency: 1})
	got, err := c.Run(context.Background(), root)
	if err != nil || got[0].MergeStatus != "merged" || got[0].MergeCommit != "abc" || gotRoot != root {
		t.Fatalf("tasks=%+v root=%q err=%v", got, gotRoot, err)
	}
}

func TestRunReturnsErrorForMergeConflict(t *testing.T) {
	runner := func(context.Context, tool.TaskState, string, time.Duration) (tool.TaskHandle, error) {
		handle := newFakeHandle(tool.TaskExecutionResult{State: "completed"})
		close(handle.done)
		return handle, nil
	}
	merger := func(context.Context, string, tool.TaskState) (tool.TaskMergeResult, error) {
		return tool.TaskMergeResult{Status: "conflict", Conflicts: []string{"file.go"}}, nil
	}
	c := New([]tool.TaskState{{ID: "task-1", Status: "pending", Worktree: ".worktrees/agent-1"}}, runner, merger, Options{})
	got, err := c.Run(context.Background(), t.TempDir())
	if err == nil || got[0].MergeStatus != "conflict" || len(got[0].MergeConflicts) != 1 {
		t.Fatalf("tasks=%+v err=%v", got, err)
	}
}

func TestRunRetriesTemporaryFailure(t *testing.T) {
	attempts := 0
	runner := func(context.Context, tool.TaskState, string, time.Duration) (tool.TaskHandle, error) {
		attempts++
		result := tool.TaskExecutionResult{State: "failed", Err: context.DeadlineExceeded}
		if attempts == 2 {
			result = tool.TaskExecutionResult{State: "completed", Output: "ok"}
		}
		handle := newFakeHandle(result)
		close(handle.done)
		return handle, nil
	}
	c := New([]tool.TaskState{{ID: "task-1", Status: "pending"}}, runner, nil, Options{MaxRetries: 1})
	got, err := c.Run(context.Background(), t.TempDir())
	if err != nil || attempts != 2 || got[0].Status != "completed" {
		t.Fatalf("attempts=%d tasks=%+v err=%v", attempts, got, err)
	}
}

func TestCancelPreservesCancelledState(t *testing.T) {
	started := make(chan struct{})
	runner := func(context.Context, tool.TaskState, string, time.Duration) (tool.TaskHandle, error) {
		handle := newFakeHandle(tool.TaskExecutionResult{State: "completed"})
		close(started)
		return handle, nil
	}
	c := New([]tool.TaskState{{ID: "task-1", Status: "pending"}}, runner, nil, Options{})
	done := make(chan []tool.TaskState, 1)
	go func() { states, _ := c.Run(context.Background(), t.TempDir()); done <- states }()
	<-started
	c.Cancel()
	select {
	case states := <-done:
		if states[0].Status != "cancelled" {
			t.Fatalf("tasks=%+v", states)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop coordinator")
	}
}

func TestRunWithTimeoutPassesTaskBudgetToRunner(t *testing.T) {
	var got time.Duration
	runner := func(_ context.Context, _ tool.TaskState, _ string, timeout time.Duration) (tool.TaskHandle, error) {
		got = timeout
		handle := newFakeHandle(tool.TaskExecutionResult{State: "completed"})
		close(handle.done)
		return handle, nil
	}
	c := New([]tool.TaskState{{ID: "task-1", Status: "pending"}}, runner, nil, Options{})
	if _, err := c.RunWithTimeout(context.Background(), t.TempDir(), 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got != 1500*time.Millisecond {
		t.Fatalf("timeout=%v", got)
	}
}

func TestRestoreConvertsInterruptedTasksToPending(t *testing.T) {
	c := New(nil, nil, nil, Options{})
	if err := c.Restore([]tool.TaskState{{ID: "task-1", Status: "running"}, {ID: "task-2", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	got := c.Status()
	if got[0].Status != "pending" || got[1].Status != "completed" {
		t.Fatalf("tasks=%+v", got)
	}
}
