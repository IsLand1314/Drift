package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testTaskHandle struct {
	mu       sync.Mutex
	result   TaskExecutionResult
	done     chan struct{}
	canceled bool
}

func newTestTaskHandle() *testTaskHandle { return &testTaskHandle{done: make(chan struct{})} }

func (h *testTaskHandle) Cancel() {
	h.mu.Lock()
	if !h.canceled {
		h.canceled = true
		h.result = TaskExecutionResult{State: "cancelled"}
		close(h.done)
	}
	h.mu.Unlock()
}

func (h *testTaskHandle) complete(result TaskExecutionResult) {
	h.mu.Lock()
	if h.result.State == "" {
		h.result = result
		close(h.done)
	}
	h.mu.Unlock()
}

func (h *testTaskHandle) Wait(ctx context.Context) (TaskExecutionResult, error) {
	select {
	case <-h.done:
		h.mu.Lock()
		result := h.result
		h.mu.Unlock()
		return result, nil
	case <-ctx.Done():
		return TaskExecutionResult{}, ctx.Err()
	}
}

func waitForTaskStatus(t *testing.T, registry Registry, status string) string {
	t.Helper()
	tool, _ := registry.Lookup("TaskStatus")
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		got, err := tool.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1"}`)
		if err == nil && strings.Contains(got, "· "+status+" ·") {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task did not reach %q", status)
	return ""
}

func TestTaskRunStatusAndCancelControlOneChild(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate","TaskRun","TaskStatus","TaskCancel"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"child","worktree":".worktrees/agent-1"}`); err != nil {
		t.Fatal(err)
	}
	handle := newTestTaskHandle()
	registry.(TaskRegistry).SetTaskRunner(func(context.Context, TaskState, string, time.Duration) (TaskHandle, error) {
		return handle, nil
	})
	run, _ := registry.Lookup("TaskRun")
	started, err := run.Execute(context.Background(), root, `{"task_id":"task-1","prompt":"inspect"}`)
	if err != nil || !strings.Contains(started, "running") {
		t.Fatalf("started=%q err=%v", started, err)
	}
	status, _ := registry.Lookup("TaskStatus")
	if got, err := status.Execute(context.Background(), root, `{"task_id":"task-1"}`); err != nil || !strings.Contains(got, "running") {
		t.Fatalf("status=%q err=%v", got, err)
	}
	cancel, _ := registry.Lookup("TaskCancel")
	if _, err := cancel.Execute(context.Background(), root, `{"task_id":"task-1"}`); err != nil {
		t.Fatal(err)
	}
	if got := waitForTaskStatus(t, registry, "cancelled"); !strings.Contains(got, "child") {
		t.Fatalf("cancelled=%q", got)
	}
}

func TestTaskRunDetachesChildFromToolCallContext(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"child","worktree":".worktrees/agent-1"}`); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	deferred := make(chan context.Context, 1)
	handle := newTestTaskHandle()
	registry.(TaskRegistry).SetTaskRunner(func(ctx context.Context, _ TaskState, _ string, _ time.Duration) (TaskHandle, error) {
		deferred <- ctx
		return handle, nil
	})
	run, _ := registry.Lookup("TaskRun")
	if _, err := run.Execute(parent, root, `{"task_id":"task-1","prompt":"inspect"}`); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case ctx := <-deferred:
		if ctx.Err() != nil {
			t.Fatalf("child context was canceled with tool call: %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("runner was not called")
	}
	handle.complete(TaskExecutionResult{State: "completed"})
}

func TestTaskRunCompletesAndReturnsChildOutputInStatus(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate","TaskRun","TaskStatus"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"child","worktree":".worktrees/agent-1"}`); err != nil {
		t.Fatal(err)
	}
	handle := newTestTaskHandle()
	registry.(TaskRegistry).SetTaskRunner(func(context.Context, TaskState, string, time.Duration) (TaskHandle, error) { return handle, nil })
	run, _ := registry.Lookup("TaskRun")
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-1","prompt":"inspect"}`); err != nil {
		t.Fatal(err)
	}
	handle.complete(TaskExecutionResult{State: "completed", Output: "verified"})
	if got := waitForTaskStatus(t, registry, "completed"); !strings.Contains(got, "result: verified") {
		t.Fatalf("status=%q", got)
	}
}

