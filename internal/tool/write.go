package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/changes"
	"github.com/IsLand1314/Drift/internal/llm"
)

const MaxWriteBytes = 128 << 10

type writeFileTool struct{}

func (writeFileTool) Name() string { return "write_file" }

func (writeFileTool) Definition() llm.ToolDefinition { return WriteDefinition() }

func (writeFileTool) Execute(ctx context.Context, root, rawArguments string) (string, error) {
	return "", fmt.Errorf("write_file requires permission confirmation")
}

func (writeFileTool) Preview(ctx context.Context, root, rawArguments string) (Preview, error) {
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	return Write(root, rawArguments)
}

func (writeFileTool) ExecutePreview(ctx context.Context, root string, preview Preview) (string, error) {
	return CommitWrite(ctx, root, preview)
}

func WriteDefinition() llm.ToolDefinition {
	function := map[string]any{
		"name":        "write_file",
		"description": "Create or overwrite a regular text file below the workspace root after user confirmation.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Workspace-relative file path."},
				"content": map[string]any{"type": "string", "description": "Complete UTF-8 text content."},
			},
			"required":             []string{"path", "content"},
			"additionalProperties": false,
		},
	}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal write_file definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

type writeArguments struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func Write(root, rawArguments string) (Preview, error) {
	var args writeArguments
	decoder := json.NewDecoder(strings.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return Preview{}, fmt.Errorf("decode write_file arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Preview{}, fmt.Errorf("decode write_file arguments: multiple JSON values")
	}
	args.Path = filepath.ToSlash(strings.TrimSpace(args.Path))
	if err := validateWritePath(args.Path); err != nil {
		return Preview{}, fmt.Errorf("write_file path is invalid: %w", err)
	}
	content := []byte(args.Content)
	if len(content) > MaxWriteBytes {
		return Preview{}, fmt.Errorf("write_file content exceeds %d bytes", MaxWriteBytes)
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return Preview{}, fmt.Errorf("open write_file root: %w", err)
	}
	defer workspace.Close()
	parent := filepath.ToSlash(filepath.Dir(args.Path))
	if parent == "." {
		parent = "."
	}
	if hasSymlink, err := hasSymlinkComponent(workspace, parent); err != nil {
		return Preview{}, fmt.Errorf("stat write_file parent: %w", err)
	} else if hasSymlink {
		return Preview{}, fmt.Errorf("write_file parent is not a regular directory")
	}
	parentInfo, err := workspace.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return Preview{}, fmt.Errorf("write_file parent directory does not exist")
	}
	old, operation := []byte(nil), "create_file"
	info, statErr := workspace.Lstat(args.Path)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return Preview{}, fmt.Errorf("write_file target is not a regular file")
		}
		old, err = workspace.ReadFile(args.Path)
		if err != nil {
			return Preview{}, fmt.Errorf("read write_file target: %w", err)
		}
		if bytesContainNUL(old) {
			return Preview{}, fmt.Errorf("write_file target is binary")
		}
		operation = "overwrite_file"
	} else if !os.IsNotExist(statErr) {
		return Preview{}, fmt.Errorf("stat write_file target: %w", statErr)
	}
	return Preview{Operation: operation, Path: args.Path, Content: append([]byte(nil), content...), OldBytes: len(old), NewBytes: len(content), Diff: writeDiff(args.Path, old, content)}, nil
}

func validateWritePath(path string) error {
	if err := validateRelativePath(path, false); err != nil {
		return err
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		switch strings.ToLower(part) {
		case ".git", ".drift", ".codex-temp", ".worktrees":
			return fmt.Errorf("path is protected")
		}
	}
	return nil
}

func CommitWrite(ctx context.Context, root string, preview Preview) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return "", fmt.Errorf("open write_file root: %w", err)
	}
	defer workspace.Close()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, err := changes.NewID()
	if err != nil {
		return "", fmt.Errorf("write_file operation id: %w", err)
	}
	started := time.Now().UTC()
	if _, err := changes.Record(root, started, id, changes.Manifest{Operation: preview.Operation, Path: preview.Path, OldBytes: preview.OldBytes, NewBytes: preview.NewBytes, Decision: "allow"}, preview.Diff, preview.Content, filepath.Base(filepath.FromSlash(preview.Path))); err != nil {
		return "", err
	}
	tempPath := filepath.Join(filepath.Dir(preview.Path), ".drift-write-"+id)
	temp, err := workspace.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("write_file temporary target: %w", err)
	}
	defer func() { _ = workspace.Remove(tempPath) }()
	if _, err := temp.Write(preview.Content); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("write_file temporary content: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("write_file temporary flush: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("write_file temporary close: %w", err)
	}
	if err := workspace.Rename(tempPath, preview.Path); err != nil {
		return "", fmt.Errorf("write_file replace target: %w", err)
	}
	return "write_file: " + preview.Operation + " " + preview.Path, nil
}

func bytesContainNUL(value []byte) bool {
	for _, b := range value {
		if b == 0 {
			return true
		}
	}
	return false
}

func writeDiff(path string, old, next []byte) string {
	if string(old) == string(next) {
		return "(no content change)"
	}
	const maxDiff = 16 << 10
	result := "--- " + path + "\n+++ " + path + "\n" + string(old) + "\n--- replacement ---\n" + string(next)
	if len(result) > maxDiff {
		return result[:maxDiff] + "\n[diff truncated]"
	}
	return result
}
