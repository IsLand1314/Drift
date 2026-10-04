package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

type planModeTool struct{ name string }

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