func TestTaskRunAutomaticallyMergesCompletedWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate","TaskRun","TaskStatus"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"child","worktree":".worktrees/agent-1"}`); err != nil {
		t.Fatal(err)
	}
	handle := newTestTaskHandle()
	called := make(chan string, 1)
	control := registry.(TaskRegistry)
	control.SetTaskRunner(func(context.Context, TaskState, string, time.Duration) (TaskHandle, error) { return handle, nil })
	control.SetTaskMerger(func(_ context.Context, mergeRoot string, task TaskState) (TaskMergeResult, error) {
		called <- mergeRoot
		return TaskMergeResult{Status: "success", AfterHEAD: "merge-1"}, nil
	})
	run, _ := registry.Lookup("TaskRun")
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-1","prompt":"inspect"}`); err != nil {
		t.Fatal(err)
	}
	handle.complete(TaskExecutionResult{State: "completed", Output: "verified"})
	select {
	case got := <-called:
		if got != root {
			t.Fatalf("merge root=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("merge callback not called")
	}
	status, _ := registry.Lookup("TaskStatus")
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		got, _ := status.Execute(context.Background(), root, `{"task_id":"task-1"}`)
		if strings.Contains(got, "merge: merged") && strings.Contains(got, "merge commit: merge-1") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	got, _ := status.Execute(context.Background(), root, `{"task_id":"task-1"}`)
	t.Fatalf("status=%q", got)
}

func TestTaskMergeRetriesQueuedConflict(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate","TaskRun","TaskStatus","TaskMerge"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"child","worktree":".worktrees/agent-1"}`); err != nil {
		t.Fatal(err)
	}
	handle := newTestTaskHandle()
	merges := 0
	control := registry.(TaskRegistry)
	control.SetTaskRunner(func(context.Context, TaskState, string, time.Duration) (TaskHandle, error) { return handle, nil })
	control.SetTaskMerger(func(_ context.Context, _ string, task TaskState) (TaskMergeResult, error) {
		merges++
		if task.MergeStatus == "conflict" {
			return TaskMergeResult{Status: "success", AfterHEAD: "merge-2"}, nil
		}
		return TaskMergeResult{Status: "conflict", Conflicts: []string{"same.txt"}}, nil
	})
	run, _ := registry.Lookup("TaskRun")
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-1","prompt":"inspect"}`); err != nil {
		t.Fatal(err)
	}
	handle.complete(TaskExecutionResult{State: "completed", Output: "verified"})
	waitForTaskStatus(t, registry, "completed")
	status, _ := registry.Lookup("TaskStatus")
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		got, _ := status.Execute(context.Background(), root, `{"task_id":"task-1"}`)
		if strings.Contains(got, "merge: conflict") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	merge, _ := registry.Lookup("TaskMerge")
	if got, err := merge.Execute(context.Background(), root, `{"task_id":"task-1"}`); err != nil || !strings.Contains(got, "merge: merged") {
		t.Fatalf("merge=%q err=%v", got, err)
	}
	if merges != 2 {
		t.Fatalf("merge attempts=%d", merges)
	}
}

