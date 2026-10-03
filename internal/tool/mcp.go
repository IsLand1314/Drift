package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type MCPTool interface {
	Tool
	MCPServer() string
}

type mcpTool struct {
	server string
	remote string
	name   string
	desc   string
	schema json.RawMessage
	client MCPClient
}

func AttachMCP(value Registry, server string, client MCPClient) error {
	registry, ok := value.(*registry)
	if !ok {
		return errors.New("tool: MCP requires chat registry")
	}
	if !mcpName.MatchString(server) || client == nil {
		return errors.New("tool: invalid MCP server")
	}
	tools, err := client.ListTools(context.Background())
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
		pending = append(pending, mcpTool{server: server, remote: remote.Name, name: name, desc: remote.Description, schema: append(json.RawMessage(nil), remote.InputSchema...), client: client})
	}
	for _, current := range pending {
		registry.add(current, false)
	}
	return nil
}

func (t mcpTool) Name() string      { return t.name }
func (t mcpTool) MCPServer() string { return t.server }
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
