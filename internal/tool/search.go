package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

const (
	MaxSearchFiles   = 200
	MaxSearchMatches = 100
	MaxSearchBytes   = 32 << 10
)

type searchTextTool struct{}

func (searchTextTool) Name() string                   { return "search_text" }
func (searchTextTool) Definition() llm.ToolDefinition { return SearchDefinition() }
func (searchTextTool) Execute(ctx context.Context, root, rawArguments string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return searchWithContext(ctx, root, rawArguments)
}

type searchArguments struct {
	Query string
	Path  string
}

func decodeSearchArguments(raw string) (searchArguments, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("arguments must be a JSON object")
		}
		return searchArguments{}, fmt.Errorf("decode search_text arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return searchArguments{}, fmt.Errorf("decode search_text arguments: multiple JSON values")
		}
		return searchArguments{}, fmt.Errorf("decode search_text arguments: %w", err)
	}
	for name := range fields {
		if name != "query" && name != "path" {
			return searchArguments{}, fmt.Errorf("decode search_text arguments: unknown field %q", name)
		}
	}
	var args searchArguments
	rawQuery, ok := fields["query"]
	if !ok || string(rawQuery) == "null" || json.Unmarshal(rawQuery, &args.Query) != nil || args.Query == "" {
		return searchArguments{}, fmt.Errorf("search_text query must be a non-empty string")
	}
	if rawPath, ok := fields["path"]; ok {
		if string(rawPath) == "null" || json.Unmarshal(rawPath, &args.Path) != nil {
			return searchArguments{}, fmt.Errorf("search_text path must be a string")
		}
	}
	return args, nil
}

func SearchDefinition() llm.ToolDefinition {
	function := map[string]any{"name": "search_text", "description": "Search literal text in regular files below the workspace root.", "parameters": map[string]any{
		"type": "object", "properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Literal text to find."},
			"path":  map[string]any{"type": "string", "description": "Optional relative directory below the workspace root."},
		}, "required": []string{"query"}, "additionalProperties": false,
	}}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal search_text definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

func Search(root, rawArguments string) (string, error) {
	return searchWithContext(context.Background(), root, rawArguments)
}

func searchWithContext(ctx context.Context, root, rawArguments string) (string, error) {
	args, err := decodeSearchArguments(rawArguments)
	if err != nil {
		return "", err
	}
	if err := validateRelativePath(args.Path, true); err != nil {
		return "", fmt.Errorf("search_text path is invalid: %w", err)
	}
	workspace, err := openWorkspace(root)
	if err != nil {
		return "", fmt.Errorf("open search_text root: %w", err)
	}
	defer workspace.Close()
	var out strings.Builder
	files, matches := 0, 0
	truncated := false
	stop := errorsSearchStop{}
	err = walkRegularFiles(workspace, args.Path, func(filePath string, _ fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		files++
		if files > MaxSearchFiles {
			truncated = true
			return stop
		}
		file, err := workspace.Open(filePath)
		if err != nil {
			return nil
		}
		defer file.Close()
		prefix, err := io.ReadAll(io.LimitReader(file, 64<<10))
		if err != nil || strings.IndexByte(string(prefix), 0) >= 0 {
			return nil
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64<<10), 64<<10)
		lineNo := 0
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			lineNo++
			line := scanner.Text()
			if !strings.Contains(line, args.Query) {
				continue
			}
			matches++
			if matches > MaxSearchMatches {
				truncated = true
				return stop
			}
			record := path.Clean(strings.ReplaceAll(filePath, `\`, "/")) + ":" + fmt.Sprint(lineNo) + ": " + string([]byte(line)[:min(240, len(line))])
			if out.Len() > 0 {
				record = "\n" + record
			}
			if out.Len()+len(record) > MaxSearchBytes {
				truncated = true
				return stop
			}
			out.WriteString(record)
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return "", fmt.Errorf("search_text: %w", err)
	}
	if truncated {
		marker := "search_text: results truncated at configured limit"
		if out.Len() > 0 {
			marker = "\n" + marker
		}
		if out.Len()+len(marker) > MaxSearchBytes {
			out.Reset()
			marker = marker[:min(len(marker), MaxSearchBytes)]
		}
		out.WriteString(marker)
	}
	return out.String(), nil
}

type errorsSearchStop struct{}

func (errorsSearchStop) Error() string { return "search stopped" }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
