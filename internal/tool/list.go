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

func (listFilesTool) Name() string                   { return "Glob" }
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
		"name":        "Glob",
		"description": "List regular files below a workspace directory.",
		"parameters": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"path": map[string]any{"type": "string"}},
			"additionalProperties": false,
		},
	}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal Glob definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

func List(root, rawArguments string) (string, error) {
	args, err := decodeListArguments(rawArguments)
	if err != nil {
		return "", err
	}
	if err := validateRelativePath(args.Path, true); err != nil {
		return "", fmt.Errorf("Glob path is invalid: %w", err)
	}
	workspace, err := openWorkspace(root)
	if err != nil {
		return "", fmt.Errorf("open Glob root: %w", err)
	}
	defer workspace.Close()
	paths := make([]string, 0, MaxListEntries)
	if err := walkRegularFiles(workspace, args.Path, func(filePath string, _ fs.DirEntry) error {
		paths = append(paths, path.Clean(strings.ReplaceAll(filePath, `\`, "/")))
		return nil
	}); err != nil {
		return "", fmt.Errorf("Glob: %w", err)
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
		result += "Glob: results truncated at 200 entries"
	}
	return result, nil
}

func decodeListArguments(rawArguments string) (listArguments, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(rawArguments))
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("arguments must be a JSON object")
		}
		return listArguments{}, fmt.Errorf("decode Glob arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return listArguments{}, fmt.Errorf("decode Glob arguments: multiple JSON values")
		}
		return listArguments{}, fmt.Errorf("decode Glob arguments: %w", err)
	}
	for name := range fields {
		if name != "path" {
			return listArguments{}, fmt.Errorf("decode Glob arguments: unknown field %q", name)
		}
	}
	var args listArguments
	if rawPath, ok := fields["path"]; ok {
		if string(rawPath) == "null" {
			return listArguments{}, fmt.Errorf("decode Glob arguments: path must be a string")
		}
		if err := json.Unmarshal(rawPath, &args.Path); err != nil {
			return listArguments{}, fmt.Errorf("decode Glob arguments: path must be a string: %w", err)
		}
	}
	return args, nil
}
