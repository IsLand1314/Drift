// Package tool contains the tools exposed to the agent loop.
package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

// MaxReadBytes 限制单个文件进入模型上下文的最大大小。
const MaxReadBytes = 128 << 10

type readFileTool struct{}

func (readFileTool) Name() string {
	return "read_file"
}

func (readFileTool) Definition() llm.ToolDefinition {
	return ReadDefinition()
}

func (readFileTool) Execute(ctx context.Context, root string, rawArguments string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return Read(root, rawArguments)
}

type readArguments struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset"`
	Limit  *int   `json:"limit"`
}

// ReadDefinition 返回给模型的唯一工具 schema；它只描述“读取文件”，不包含写入或执行能力。
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
				"offset": map[string]any{
					"type":        "integer",
					"description": "Optional zero-based line offset for reading a page.",
					"default":     0,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Optional maximum number of lines in a page.",
					"default":     2000,
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

// Read 在 workspace 内安全读取一个普通文件。
// 校验顺序是：JSON 参数 → 相对路径 → dotenv/软链接保护 → 普通文件 → 大小限制。
// 调用方会把这里的错误脱敏后再交给模型。
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
	if err := validateRelativePath(args.Path, false); err != nil {
		return "", fmt.Errorf("read_file path is invalid: %w", err)
	}
	if IsDotEnvCredentialFile(filepath.Base(args.Path)) {
		// .env 即使被 .gitignore 忽略，也可能包含 API Key，不能进入模型上下文。
		return "", fmt.Errorf("read_file target is restricted")
	}

	workspace, err := os.OpenRoot(root)
	if err != nil {
		return "", fmt.Errorf("open read_file root: %w", err)
	}
	defer workspace.Close()
	hasSymlink, err := hasSymlinkComponent(workspace, args.Path)
	if err != nil {
		return "", fmt.Errorf("stat read_file target: %w", err)
	}
	if hasSymlink {
		// 禁止工作区内软链接，避免用别名绕过敏感文件名或路径边界。
		return "", fmt.Errorf("read_file target is not a regular file")
	}

	file, err := workspace.Open(args.Path)
	if err != nil {
		return "", fmt.Errorf("open read_file target: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat read_file target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read_file target is not a regular file")
	}
	if args.Offset != nil || args.Limit != nil {
		offset, limit := 0, 2000
		if args.Offset != nil {
			offset = *args.Offset
		}
		if args.Limit != nil {
			limit = *args.Limit
		}
		if offset < 0 {
			return "", fmt.Errorf("read_file offset must be greater than or equal to 0")
		}
		if limit <= 0 {
			return "", fmt.Errorf("read_file limit must be greater than 0")
		}
		return readPage(file, offset, limit)
	}
	content, err := io.ReadAll(io.LimitReader(file, MaxReadBytes+1))
	if err != nil {
		return "", fmt.Errorf("read_file target: %w", err)
	}
	if len(content) > MaxReadBytes {
		return "", fmt.Errorf("read_file target exceeds %d bytes; use offset and limit to read a page", MaxReadBytes)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return "", fmt.Errorf("read_file target is binary")
	}
	return string(content), nil
}

func readPage(file *os.File, offset, limit int) (string, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), MaxReadBytes+1)
	lineNumber, selected := 0, make([]string, 0, limit)
	for scanner.Scan() {
		if strings.IndexByte(scanner.Text(), 0) >= 0 {
			return "", fmt.Errorf("read_file target is binary")
		}
		if lineNumber >= offset {
			selected = append(selected, scanner.Text())
			if len(selected) == limit {
				break
			}
		}
		lineNumber++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read_file target: %w", err)
	}
	if len(selected) == 0 {
		return "", nil
	}
	result := "read_file: lines " + strconv.Itoa(offset+1) + "-" + strconv.Itoa(offset+len(selected)) + "\n" + strings.Join(selected, "\n")
	if len(selected) == limit && scanner.Scan() {
		nextOffset := offset + len(selected)
		result += "\nread_file: more lines available; next offset: " + strconv.Itoa(nextOffset)
	}
	if len(result) > MaxReadBytes {
		return "", fmt.Errorf("read_file page exceeds %d bytes; reduce limit", MaxReadBytes)
	}
	return result, nil
}

// IsDotEnvCredentialFile reports whether a filename is a dotenv credential file.
func IsDotEnvCredentialFile(name string) bool {
	name = strings.ToLower(name)
	return name == ".env" || strings.HasPrefix(name, ".env.")
}

func hasSymlinkComponent(root *os.Root, path string) (bool, error) {
	// 逐级 Lstat，而不是只检查最终文件名，覆盖“软链接目录/普通文件”的组合路径。
	clean := filepath.Clean(path)
	current := ""
	for _, component := range strings.Split(filepath.ToSlash(clean), "/") {
		if component == "" || component == "." {
			continue
		}
		if current == "" {
			current = component
		} else {
			current = filepath.Join(current, component)
		}
		info, err := root.Lstat(current)
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}
