package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/memory"
)

type experienceProposeTool struct{}

func (experienceProposeTool) Name() string { return "ExperiencePropose" }
func (experienceProposeTool) Definition() llm.ToolDefinition {
	return controlDefinition("ExperiencePropose", "Draft a candidate experience for user review. This does not persist anything.", experienceProperties(), []string{"title", "trigger", "solution", "verification"})
}
func (experienceProposeTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	item, err := parseExperience(raw)
	if err != nil {
		return "", err
	}
	return formatExperience(item, "candidate"), nil
}

type experienceSaveTool struct{ repo *memory.Repository }

func (t experienceSaveTool) Name() string { return "ExperienceSave" }
func (t experienceSaveTool) Definition() llm.ToolDefinition {
	return controlDefinition("ExperienceSave", "Save a user-approved candidate experience. Requires normal write approval.", experienceProperties(), []string{"title", "trigger", "solution", "verification"})
}
func (t experienceSaveTool) Execute(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("ExperienceSave requires approval")
}
func (t experienceSaveTool) Preview(_ context.Context, _ string, raw string) (Preview, error) {
	item, err := parseExperience(raw)
	if err != nil {
		return Preview{}, err
	}
	if t.repo == nil {
		return Preview{}, fmt.Errorf("ExperienceSave memory repository unavailable")
	}
	data, _ := json.Marshal(item)
	return Preview{Operation: "memory_write", Path: ".drift/memory/experiences.jsonl", Content: data, NewBytes: len(data), Diff: formatExperience(item, "verified")}, nil
}
func (t experienceSaveTool) ExecutePreview(_ context.Context, _ string, preview Preview) (string, error) {
	var item memory.Item
	if err := json.Unmarshal(preview.Content, &item); err != nil {
		return "", err
	}
	item.Status = "verified"
	if err := t.repo.Add(item); err != nil {
		return "", err
	}
	return "ExperienceSave: verified experience saved", nil
}

func experienceProperties() map[string]any {
	return map[string]any{
		"title":        map[string]any{"type": "string"},
		"trigger":      map[string]any{"type": "string"},
		"solution":     map[string]any{"type": "string"},
		"verification": map[string]any{"type": "string"},
		"source":       map[string]any{"type": "string"},
	}
}

func parseExperience(raw string) (memory.Item, error) {
	var args struct {
		Title        string `json:"title"`
		Trigger      string `json:"trigger"`
		Solution     string `json:"solution"`
		Verification string `json:"verification"`
		Source       string `json:"source"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return memory.Item{}, fmt.Errorf("decode experience arguments: %w", err)
	}
	for _, value := range []string{args.Title, args.Trigger, args.Solution, args.Verification} {
		if strings.TrimSpace(value) == "" {
			return memory.Item{}, fmt.Errorf("experience fields are required")
		}
	}
	return memory.Item{Kind: memory.KindExperience, Text: fmt.Sprintf("%s | trigger: %s | solution: %s | verification: %s", strings.TrimSpace(args.Title), strings.TrimSpace(args.Trigger), strings.TrimSpace(args.Solution), strings.TrimSpace(args.Verification)), Source: strings.TrimSpace(args.Source), Status: "candidate"}, nil
}

func formatExperience(item memory.Item, status string) string {
	return fmt.Sprintf("Experience candidate (%s): %s\n请用户确认后再调用 ExperienceSave。", status, item.Text)
}
