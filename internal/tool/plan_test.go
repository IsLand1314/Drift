package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanModeToolsAreAvailableAndSideEffectFree(t *testing.T) {
	registry := NewChatRegistry()
	search, ok := registry.Lookup("ToolSearch")
	if !ok {
		t.Fatal("ToolSearch is not enabled")
	}
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"plan","load":["EnterPlanMode","ExitPlanMode"]}`); err != nil {
		t.Fatalf("load plan tools: %v", err)
	}
	for _, name := range []string{"EnterPlanMode", "ExitPlanMode"} {
		plan, ok := registry.Lookup(name)
		if !ok {
			t.Fatalf("%s is not enabled", name)
		}
		previewable, ok := plan.(Previewable)
		if !ok {
			t.Fatalf("%s must use preview flow", name)
		}
		preview, err := previewable.Preview(context.Background(), t.TempDir(), `{}`)
		if err != nil {
			t.Fatalf("%s preview: %v", name, err)
		}
		result, err := previewable.ExecutePreview(context.Background(), t.TempDir(), preview)
		if err != nil || !strings.Contains(result, "Plan mode") {
			t.Fatalf("%s result=%q err=%v", name, result, err)
		}
		var definition map[string]any
		if err := json.Unmarshal(plan.Definition().Function, &definition); err != nil {
			t.Fatal(err)
		}
		parameters := definition["parameters"].(map[string]any)
		if required, ok := parameters["required"]; !ok || required == nil {
			t.Fatalf("%s schema omitted required", name)
		}
	}
}

func TestPlanUpdateStoresStructuredPlan(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"plan","load":["PlanUpdate"]}`); err != nil {
		t.Fatal(err)
	}
	plans, ok := registry.(PlanRegistry)
	if !ok {
		t.Fatal("registry does not expose plan state")
	}
	plans.SetPlanID("plan-1")
	update, _ := registry.Lookup("PlanUpdate")
	result, err := update.Execute(context.Background(), t.TempDir(), `{"goal":"demo","tasks":[{"id":"task-1","title":"inspect","status":"pending"}],"risks":["none"],"acceptance":["go test ./..."]}`)
	if err != nil {
		t.Fatal(err)
	}
	plan := plans.ExportPlan()
	if plan.ID != "plan-1" || plan.Goal != "demo" || len(plan.Tasks) != 1 || !strings.Contains(result, "plan-1") {
		t.Fatalf("plan=%+v result=%q", plan, result)
	}
}

