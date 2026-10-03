package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	updated, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","status":"in_progress"}`)
	if err != nil || !strings.Contains(updated, "in_progress") {
		t.Fatalf("update=%q err=%v", updated, err)
	}
	get, _ := registry.Lookup("TaskGet")
	got, err := get.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1"}`)
	if err != nil || !strings.Contains(got, "cover the new task flow") || !strings.Contains(got, "in_progress") {
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
	if _, err := update.Execute(context.Background(), t.TempDir(), `{"task_id":"task-1","status":"in_progress"}`); err != nil {
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
	if err != nil || !strings.Contains(got, "persist me") || !strings.Contains(got, "in_progress") {
		t.Fatalf("restored=%q err=%v", got, err)
	}
}
