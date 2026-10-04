package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

type editFileTool struct{ cache *fileStateCache }

func (editFileTool) Name() string                   { return "EditFile" }
func (editFileTool) Definition() llm.ToolDefinition { return EditDefinition() }
func (editFileTool) Execute(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("EditFile requires permission confirmation")
}

func (t editFileTool) Preview(ctx context.Context, root, raw string) (Preview, error) {
	if t.cache != nil {
		if path := readPath(raw); path != "" {
			if err := t.cache.Check(root, path); err != nil {
				return Preview{}, fmt.Errorf("EditFile %s: %w", path, err)
			}
		}
	}
	return Edit(root, raw)
}
func (t editFileTool) ExecutePreview(ctx context.Context, root string, preview Preview) (string, error) {
	result, err := CommitEdit(ctx, root, preview)
	if err == nil && t.cache != nil {
		t.cache.Invalidate(root, preview.Path)
	}
	return result, err
}

func EditDefinition() llm.ToolDefinition {
	definition := map[string]any{"name": "EditFile", "description": "Replace one exact text match in a UTF-8 file below the workspace after user confirmation.", "parameters": map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"}, "old_text": map[string]any{"type": "string"}, "new_text": map[string]any{"type": "string"},
	}, "required": []string{"path", "old_text", "new_text"}, "additionalProperties": false}}
	raw, err := json.Marshal(definition)
	if err != nil {
		panic(err)
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

type editArguments struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func Edit(root, raw string) (Preview, error) {
	var args editArguments
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return Preview{}, fmt.Errorf("decode EditFile arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Preview{}, fmt.Errorf("decode EditFile arguments: multiple JSON values")
	}
	args.Path = strings.TrimSpace(args.Path)
	if err := validateWritePath(args.Path); err != nil {
		return Preview{}, fmt.Errorf("EditFile path is invalid: %w", err)
	}
	if args.OldText == "" {
		return Preview{}, fmt.Errorf("EditFile old_text is empty")
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return Preview{}, fmt.Errorf("open EditFile root: %w", err)
	}
	defer workspace.Close()
	info, err := workspace.Lstat(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Preview{}, fmt.Errorf("EditFile target does not exist")
		}
		return Preview{}, fmt.Errorf("stat EditFile target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Preview{}, fmt.Errorf("EditFile target is not a regular file")
	}
	old, err := workspace.ReadFile(args.Path)
	if err != nil {
		return Preview{}, fmt.Errorf("read EditFile target: %w", err)
	}
	if bytesContainNUL(old) {
		return Preview{}, fmt.Errorf("EditFile target is binary")
	}
	text := string(old)
	if count := strings.Count(text, args.OldText); count != 1 {
		return Preview{}, fmt.Errorf("EditFile old_text must match exactly once (matched %d)", count)
	}
	next := []byte(strings.Replace(text, args.OldText, args.NewText, 1))
	if len(next) > MaxWriteBytes {
		return Preview{}, fmt.Errorf("EditFile content exceeds %d bytes", MaxWriteBytes)
	}
	return Preview{Operation: "edit_file", Path: args.Path, Content: next, Before: append([]byte(nil), old...), BeforeExists: true, OldBytes: len(old), NewBytes: len(next), Diff: writeDiff(args.Path, old, next)}, nil
}

func CommitEdit(ctx context.Context, root string, preview Preview) (string, error) {
	return commitTextPreview(ctx, root, preview)
}
