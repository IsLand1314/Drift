package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

type planModeTool struct{ name string }

type PlanTask struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Status       string   `json:"status"`
	Worktree     string   `json:"worktree,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type Plan struct {
	ID         string     `json:"id,omitempty"`
	Goal       string     `json:"goal,omitempty"`
	Tasks      []PlanTask `json:"tasks,omitempty"`
	Risks      []string   `json:"risks,omitempty"`
	Acceptance []string   `json:"acceptance,omitempty"`
}

type PlanRegistry interface {
	SetPlanID(string)
	ExportPlan() Plan
	RestorePlan(Plan) error
	ResetPlan()
}

func (t planModeTool) Name() string { return t.name }

func (t planModeTool) Definition() llm.ToolDefinition {
	description := "Enter read-only plan mode before analyzing a multi-step change."
	if t.name == "ExitPlanMode" {
		description = "Exit plan mode after the user approves continuing with changes."
	}
	return controlDefinition(t.name, description, map[string]any{}, nil)
}

func (t planModeTool) Execute(context.Context, string, string) (string, error) {
	return "", nil
}

func (t planModeTool) Preview(_ context.Context, _ string, raw string) (Preview, error) {
	if strings.TrimSpace(raw) != "" {
		var args map[string]any
		if err := json.Unmarshal([]byte(raw), &args); err != nil || len(args) != 0 {
			return Preview{}, fmt.Errorf("%s accepts no arguments", t.name)
		}
	}
	return Preview{Operation: strings.ToLower(t.name), OldBytes: 0, NewBytes: 0}, nil
}

func (t planModeTool) ExecutePreview(context.Context, string, Preview) (string, error) {
	if t.name == "EnterPlanMode" {
		return "Plan mode enabled; only read and analysis tools are allowed.", nil
	}
	return "Plan mode exited; normal permission checks apply.", nil
}

type planUpdateTool struct{ store *planStore }
type planStore struct {
	plan  Plan
	tasks *taskStore
}

func (planUpdateTool) Name() string { return "PlanUpdate" }
func (planUpdateTool) Definition() llm.ToolDefinition {
	taskSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"},
			"worktree": map[string]any{"type": "string"}, "dependencies": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"id", "title", "status"},
	}
	return controlDefinition("PlanUpdate", "Save the structured plan while in plan mode.", map[string]any{
		"goal": map[string]any{"type": "string"}, "tasks": map[string]any{"type": "array", "items": taskSchema, "maxItems": 32},
		"risks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 32}, "acceptance": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 32},
	}, []string{"goal"})
}
func (t planUpdateTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var plan Plan
	if err := decodeTaskArgs(raw, &plan); err != nil {
		return "", fmt.Errorf("decode PlanUpdate arguments: %w", err)
	}
	plan.Goal = strings.TrimSpace(plan.Goal)
	if plan.Goal == "" || len([]rune(plan.Goal)) > 500 || len(plan.Tasks) > 32 || len(plan.Risks) > 32 || len(plan.Acceptance) > 32 {
		return "", fmt.Errorf("PlanUpdate arguments are invalid")
	}
	for i := range plan.Tasks {
		plan.Tasks[i].ID, plan.Tasks[i].Title, plan.Tasks[i].Status = strings.TrimSpace(plan.Tasks[i].ID), strings.TrimSpace(plan.Tasks[i].Title), strings.TrimSpace(plan.Tasks[i].Status)
		plan.Tasks[i].Description = strings.TrimSpace(plan.Tasks[i].Description)
		plan.Tasks[i].Worktree = normalizeTaskWorktree(plan.Tasks[i].Worktree)
		if plan.Tasks[i].ID == "" || plan.Tasks[i].Title == "" || len([]rune(plan.Tasks[i].Description)) > 4000 || !validPlanStatus(plan.Tasks[i].Status) || (plan.Tasks[i].Worktree != "" && !validTaskWorktree(plan.Tasks[i].Worktree)) {
			return "", fmt.Errorf("PlanUpdate task is invalid")
		}
	}
	if err := t.store.tasks.syncPlanTasks(plan.Tasks); err != nil {
		return "", err
	}
	t.store.plan.Goal, t.store.plan.Tasks = plan.Goal, append([]PlanTask(nil), plan.Tasks...)
	t.store.plan.Risks, t.store.plan.Acceptance = append([]string(nil), plan.Risks...), append([]string(nil), plan.Acceptance...)
	return fmt.Sprintf("PlanUpdate: %s (%d tasks)", t.store.plan.ID, len(plan.Tasks)), nil
}

type planExecuteTool struct {
	plan  *planStore
	tasks *taskStore
}

func (planExecuteTool) Name() string { return "PlanExecute" }
func (planExecuteTool) Definition() llm.ToolDefinition {
	return controlDefinition("PlanExecute", "Execute the current plan's tasks sequentially through the existing child Agent runner.", map[string]any{
		"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 600000},
	}, nil)
}
func (t planExecuteTool) Execute(ctx context.Context, root, raw string) (string, error) {
	var args struct {
		TimeoutMS int `json:"timeout_ms"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode PlanExecute arguments: %w", err)
	}
	if args.TimeoutMS < 0 || args.TimeoutMS > 600000 {
		return "", fmt.Errorf("PlanExecute arguments are invalid")
	}
	if len(t.plan.plan.Tasks) == 0 {
		return "", fmt.Errorf("PlanExecute has no tasks")
	}
	t.tasks.mu.Lock()
	runnerAvailable := t.tasks.runner != nil
	t.tasks.mu.Unlock()
	if !runnerAvailable {
		for _, planTask := range t.plan.plan.Tasks {
			if item, ok := t.tasks.state(planTask.ID); ok && item.Status != "completed" {
				t.tasks.fail(planTask.ID, "TaskRun is unavailable in this host")
			}
		}
		return "", fmt.Errorf("PlanExecute is unavailable in this host")
	}
	for _, planTask := range t.plan.plan.Tasks {
		item, ok := t.tasks.state(planTask.ID)
		if !ok {
			return "", fmt.Errorf("PlanExecute task %q not found", planTask.ID)
		}
		if !dependenciesCompleted(t.tasks, item) {
			return "", fmt.Errorf("PlanExecute task %q is blocked by dependencies", item.ID)
		}
		if item.Status == "completed" {
			continue
		}
		rawRun, _ := json.Marshal(map[string]any{"task_id": item.ID, "prompt": item.Description, "timeout_ms": args.TimeoutMS})
		if _, err := (taskRunTool{store: t.tasks}).Execute(ctx, root, string(rawRun)); err != nil {
			return "", err
		}
		for {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			current, _ := t.tasks.state(item.ID)
			switch current.Status {
			case "completed":
				break
			case "failed", "cancelled", "timeout", "blocked":
				blockPlanDependents(t.tasks, t.plan.plan.Tasks, current.ID)
				return "", fmt.Errorf("PlanExecute task %q ended with %s", current.ID, current.Status)
			default:
				time.Sleep(20 * time.Millisecond)
				continue
			}
			break
		}
	}
	return fmt.Sprintf("PlanExecute: completed %d tasks", len(t.plan.plan.Tasks)), nil
}

func blockPlanDependents(store *taskStore, planTasks []PlanTask, failedID string) {
	changed := true
	for changed {
		changed = false
		for _, planTask := range planTasks {
			item, ok := store.state(planTask.ID)
			if !ok || item.Status != "pending" {
				continue
			}
			for _, dependency := range item.DependsOn {
				dependencyState, exists := store.state(dependency)
				if dependency == failedID || (exists && (dependencyState.Status == "failed" || dependencyState.Status == "cancelled" || dependencyState.Status == "timeout" || dependencyState.Status == "blocked")) {
					store.mu.Lock()
					item.Status = "blocked"
					item.Error = "dependency " + dependency + " failed"
					store.tasks[item.ID] = item
					store.mu.Unlock()
					changed = true
					break
				}
			}
		}
	}
}

func dependenciesCompleted(store *taskStore, item TaskState) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return dependenciesCompletedLocked(store.tasks, item)
}
func validPlanStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "blocked", "failed":
		return true
	default:
		return false
	}
}