func TestPlanExecuteRunsPlanTasksThroughTaskRunner(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"plan","load":["PlanUpdate","PlanExecute"]}`); err != nil {
		t.Fatal(err)
	}
	plans := registry.(PlanRegistry)
	plans.SetPlanID("plan-1")
	handle := newTestTaskHandle()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	var gotPrompt string
	registry.(TaskRegistry).SetTaskRunner(func(_ context.Context, _ TaskState, prompt string, _ time.Duration) (TaskHandle, error) {
		gotPrompt = prompt
		go func() {
			time.Sleep(5 * time.Millisecond)
			handle.complete(TaskExecutionResult{State: "completed", Output: "ok"})
		}()
		return handle, nil
	})
	update, _ := registry.Lookup("PlanUpdate")
	if _, err := update.Execute(context.Background(), root, `{"goal":"demo","tasks":[{"id":"task-1","title":"run","description":"Call ReadFile for README.md and report its contents.","status":"pending","worktree":".worktrees/agent-1"}]}`); err != nil {
		t.Fatal(err)
	}
	execute, _ := registry.Lookup("PlanExecute")
	result, err := execute.Execute(context.Background(), root, `{}`)
	if err != nil || !strings.Contains(result, "completed 1 tasks") {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if gotPrompt != "Call ReadFile for README.md and report its contents." {
		t.Fatalf("prompt=%q", gotPrompt)
	}
}

type testCoordinator struct {
	called bool
	root   string
}

func (c *testCoordinator) Run(_ context.Context, root string, tasks []TaskState) ([]TaskState, error) {
	c.called, c.root = true, root
	for i := range tasks {
		tasks[i].Status = "completed"
		tasks[i].Result = "coordinated"
	}
	return tasks, nil
}
func (*testCoordinator) Cancel()             {}
func (*testCoordinator) Status() []TaskState { return nil }

func TestPlanExecuteDelegatesToCoordinator(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	root := t.TempDir()
	if _, err := search.Execute(context.Background(), root, `{"query":"plan","load":["PlanUpdate","PlanExecute"]}`); err != nil {
		t.Fatal(err)
	}
	registry.(PlanRegistry).SetPlanID("plan-1")
	update, _ := registry.Lookup("PlanUpdate")
	if _, err := update.Execute(context.Background(), root, `{"goal":"demo","tasks":[{"id":"task-1","title":"run","status":"pending"}]}`); err != nil {
		t.Fatal(err)
	}
	coordinator := &testCoordinator{}
	registry.(TaskRegistry).SetCoordinator(coordinator)
	execute, _ := registry.Lookup("PlanExecute")
	if _, err := execute.Execute(context.Background(), root, `{}`); err != nil {
		t.Fatal(err)
	}
	if !coordinator.called || coordinator.root != root {
		t.Fatalf("coordinator called=%v root=%q", coordinator.called, coordinator.root)
	}
	states := registry.(TaskRegistry).ExportTasks()
	if len(states) != 1 || states[0].Status != "completed" || states[0].Result != "coordinated" {
		t.Fatalf("persisted task state=%+v", states)
	}
}

func TestPlanExecuteMarksTasksFailedWhenRunnerUnavailable(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	root := t.TempDir()
	if _, err := search.Execute(context.Background(), root, `{"query":"plan","load":["PlanUpdate","PlanExecute"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Execute(context.Background(), root, `{"query":"task status","load":["TaskStatus"]}`); err != nil {
		t.Fatal(err)
	}
	plans := registry.(PlanRegistry)
	plans.SetPlanID("plan-1")
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	update, _ := registry.Lookup("PlanUpdate")
	if _, err := update.Execute(context.Background(), root, `{"goal":"demo","tasks":[{"id":"task-1","title":"run","status":"pending","worktree":".worktrees/agent-1"}]}`); err != nil {
		t.Fatal(err)
	}
	execute, _ := registry.Lookup("PlanExecute")
	if _, err := execute.Execute(context.Background(), root, `{}`); err == nil {
		t.Fatal("PlanExecute unexpectedly succeeded without a runner")
	}
	status, _ := registry.Lookup("TaskStatus")
	got, err := status.Execute(context.Background(), root, `{"task_id":"task-1"}`)
	if err != nil || !strings.Contains(got, "· failed ·") || !strings.Contains(got, "TaskRun is unavailable in this host") {
		t.Fatalf("status=%q err=%v", got, err)
	}
}

func TestPlanExecuteBlocksDependentsAfterTaskFailure(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	root := t.TempDir()
	if _, err := search.Execute(context.Background(), root, `{"query":"plan","load":["PlanUpdate","PlanExecute"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Execute(context.Background(), root, `{"query":"task status","load":["TaskStatus"]}`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	registry.(PlanRegistry).SetPlanID("plan-1")
	handles := map[string]*testTaskHandle{}
	registry.(TaskRegistry).SetTaskRunner(func(_ context.Context, task TaskState, _ string, _ time.Duration) (TaskHandle, error) {
		handle := newTestTaskHandle()
		handles[task.ID] = handle
		return handle, nil
	})
	update, _ := registry.Lookup("PlanUpdate")
	if _, err := update.Execute(context.Background(), root, `{"goal":"demo","tasks":[{"id":"task-1","title":"first","status":"pending","worktree":".worktrees/agent-1"},{"id":"task-2","title":"second","status":"pending","worktree":".worktrees/agent-2","dependencies":["task-1"]}]}`); err != nil {
		t.Fatal(err)
	}
	execute, _ := registry.Lookup("PlanExecute")
	done := make(chan error, 1)
	go func() { _, err := execute.Execute(context.Background(), root, `{}`); done <- err }()
	deadline := time.Now().Add(time.Second)
	for handles["task-1"] == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if handles["task-1"] == nil {
		t.Fatal("first task did not start")
	}
	handles["task-1"].complete(TaskExecutionResult{State: "failed", Err: errors.New("first failed")})
	if err := <-done; err == nil {
		t.Fatal("PlanExecute unexpectedly succeeded")
	}
	status, _ := registry.Lookup("TaskStatus")
	got, err := status.Execute(context.Background(), root, `{"task_id":"task-2"}`)
	if err != nil || !strings.Contains(got, "· blocked ·") || !strings.Contains(got, "dependency task-1 failed") {
		t.Fatalf("status=%q err=%v", got, err)
	}
}
