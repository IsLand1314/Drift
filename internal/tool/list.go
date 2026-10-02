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
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func ListDefinition() llm.ToolDefinition {
	function := map[string]any{
		"name":        "Glob",
		"description": "Find regular files below the workspace root by glob pattern.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern such as **/*.go."},
				"path":    map[string]any{"type": "string", "description": "Optional relative search root."},
			},
			"required":             []string{"pattern"},
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
	if args.Pattern == "" {
		return "", fmt.Errorf("Glob pattern must be a non-empty string")
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
		filePath = path.Clean(strings.ReplaceAll(filePath, `\`, "/"))
		if globMatch(args.Pattern, filePath) {
			paths = append(paths, filePath)
		}
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
		if name != "pattern" && name != "path" {
			return listArguments{}, fmt.Errorf("decode Glob arguments: unknown field %q", name)
		}
	}
	var args listArguments
	rawPattern, ok := fields["pattern"]
	if !ok || string(rawPattern) == "null" || json.Unmarshal(rawPattern, &args.Pattern) != nil || args.Pattern == "" {
		return listArguments{}, fmt.Errorf("decode Glob arguments: pattern must be a non-empty string")
	}
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

func globMatch(pattern, name string) bool {
	pattern = path.Clean(strings.ReplaceAll(pattern, `\`, "/"))
	name = path.Clean(strings.ReplaceAll(name, `\`, "/"))
	return globSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func globSegments(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		return globSegments(pattern[1:], name) || (len(name) > 0 && globSegments(pattern, name[1:]))
	}
	if len(name) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], name[0])
	return err == nil && matched && globSegments(pattern[1:], name[1:])
}
