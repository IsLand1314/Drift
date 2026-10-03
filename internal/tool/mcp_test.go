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

func TestAttachMCPAddsSearchableButDisabledTool(t *testing.T) {
	registry := NewChatRegistry()
	err := AttachMCP(registry, "demo", fakeMCPClient{tools: []mcp.Tool{{Name: "echo", Description: "Echo text", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
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
	if err := AttachMCP(registry, "bad/name", valid); err == nil {
		t.Fatal("unsafe server accepted")
	}
	if err := AttachMCP(registry, "demo", valid); err != nil {
		t.Fatal(err)
	}
	if err := AttachMCP(registry, "demo", valid); err == nil {
		t.Fatal("duplicate tool accepted")
	}
}
