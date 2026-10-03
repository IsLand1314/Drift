package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/IsLand1314/Drift/internal/llm"
)

type task struct {
	ID          string
	Subject     string
	Description string
	Status      string
}

type taskStore struct {
	mu    sync.Mutex
	next  int
	tasks map[string]task
}

func newTaskStore() *taskStore { return &taskStore{tasks: make(map[string]task)} }

type taskCreateTool struct{ store *taskStore }
type taskListTool struct{ store *taskStore }
type taskGetTool struct{ store *taskStore }
type taskUpdateTool struct{ store *taskStore }

func (taskCreateTool) Name() string { return "TaskCreate" }
func (taskListTool) Name() string   { return "TaskList" }
func (taskGetTool) Name() string    { return "TaskGet" }
func (taskUpdateTool) Name() string { return "TaskUpdate" }

func (t taskCreateTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Create a session task.", map[string]any{
		"subject":     map[string]any{"type": "string", "description": "Short task title."},
		"description": map[string]any{"type": "string", "description": "Optional task details."},
	}, []string{"subject"})
}

func (t taskListTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "List tasks in the current chat session.", map[string]any{}, nil)
}

func (t taskGetTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Get one task from the current chat session.", map[string]any{
		"task_id": map[string]any{"type": "string"},
	}, []string{"task_id"})
}

func (t taskUpdateTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Update a task status or description.", map[string]any{
		"task_id":     map[string]any{"type": "string"},
		"status":      map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
		"description": map[string]any{"type": "string"},
	}, []string{"task_id"})
}

func (t taskCreateTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskCreate arguments: %w", err)
	}
	args.Subject = strings.TrimSpace(args.Subject)
	args.Description = strings.TrimSpace(args.Description)
	if args.Subject == "" || len([]rune(args.Subject)) > 200 || len([]rune(args.Description)) > 2000 {
		return "", fmt.Errorf("TaskCreate arguments are invalid")
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	t.store.next++
	item := task{ID: fmt.Sprintf("task-%d", t.store.next), Subject: args.Subject, Description: args.Description, Status: "pending"}
	t.store.tasks[item.ID] = item
	return formatTask(item), nil
}

func (t taskListTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct{}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", err
	}
	t.store.mu.Lock()
	items := make([]task, 0, len(t.store.tasks))
	for _, item := range t.store.tasks {
		items = append(items, item)
	}
	t.store.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if len(items) == 0 {
		return "TaskList: no tasks", nil
	}
	var out strings.Builder
	for index, item := range items {
		if index > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(formatTask(item))
	}
	return out.String(), nil
}

func (t taskGetTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskGet arguments: %w", err)
	}
	return t.store.get(strings.TrimSpace(args.TaskID))
}

func (t taskUpdateTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID      string `json:"task_id"`
		Status      string `json:"status"`
		Description string `json:"description"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskUpdate arguments: %w", err)
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	args.Status = strings.TrimSpace(args.Status)
	args.Description = strings.TrimSpace(args.Description)
	if args.Status != "" && !validTaskStatus(args.Status) || len([]rune(args.Description)) > 2000 {
		return "", fmt.Errorf("TaskUpdate arguments are invalid")
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	item, ok := t.store.tasks[args.TaskID]
	if !ok {
		return "", fmt.Errorf("TaskUpdate task %q not found", args.TaskID)
	}
	if args.Status != "" {
		item.Status = args.Status
	}
	if args.Description != "" {
		item.Description = args.Description
	}
	t.store.tasks[item.ID] = item
	return formatTask(item), nil
}

func (s *taskStore) get(id string) (string, error) {
	s.mu.Lock()
	item, ok := s.tasks[id]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("TaskGet task %q not found", id)
	}
	return formatTask(item), nil
}

func validTaskStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "cancelled":
		return true
	default:
		return false
	}
}

func formatTask(item task) string {
	result := item.ID + " · " + item.Status + " · " + item.Subject
	if item.Description != "" {
		result += "\n" + item.Description
	}
	return result
}

func decodeTaskArgs(raw string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}
