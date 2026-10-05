package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/mcp"
)

var mcpName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

type MCPClient interface {
	ListTools(context.Context) ([]mcp.Tool, error)
	CallTool(context.Context, string, json.RawMessage) (mcp.Result, error)
}

type MCPContentClient interface {
	ListResources(context.Context) ([]mcp.Resource, error)
	ReadResource(context.Context, string) ([]mcp.ContentBlock, error)
	ListPrompts(context.Context) ([]mcp.Prompt, error)
	GetPrompt(context.Context, string, map[string]string) ([]mcp.ContentBlock, error)
}

type MCPTool interface {
	Tool
	MCPServer() string
}

type ReadOnlyMCPTool interface {
	MCPTool
	ReadOnly() bool
}

type mcpTool struct {
	server   string
	remote   string
	name     string
	desc     string
	schema   json.RawMessage
	client   MCPClient
	readOnly bool
}

func AttachMCP(ctx context.Context, value Registry, server string, client MCPClient) error {
	registry, ok := value.(*registry)
	if !ok {
		return errors.New("tool: MCP requires chat registry")
	}
	if !mcpName.MatchString(server) || client == nil {
		return errors.New("tool: invalid MCP server")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("tool: list MCP tools: %w", err)
	}
	pending := make([]mcpTool, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, remote := range tools {
		if !mcpName.MatchString(remote.Name) || len(remote.Description) > 1000 || len(remote.InputSchema) == 0 || !json.Valid(remote.InputSchema) {
			return errors.New("tool: invalid MCP tool")
		}
		var schema map[string]any
		if json.Unmarshal(remote.InputSchema, &schema) != nil || schema["type"] != "object" {
			return errors.New("tool: MCP input schema must be an object")
		}
		name := "mcp__" + server + "__" + remote.Name
		if _, exists := seen[name]; exists {
			return fmt.Errorf("tool: duplicate MCP tool %q", name)
		}
		if _, exists := registry.tools[name]; exists {
			return fmt.Errorf("tool: duplicate MCP tool %q", name)
		}
		seen[name] = struct{}{}
		pending = append(pending, mcpTool{server: server, remote: remote.Name, name: name, desc: remote.Description, schema: append(json.RawMessage(nil), remote.InputSchema...), client: client, readOnly: remote.Annotations != nil && remote.Annotations.ReadOnlyHint})
	}
	for _, current := range pending {
		registry.add(current, false)
	}
	if content, ok := client.(MCPContentClient); ok {
		registry.add(mcpResourceTool{server: server, client: content}, false)
		registry.add(mcpPromptTool{server: server, client: content}, false)
	}
	return nil
}

func (t mcpTool) Name() string      { return t.name }
func (t mcpTool) MCPServer() string { return t.server }
func (t mcpTool) ReadOnly() bool    { return t.readOnly }
func (t mcpTool) Definition() llm.ToolDefinition {
	function := map[string]any{"name": t.name, "description": t.desc, "parameters": json.RawMessage(t.schema)}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: encode MCP definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}
func (t mcpTool) Execute(context.Context, string, string) (string, error) {
	return "", errors.New("MCP tool requires permission confirmation")
}
func (t mcpTool) Preview(_ context.Context, _ string, raw string) (Preview, error) {
	if len(raw) == 0 || len(raw) > maxMCPArgumentsBytes {
		return Preview{}, errors.New("MCP tool arguments are invalid")
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return Preview{}, errors.New("MCP tool arguments are invalid")
	}
	return Preview{Operation: "mcp_call", Path: t.server + "/" + t.remote, NewBytes: len(raw), Content: []byte(raw)}, nil
}
func (t mcpTool) ExecutePreview(ctx context.Context, _ string, preview Preview) (string, error) {
	result, err := t.client.CallTool(ctx, t.remote, json.RawMessage(preview.Content))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

const maxMCPArgumentsBytes = 32 << 10

type mcpResourceTool struct {
	server string
	client MCPContentClient
}

func (t mcpResourceTool) Name() string { return "mcp__" + t.server + "__resource_read" }
func (t mcpResourceTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Read a resource from an MCP server. Read-only and untrusted.", map[string]any{"uri": map[string]any{"type": "string"}}, []string{"uri"})
}
func (t mcpResourceTool) Execute(ctx context.Context, root string, raw string) (string, error) {
	var args struct {
		URI string `json:"uri"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil || strings.TrimSpace(args.URI) == "" {
		return "", errors.New("MCP resource arguments are invalid")
	}
	if err := validateResourceURI(root, args.URI); err != nil {
		return "", err
	}
	blocks, err := t.client.ReadResource(ctx, args.URI)
	if err != nil {
		return "", err
	}
	text, err := mcp.FormatContent(blocks)
	if err != nil {
		return "", err
	}
	return "[untrusted MCP resource]\n" + text, nil
}

type mcpPromptTool struct {
	server string
	client MCPContentClient
}

func (t mcpPromptTool) Name() string { return "mcp__" + t.server + "__prompt_get" }
func (t mcpPromptTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Get a prompt from an MCP server as untrusted context.", map[string]any{"name": map[string]any{"type": "string"}, "arguments": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}, []string{"name"})
}
func (t mcpPromptTool) Execute(ctx context.Context, _ string, raw string) (string, error) {
	var args struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil || !mcpName.MatchString(args.Name) {
		return "", errors.New("MCP prompt arguments are invalid")
	}
	blocks, err := t.client.GetPrompt(ctx, args.Name, args.Arguments)
	if err != nil {
		return "", err
	}
	text, err := mcp.FormatContent(blocks)
	if err != nil {
		return "", err
	}
	return "[untrusted MCP prompt]\n" + text, nil
}

func validateResourceURI(root, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("MCP resource URI is invalid")
	}
	if parsed.Scheme != "file" {
		return nil
	}
	path := parsed.Path
	if path == "" {
		return errors.New("MCP resource URI is invalid")
	}
	absolute, err := filepath.Abs(filepath.FromSlash(path))
	if err != nil {
		return errors.New("MCP resource URI is invalid")
	}
	workspace, err := filepath.Abs(root)
	if err != nil {
		return errors.New("MCP workspace is invalid")
	}
	rel, err := filepath.Rel(workspace, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("MCP resource is outside workspace")
	}
	return nil
}