func TestTaskRunKeepsIndependentTaskResultsIsolated(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"agent-1", "agent-2"} {
		if err := os.MkdirAll(filepath.Join(root, ".worktrees", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate","TaskRun","TaskStatus"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"one","worktree":".worktrees/agent-1"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := create.Execute(context.Background(), root, `{"subject":"two","worktree":".worktrees/agent-2"}`); err != nil {
		t.Fatal(err)
	}
	handles := map[string]*testTaskHandle{"task-1": newTestTaskHandle(), "task-2": newTestTaskHandle()}
	registry.(TaskRegistry).SetTaskRunner(func(_ context.Context, task TaskState, _ string, _ time.Duration) (TaskHandle, error) {
		return handles[task.ID], nil
	})
	run, _ := registry.Lookup("TaskRun")
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-1","prompt":"one"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-2","prompt":"two"}`); err != nil {
		t.Fatal(err)
	}
	handles["task-1"].complete(TaskExecutionResult{State: "failed", Err: errors.New("one failed")})
	handles["task-2"].complete(TaskExecutionResult{State: "completed", Output: "two complete"})
	status, _ := registry.Lookup("TaskStatus")
	var first, second string
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		first, _ = status.Execute(context.Background(), root, `{"task_id":"task-1"}`)
		second, _ = status.Execute(context.Background(), root, `{"task_id":"task-2"}`)
		if strings.Contains(first, "· failed ·") && strings.Contains(second, "result: two complete") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(first, "error: one failed") || !strings.Contains(second, "· completed ·") || strings.Contains(second, "one failed") {
		t.Fatalf("first=%q second=%q", first, second)
	}
}

func TestTaskDependenciesBlockUntilPrerequisiteCompletes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(root, ".worktrees", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate","TaskRun","TaskStatus"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"one","worktree":".worktrees/one"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := create.Execute(context.Background(), root, `{"subject":"two","worktree":".worktrees/two","depends_on":["task-1"]}`); err != nil {
		t.Fatal(err)
	}
	handles := map[string]*testTaskHandle{"task-1": newTestTaskHandle(), "task-2": newTestTaskHandle()}
	started := make([]string, 0, 2)
	registry.(TaskRegistry).SetTaskRunner(func(_ context.Context, task TaskState, _ string, _ time.Duration) (TaskHandle, error) {
		started = append(started, task.ID)
		return handles[task.ID], nil
	})
	run, _ := registry.Lookup("TaskRun")
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-2","prompt":"blocked"}`); err == nil {
		t.Fatal("dependent task started before prerequisite")
	}
	status, _ := registry.Lookup("TaskStatus")
	blocked, err := status.Execute(context.Background(), root, `{"task_id":"task-2"}`)
	if err != nil || !strings.Contains(blocked, "· blocked ·") || len(started) != 0 {
		t.Fatalf("blocked=%q started=%v err=%v", blocked, started, err)
	}
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-1","prompt":"first"}`); err != nil {
		t.Fatal(err)
	}
	handles["task-1"].complete(TaskExecutionResult{State: "completed", Output: "first done"})
	waitForTaskStatus(t, registry, "completed")
	if _, err := run.Execute(context.Background(), root, `{"task_id":"task-2","prompt":"second"}`); err != nil {
		t.Fatal(err)
	}
	if len(started) != 2 || started[1] != "task-2" {
		t.Fatalf("started=%v", started)
	}
}

func TestTaskDependencyCycleIsRejected(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"task","load":["TaskCreate","TaskUpdate"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), t.TempDir(), `{"subject":"one"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := create.Execute(context.Background(), t.TempDir(), `{"subject":"two","depends_on":["task-1"]}`); err != nil {
		t.Fatal(err)
	}
	update, _ := registry.Lookup("TaskUpdate")
	if _, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","depends_on":["task-2"]}`); err == nil {
		t.Fatal("dependency cycle accepted")
	}
}

func TestTaskRestoreRecomputesBlockedAndRecoversRunning(t *testing.T) {
	registry := NewChatRegistry()
	tasks := registry.(TaskRegistry)
	if err := tasks.RestoreTasks([]TaskState{
		{ID: "task-1", Subject: "one", Status: "running"},
		{ID: "task-2", Subject: "two", Status: "pending", DependsOn: []string{"task-1"}},
	}); err != nil {
		t.Fatal(err)
	}
	items := tasks.ExportTasks()
	if items[0].Status != "pending" || items[1].Status != "blocked" {
		t.Fatalf("recovered=%+v", items)
	}
	if err := tasks.RestoreTasks([]TaskState{
		{ID: "task-1", Subject: "one", Status: "completed"},
		{ID: "task-2", Subject: "two", Status: "blocked", DependsOn: []string{"task-1"}},
	}); err != nil {
		t.Fatal(err)
	}
	items = tasks.ExportTasks()
	if items[1].Status != "pending" {
		t.Fatalf("completed dependency did not unblock: %+v", items)
	}
}

