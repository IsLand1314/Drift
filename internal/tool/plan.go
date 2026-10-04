package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

type planModeTool struct{ name string }

type PlanTask struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
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
type planStore struct{ plan Plan }

func (planUpdateTool) Name() string { return "PlanUpdate" }
func (planUpdateTool) Definition() llm.ToolDefinition {
	return controlDefinition("PlanUpdate", "Save the structured plan while in plan mode.", map[string]any{
		"goal": map[string]any{"type": "string"}, "tasks": map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "maxItems": 32},
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
		if plan.Tasks[i].ID == "" || plan.Tasks[i].Title == "" || !validPlanStatus(plan.Tasks[i].Status) {
			return "", fmt.Errorf("PlanUpdate task is invalid")
		}
	}
	t.store.plan.Goal, t.store.plan.Tasks = plan.Goal, append([]PlanTask(nil), plan.Tasks...)
	t.store.plan.Risks, t.store.plan.Acceptance = append([]string(nil), plan.Risks...), append([]string(nil), plan.Acceptance...)
	return fmt.Sprintf("PlanUpdate: %s (%d tasks)", t.store.plan.ID, len(plan.Tasks)), nil
}
func validPlanStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "blocked", "failed":
		return true
	default:
		return false
	}
}
