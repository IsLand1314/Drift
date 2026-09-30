package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

const MaxListEntries = 200

type listFilesTool struct{}

func (listFilesTool) Name() string                   { return "list_files" }
func (listFilesTool) Definition() llm.ToolDefinition { return ListDefinition() }
func (listFilesTool) Execute(ctx context.Context, root, rawArguments string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return List(root, rawArguments)
}

type listArguments struct {
	Path string `json:"path"`
}

func ListDefinition() llm.ToolDefinition {
	function := map[string]any{
		"name":        "list_files",
		"description": "List regular files below a workspace directory.",
		"parameters": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"path": map[string]any{"type": "string"}},
			"additionalProperties": false,
		},
	}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal list_files definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

func List(root, rawArguments string) (string, error) {
	var args listArguments
	decoder := json.NewDecoder(strings.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("decode list_files arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode list_files arguments: multiple JSON values")
		}
		return "", fmt.Errorf("decode list_files arguments: %w", err)
	}
	if err := validateRelativePath(args.Path, true); err != nil {
		return "", fmt.Errorf("list_files path is invalid: %w", err)
	}
	workspace, err := openWorkspace(root)
	if err != nil {
		return "", fmt.Errorf("open list_files root: %w", err)
	}
	defer workspace.Close()
	paths := make([]string, 0, MaxListEntries)
	if err := walkRegularFiles(workspace, args.Path, func(filePath string, _ fs.DirEntry) error {
		paths = append(paths, path.Clean(strings.ReplaceAll(filePath, `\`, "/")))
		return nil
	}); err != nil {
		return "", fmt.Errorf("list_files: %w", err)
	}
	sort.Strings(paths)
	truncated := len(paths) > MaxListEntries
	if truncated {
		paths = paths[:MaxListEntries]
	}
	result := strings.Join(paths, "\n")
	if truncated {
		if result != "" {
			result += "\n"
		}
		result += "list_files: results truncated at 200 entries"
	}
	return result, nil
}
