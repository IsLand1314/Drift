// Package tool contains the tools exposed to the agent loop.
package tool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitee.com/island0920/drift/internal/llm"
)

const MaxReadBytes = 128 << 10

type readArguments struct {
	Path string `json:"path"`
}

// ReadDefinition returns the schema for the bounded read_file tool.
func ReadDefinition() llm.ToolDefinition {
	function := map[string]any{
		"name":        "read_file",
		"description": "Read a regular file below the workspace root.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "A relative path to a file below the workspace root.",
				},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal read_file definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

// Read reads one regular file below root, with a strict argument shape and a size limit.
func Read(root, rawArguments string) (string, error) {
	var args readArguments
	decoder := json.NewDecoder(strings.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("decode read_file arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode read_file arguments: multiple JSON values")
		}
		return "", fmt.Errorf("decode read_file arguments: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("read_file path is blank")
	}
	if filepath.IsAbs(args.Path) {
		return "", fmt.Errorf("read_file path must be relative")
	}

	resolvedRoot, err := resolvePath(root)
	if err != nil {
		return "", fmt.Errorf("resolve read_file root: %w", err)
	}
	resolvedTarget, err := resolvePath(filepath.Join(resolvedRoot, args.Path))
	if err != nil {
		return "", fmt.Errorf("resolve read_file target: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return "", fmt.Errorf("check read_file containment: %w", err)
	}
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("read_file path escapes root")
	}

	info, err := os.Stat(resolvedTarget)
	if err != nil {
		return "", fmt.Errorf("stat read_file target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read_file target is not a regular file")
	}
	file, err := os.Open(resolvedTarget)
	if err != nil {
		return "", fmt.Errorf("open read_file target: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, MaxReadBytes+1))
	if err != nil {
		return "", fmt.Errorf("read_file target: %w", err)
	}
	if len(content) > MaxReadBytes {
		return "", fmt.Errorf("read_file target exceeds %d bytes", MaxReadBytes)
	}
	return string(content), nil
}

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
