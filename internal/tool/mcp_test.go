package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/mcp"
)

type fakeMCPClient struct{ tools []mcp.Tool }

func (f fakeMCPClient) ListTools(context.Context) ([]mcp.Tool, error) { return f.tools, nil }
func (fakeMCPClient) CallTool(context.Context, string, json.RawMessage) (mcp.Result, error) {
	return mcp.Result{Text: "ok"}, nil
}

type contextMCPClient struct{ cancelled bool }

func (c *contextMCPClient) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	<-ctx.Done()
	c.cancelled = true
	return nil, ctx.Err()
}

func (*contextMCPClient) CallTool(context.Context, string, json.RawMessage) (mcp.Result, error) {
	return mcp.Result{}, nil
}

func TestAttachMCPUsesCallerContextForDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &contextMCPClient{}
	if err := AttachMCP(ctx, NewChatRegistry(), "demo", client); err == nil {
		t.Fatal("AttachMCP() error = nil, want cancelled discovery")
	}
	if !client.cancelled {
		t.Fatal("AttachMCP() did not pass the cancelled context to discovery")
	}
}

func TestAttachMCPAddsSearchableButDisabledTool(t *testing.T) {
	registry := NewChatRegistry()
	err := AttachMCP(context.Background(), registry, "demo", fakeMCPClient{tools: []mcp.Tool{{Name: "echo", Description: "Echo text", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Lookup("mcp__demo__echo"); ok {
		t.Fatal("MCP tool was enabled before ToolSearch load")
	}
	search, _ := registry.Lookup("ToolSearch")
	result, err := search.Execute(context.Background(), t.TempDir(), `{"query":"echo","load":["mcp__demo__echo"]}`)
	if err != nil || !strings.Contains(result, "mcp__demo__echo") {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if _, ok := registry.Lookup("mcp__demo__echo"); !ok {
		t.Fatal("MCP tool was not loaded")
	}
}

func TestAttachMCPRejectsUnsafeOrDuplicateNames(t *testing.T) {
	registry := NewChatRegistry()
	valid := fakeMCPClient{tools: []mcp.Tool{{Name: "echo", Description: "Echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	if err := AttachMCP(context.Background(), registry, "bad/name", valid); err == nil {
		t.Fatal("unsafe server accepted")
	}
	if err := AttachMCP(context.Background(), registry, "demo", valid); err != nil {
		t.Fatal(err)
	}
	if err := AttachMCP(context.Background(), registry, "demo", valid); err == nil {
		t.Fatal("duplicate tool accepted")
	}
}
