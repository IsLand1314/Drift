package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

type QuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Question struct {
	Question      string
	Options       []QuestionOption
	MultiSelect   bool
	AllowFreeText bool
}

type QuestionAnswer struct {
	Selected  []string
	Text      string
	Cancelled bool
}

func (a QuestionAnswer) ToolResult() string {
	if a.Cancelled {
		return "AskUserQuestion cancelled by user"
	}
	parts := make([]string, 0, 2)
	if len(a.Selected) > 0 {
		parts = append(parts, "selected="+strings.Join(a.Selected, ","))
	}
	if strings.TrimSpace(a.Text) != "" {
		parts = append(parts, "text="+strings.TrimSpace(a.Text))
	}
	if len(parts) == 0 {
		return "AskUserQuestion answered with no selection"
	}
	return "AskUserQuestion answer: " + strings.Join(parts, " ")
}

type Questionable interface {
	Tool
	Question(rawArguments string) (Question, error)
}

type askUserQuestionTool struct{}

func (askUserQuestionTool) Name() string                   { return "AskUserQuestion" }
func (askUserQuestionTool) Definition() llm.ToolDefinition { return AskUserQuestionDefinition() }
func (askUserQuestionTool) Execute(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("AskUserQuestion requires interactive question handling")
}

func (askUserQuestionTool) Question(raw string) (Question, error) {
	var args struct {
		Question      string           `json:"question"`
		Options       []QuestionOption `json:"options"`
		MultiSelect   bool             `json:"multi_select"`
		AllowFreeText bool             `json:"allow_free_text"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return Question{}, fmt.Errorf("decode AskUserQuestion arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Question{}, fmt.Errorf("decode AskUserQuestion arguments: multiple JSON values")
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Question == "" || len([]rune(args.Question)) > 500 {
		return Question{}, fmt.Errorf("AskUserQuestion question is invalid")
	}
	if len(args.Options) == 0 && !args.AllowFreeText {
		return Question{}, fmt.Errorf("AskUserQuestion needs options or free text")
	}
	if len(args.Options) > 8 {
		return Question{}, fmt.Errorf("AskUserQuestion supports at most 8 options")
	}
	seen := make(map[string]struct{}, len(args.Options))
	for index := range args.Options {
		option := &args.Options[index]
		option.ID = strings.TrimSpace(option.ID)
		option.Label = strings.TrimSpace(option.Label)
		option.Description = strings.TrimSpace(option.Description)
		if option.ID == "" || option.Label == "" || len([]rune(option.ID)) > 64 || len([]rune(option.Label)) > 120 {
			return Question{}, fmt.Errorf("AskUserQuestion option %d is invalid", index+1)
		}
		if _, exists := seen[option.ID]; exists {
			return Question{}, fmt.Errorf("AskUserQuestion option IDs must be unique")
		}
		seen[option.ID] = struct{}{}
	}
	return Question{Question: args.Question, Options: args.Options, MultiSelect: args.MultiSelect, AllowFreeText: args.AllowFreeText}, nil
}

func AskUserQuestionDefinition() llm.ToolDefinition {
	return controlDefinition("AskUserQuestion", "Ask the user a structured clarification question. This never grants permission or changes sandbox policy.", map[string]any{
		"question":        map[string]any{"type": "string", "description": "Question shown to the user."},
		"options":         map[string]any{"type": "array", "description": "Optional choices with stable IDs.", "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}}, "required": []string{"id", "label"}, "additionalProperties": false}},
		"multi_select":    map[string]any{"type": "boolean", "description": "Whether the user may choose multiple options."},
		"allow_free_text": map[string]any{"type": "boolean", "description": "Whether the user may provide free text instead of a listed option."},
	}, []string{"question"})
}

type toolSummary struct {
	Name        string
	Category    string
	Description string
}

type toolSearchTool struct{ registry *registry }

func (t toolSearchTool) Name() string                   { return "ToolSearch" }
func (t toolSearchTool) Definition() llm.ToolDefinition { return ToolSearchDefinition() }
func (t toolSearchTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		Query string   `json:"query"`
		Load  []string `json:"load"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("decode ToolSearch arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return "", fmt.Errorf("decode ToolSearch arguments: multiple JSON values")
	}
	query := strings.TrimSpace(args.Query)
	if query == "" || len([]rune(query)) > 120 {
		return "", fmt.Errorf("ToolSearch query is invalid")
	}
	matches := t.registry.search(query)
	if len(matches) == 0 {
		return "ToolSearch: no matching tools", nil
	}
	matchSet := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		matchSet[match.Name] = struct{}{}
	}
	for _, name := range args.Load {
		if _, ok := matchSet[name]; !ok {
			return "", fmt.Errorf("ToolSearch cannot load unmatched tool %q", name)
		}
		t.registry.enable(name)
	}
	var out strings.Builder
	out.WriteString("ToolSearch matches:\n")
	for _, match := range matches {
		out.WriteString("- ")
		out.WriteString(match.Name)
		out.WriteString(" [")
		out.WriteString(match.Category)
		out.WriteString("]: ")
		out.WriteString(match.Description)
		out.WriteByte('\n')
	}
	if len(args.Load) > 0 {
		loaded := append([]string(nil), args.Load...)
		sort.Strings(loaded)
		out.WriteString("Loaded schemas: ")
		out.WriteString(strings.Join(loaded, ", "))
	}
	return strings.TrimSpace(out.String()), nil
}

func ToolSearchDefinition() llm.ToolDefinition {
	return controlDefinition("ToolSearch", "Search available tools by name, category, or description. Include exact names in load to make their full schemas available on the next model request.", map[string]any{
		"query": map[string]any{"type": "string", "description": "Keywords describing the needed capability."},
		"load":  map[string]any{"type": "array", "description": "Optional exact names from this search result whose schemas should be loaded.", "items": map[string]any{"type": "string"}},
	}, []string{"query"})
}

func controlDefinition(name, description string, properties map[string]any, required []string) llm.ToolDefinition {
	if required == nil {
		required = []string{}
	}
	function := map[string]any{"name": name, "description": description, "parameters": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal control definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}