func TestTaskToolsCreateListGetAndUpdateWithinChat(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"task","load":["TaskCreate","TaskList","TaskGet","TaskUpdate"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	created, err := create.Execute(context.Background(), t.TempDir(), `{"subject":"add tests","description":"cover the new task flow"}`)
	if err != nil || !strings.Contains(created, "task-1") || !strings.Contains(created, "pending") {
		t.Fatalf("create=%q err=%v", created, err)
	}
	update, _ := registry.Lookup("TaskUpdate")
	updated, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","status":"running"}`)
	if err != nil || !strings.Contains(updated, "running") {
		t.Fatalf("update=%q err=%v", updated, err)
	}
	get, _ := registry.Lookup("TaskGet")
	got, err := get.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1"}`)
	if err != nil || !strings.Contains(got, "cover the new task flow") || !strings.Contains(got, "running") {
		t.Fatalf("get=%q err=%v", got, err)
	}
	list, _ := registry.Lookup("TaskList")
	listed, err := list.Execute(context.Background(), t.TempDir(), `{}`)
	if err != nil || !strings.Contains(listed, "task-1") || !strings.Contains(listed, "add tests") {
		t.Fatalf("list=%q err=%v", listed, err)
	}
}

func TestTaskWorktreeBindingIsPersistedAndPathScoped(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"task","load":["TaskCreate","TaskUpdate"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	created, err := create.Execute(context.Background(), t.TempDir(), `{"subject":"isolated change","worktree":".worktrees/agent-1"}`)
	if err != nil || !strings.Contains(created, ".worktrees/agent-1") {
		t.Fatalf("create=%q err=%v", created, err)
	}
	update, _ := registry.Lookup("TaskUpdate")
	if _, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","worktree":".worktrees/agent-2"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","worktree":"../escape"}`); err == nil {
		t.Fatal("escaped worktree accepted")
	}
	state := registry.(TaskRegistry).ExportTasks()
	if len(state) != 1 || state[0].Worktree != ".worktrees/agent-2" {
		t.Fatalf("state=%+v", state)
	}
}

func TestTaskSwitchSelectsExistingWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate","TaskSwitch"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"isolated","worktree":".worktrees/agent-1"}`); err != nil {
		t.Fatal(err)
	}
	switchTool, _ := registry.Lookup("TaskSwitch")
	if _, err := switchTool.Execute(context.Background(), root, `{"task_id":"task-1"}`); err != nil {
		t.Fatal(err)
	}
	path, err := registry.(TaskSwitcher).ActiveWorktree(root)
	if err != nil || filepath.Clean(path) != filepath.Clean(filepath.Join(root, ".worktrees", "agent-1")) {
		t.Fatalf("active path=%q err=%v", path, err)
	}
}

func TestTaskUpdateRejectsInvalidStatusAndUnknownTask(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"task","load":["TaskCreate","TaskUpdate"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), t.TempDir(), `{"subject":"one"}`); err != nil {
		t.Fatal(err)
	}
	update, _ := registry.Lookup("TaskUpdate")
	if _, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","status":"done"}`); err == nil {
		t.Fatal("invalid status accepted")
	}
	if _, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-9","status":"completed"}`); err == nil {
		t.Fatal("unknown task accepted")
	}
}

func TestTaskListRejectsUnknownArguments(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"task","load":["TaskList"]}`); err != nil {
		t.Fatal(err)
	}
	list, _ := registry.Lookup("TaskList")
	if _, err := list.Execute(context.Background(), t.TempDir(), `{"unexpected":true}`); err == nil {
		t.Fatal("TaskList accepted unknown arguments")
	}
}

func TestTaskStateCanBeExportedAndRestored(t *testing.T) {
	first := NewChatRegistry()
	firstTasks := first.(TaskRegistry)
	search, _ := first.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"task","load":["TaskCreate","TaskUpdate"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := first.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), t.TempDir(), `{"subject":"persist me","description":"resume this"}`); err != nil {
		t.Fatal(err)
	}
	update, _ := first.Lookup("TaskUpdate")
	if _, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","status":"running"}`); err != nil {
		t.Fatal(err)
	}

	second := NewChatRegistry()
	secondTasks := second.(TaskRegistry)
	if err := secondTasks.RestoreTasks(firstTasks.ExportTasks()); err != nil {
		t.Fatal(err)
	}
	search, _ = second.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"task","load":["TaskGet"]}`); err != nil {
		t.Fatal(err)
	}
	get, _ := second.Lookup("TaskGet")
	got, err := get.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1"}`)
	if err != nil || !strings.Contains(got, "persist me") || !strings.Contains(got, "pending") {
		t.Fatalf("restored=%q err=%v", got, err)
	}
}
