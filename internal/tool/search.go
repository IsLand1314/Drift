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
	"regexp"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

const (
	MaxSearchFiles   = 200
	MaxSearchMatches = 100
	MaxSearchBytes   = 32 << 10
)

const searchTruncatedMarker = "Grep: results truncated at configured limit"

type searchTextTool struct{}

func (searchTextTool) Name() string                   { return "Grep" }
func (searchTextTool) Definition() llm.ToolDefinition { return SearchDefinition() }
func (searchTextTool) Execute(ctx context.Context, root, rawArguments string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return searchWithContext(ctx, root, rawArguments)
}

type searchArguments struct {
	Pattern string
	Path    string
	Include string
}

func decodeSearchArguments(raw string) (searchArguments, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("arguments must be a JSON object")
		}
		return searchArguments{}, fmt.Errorf("decode Grep arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return searchArguments{}, fmt.Errorf("decode Grep arguments: multiple JSON values")
		}
		return searchArguments{}, fmt.Errorf("decode Grep arguments: %w", err)
	}
	for name := range fields {
		if name != "pattern" && name != "path" && name != "include" {
			return searchArguments{}, fmt.Errorf("decode Grep arguments: unknown field %q", name)
		}
	}
	var args searchArguments
	rawPattern, ok := fields["pattern"]
	if !ok || string(rawPattern) == "null" || json.Unmarshal(rawPattern, &args.Pattern) != nil || args.Pattern == "" {
		return searchArguments{}, fmt.Errorf("Grep pattern must be a non-empty string")
	}
	if rawPath, ok := fields["path"]; ok {
		if string(rawPath) == "null" || json.Unmarshal(rawPath, &args.Path) != nil {
			return searchArguments{}, fmt.Errorf("Grep path must be a string")
		}
	}
	if rawInclude, ok := fields["include"]; ok {
		if string(rawInclude) == "null" || json.Unmarshal(rawInclude, &args.Include) != nil {
			return searchArguments{}, fmt.Errorf("Grep include must be a string")
		}
	}
	return args, nil
}

func SearchDefinition() llm.ToolDefinition {
	function := map[string]any{"name": "Grep", "description": "Search regular files with a regular expression.", "parameters": map[string]any{
		"type": "object", "properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Regular expression to find."},
			"path":    map[string]any{"type": "string", "description": "Optional relative directory below the workspace root."},
			"include": map[string]any{"type": "string", "description": "Optional filename glob such as *.go."},
		}, "required": []string{"pattern"}, "additionalProperties": false,
	}}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal Grep definition: " + err.Error())
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
		return "", fmt.Errorf("Grep path is invalid: %w", err)
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("Grep pattern is invalid: %w", err)
	}
	if args.Include != "" {
		if _, err := path.Match(args.Include, ""); err != nil {
			return "", fmt.Errorf("Grep include is invalid: %w", err)
		}
	}
	workspace, err := openWorkspace(root)
	if err != nil {
		return "", fmt.Errorf("open Grep root: %w", err)
	}
	defer workspace.Close()
	var out strings.Builder
	files, matches := 0, 0
	truncated := false
	stop := errorsSearchStop{}
	err = walkRegularFilesContext(ctx, workspace, args.Path, func(filePath string, _ fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		files++
		if files > MaxSearchFiles {
			truncated = true
			return stop
		}
		if args.Include != "" {
			matched, matchErr := path.Match(args.Include, path.Base(filePath))
			if matchErr != nil {
				return fmt.Errorf("Grep include is invalid: %w", matchErr)
			}
			if !matched {
				return nil
			}
		}
		file, err := workspace.Open(filePath)
		if err != nil {
			return fmt.Errorf("open %s: %w", filePath, err)
		}
		defer file.Close()
		prefix, err := io.ReadAll(io.LimitReader(file, 64<<10))
		if err != nil {
			return fmt.Errorf("read %s: %w", filePath, err)
		}
		if strings.IndexByte(string(prefix), 0) >= 0 {
			return nil
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek %s: %w", filePath, err)
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
			if !re.MatchString(line) {
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
			if out.Len()+len(record)+1+len(searchTruncatedMarker) > MaxSearchBytes {
				truncated = true
				return stop
			}
			out.WriteString(record)
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan %s: %w", filePath, err)
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return "", fmt.Errorf("Grep: %w", err)
	}
	if truncated {
		marker := searchTruncatedMarker
		if out.Len() > 0 {
			marker = "\n" + marker
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
