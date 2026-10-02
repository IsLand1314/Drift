package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/changes"
	"github.com/IsLand1314/Drift/internal/llm"
)

type deleteFileTool struct{}

func (deleteFileTool) Name() string                   { return "delete_file" }
func (deleteFileTool) Definition() llm.ToolDefinition { return DeleteDefinition() }
func (deleteFileTool) Execute(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("delete_file requires permission confirmation")
}
func (deleteFileTool) Preview(ctx context.Context, root, raw string) (Preview, error) {
	return Delete(root, raw)
}
func (deleteFileTool) ExecutePreview(ctx context.Context, root string, preview Preview) (string, error) {
	return CommitDelete(ctx, root, preview)
}

func DeleteDefinition() llm.ToolDefinition {
	definition := map[string]any{"name": "delete_file", "description": "Delete one regular file below the workspace after user confirmation.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}, "additionalProperties": false}}
	raw, err := json.Marshal(definition)
	if err != nil {
		panic(err)
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

func Delete(root, raw string) (Preview, error) {
	var args struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return Preview{}, fmt.Errorf("decode delete_file arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Preview{}, fmt.Errorf("decode delete_file arguments: multiple JSON values")
	}
	args.Path = strings.TrimSpace(args.Path)
	if err := validateWritePath(args.Path); err != nil {
		return Preview{}, fmt.Errorf("delete_file path is invalid: %w", err)
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return Preview{}, fmt.Errorf("open delete_file root: %w", err)
	}
	defer workspace.Close()
	info, err := workspace.Lstat(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Preview{}, fmt.Errorf("delete_file target does not exist")
		}
		return Preview{}, fmt.Errorf("stat delete_file target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Preview{}, fmt.Errorf("delete_file target is not a regular file")
	}
	old, err := workspace.ReadFile(args.Path)
	if err != nil {
		return Preview{}, fmt.Errorf("read delete_file target: %w", err)
	}
	if bytesContainNUL(old) {
		return Preview{}, fmt.Errorf("delete_file target is binary")
	}
	return Preview{Operation: "delete_file", Content: old, Before: append([]byte(nil), old...), BeforeExists: true, Path: args.Path, OldBytes: len(old), NewBytes: 0, Diff: writeDiff(args.Path, old, nil)}, nil
}

func CommitDelete(ctx context.Context, root string, preview Preview) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return "", fmt.Errorf("open delete_file root: %w", err)
	}
	defer workspace.Close()
	info, err := workspace.Lstat(preview.Path)
	if err != nil {
		return "", fmt.Errorf("delete_file target changed: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("delete_file target changed")
	}
	id, err := changes.NewID()
	if err != nil {
		return "", fmt.Errorf("delete_file operation id: %w", err)
	}
	manifest := changes.Manifest{Operation: preview.Operation, Path: preview.Path, OldBytes: preview.OldBytes, NewBytes: 0, Decision: "allow"}
	if set := changes.FromContext(ctx); set != nil {
		if err := set.Record(manifest, preview.Diff, preview.Content, preview.Path+".before"); err != nil {
			return "", err
		}
	} else if _, err := changes.Record(root, time.Now().UTC(), id, manifest, preview.Diff, preview.Content, preview.Path+".before"); err != nil {
		return "", err
	}
	if err := workspace.Remove(preview.Path); err != nil {
		return "", fmt.Errorf("delete_file remove target: %w", err)
	}
	return "delete_file: deleted " + preview.Path, nil
}
