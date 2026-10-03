package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/mcp"
	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

type mcpManager struct {
	root     string
	registry tool.Registry
	config   mcp.Config
	clients  map[string]*mcp.Client
	audit    session.Writer
}

func newMCPManager(root string, registry tool.Registry) (*mcpManager, error) {
	config, err := mcp.Load(root)
	if err != nil {
		return nil, err
	}
	return &mcpManager{root: root, registry: registry, config: config, clients: make(map[string]*mcp.Client)}, nil
}

func (m *mcpManager) SetAudit(audit session.Writer) { m.audit = audit }

func (m *mcpManager) Connected(name string) bool {
	_, ok := m.clients[name]
	return ok
}

func (m *mcpManager) Connect(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	server, ok := m.config.Server(name)
	if !ok {
		return fmt.Errorf("MCP server %q is not configured", name)
	}
	if m.Connected(name) {
		return fmt.Errorf("MCP server %q is already connected", name)
	}
	env := make([]string, 0, len(server.EnvRefs))
	for _, ref := range server.EnvRefs {
		value, exists := os.LookupEnv(ref)
		if !exists {
			return fmt.Errorf("MCP environment reference %q is not set", ref)
		}
		env = append(env, ref+"="+value)
	}
	client, err := mcp.Start(ctx, server, env)
	if err != nil {
		m.record(name, "connection_failed", err)
		return err
	}
	if err := tool.AttachMCP(m.registry, name, client); err != nil {
		_ = client.Close()
		m.record(name, "protocol_failed", err)
		return err
	}
	m.clients[name] = client
	m.record(name, "connected", nil)
	return nil
}

func (m *mcpManager) Close() error {
	var first error
	for name, client := range m.clients {
		if err := client.Close(); err != nil && first == nil {
			first = fmt.Errorf("MCP server %q: %w", name, err)
		}
		delete(m.clients, name)
	}
	return first
}

func (m *mcpManager) record(name, status string, err error) {
	if m.audit == nil {
		return
	}
	event := agent.Event{Type: agent.EventToolResult, ToolName: "MCPConnect", MCPServer: name, Operation: "mcp_connect", ExecutionStatus: status}
	if err != nil {
		event.ErrorSummary = err.Error()
		event.FailureReason = status
	}
	_ = m.audit.Append(event)
}

func handleMCPCommand(ctx context.Context, text string, manager *mcpManager) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "/mcp list" {
		if len(manager.config.Servers) == 0 {
			return "MCP：没有配置本地 stdio server", true
		}
		lines := make([]string, 0, len(manager.config.Servers))
		for _, server := range manager.config.Servers {
			state := "configured"
			if manager.Connected(server.Name) {
				state = "connected"
			}
			lines = append(lines, server.Name+" · "+state)
		}
		return strings.Join(lines, "\n"), true
	}
	if text == "/mcp" {
		return "用法：/mcp list 或 /mcp connect <name>", true
	}
	const prefix = "/mcp connect "
	if strings.HasPrefix(text, prefix) {
		name := strings.TrimSpace(strings.TrimPrefix(text, prefix))
		if name == "" {
			return "用法：/mcp connect <name>", true
		}
		if err := manager.Connect(ctx, name); err != nil {
			return "MCP 连接失败：" + err.Error(), true
		}
		return "MCP 已连接：" + name, true
	}
	if strings.HasPrefix(text, "/mcp ") {
		return "用法：/mcp list 或 /mcp connect <name>", true
	}
	return "", false
}
