package tool

import (
	"context"
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
