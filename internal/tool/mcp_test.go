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

type fakeMCPContentClient struct{ fakeMCPClient }

func (fakeMCPContentClient) ListResources(context.Context) ([]mcp.Resource, error) {
	return []mcp.Resource{{Name: "doc", URI: "memory://doc"}}, nil
}
func (fakeMCPContentClient) ReadResource(context.Context, string) ([]mcp.ContentBlock, error) {
	return []mcp.ContentBlock{{Type: "text", Text: "resource text"}}, nil
}
func (fakeMCPContentClient) ListPrompts(context.Context) ([]mcp.Prompt, error) {
	return []mcp.Prompt{{Name: "summarize"}}, nil
}
func (fakeMCPContentClient) GetPrompt(context.Context, string, map[string]string) ([]mcp.ContentBlock, error) {
	return []mcp.ContentBlock{{Type: "text", Text: "prompt text"}}, nil
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

func TestAttachMCPPreservesExplicitReadOnlyHint(t *testing.T) {
	registry := NewChatRegistry()
	client := fakeMCPClient{tools: []mcp.Tool{{Name: "inspect", Description: "Inspect", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}}}
	if err := AttachMCP(context.Background(), registry, "demo", client); err != nil {
		t.Fatal(err)
	}
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"inspect","load":["mcp__demo__inspect"]}`); err != nil {
		t.Fatal(err)
	}
	value, ok := registry.Lookup("mcp__demo__inspect")
	if !ok {
		t.Fatal("MCP tool was not registered")
	}
	declared, ok := value.(ReadOnlyMCPTool)
	if !ok || !declared.ReadOnly() {
		t.Fatalf("read-only capability = %#v, %v", value, ok)
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

func TestAttachMCPRegistersResourceAndPromptToolsLazily(t *testing.T) {
	registry := NewChatRegistry()
	client := fakeMCPContentClient{fakeMCPClient{tools: []mcp.Tool{{Name: "echo", Description: "Echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}}}
	if err := AttachMCP(context.Background(), registry, "demo", client); err != nil {
		t.Fatal(err)
	}
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"resource prompt","load":["mcp__demo__resource_read","mcp__demo__prompt_get"]}`); err != nil {
		t.Fatal(err)
	}
	resource, ok := registry.Lookup("mcp__demo__resource_read")
	if !ok {
		t.Fatal("resource tool not loaded")
	}
	got, err := resource.Execute(context.Background(), t.TempDir(), `{"uri":"memory://doc"}`)
	if err != nil || !strings.Contains(got, "resource text") || !strings.Contains(got, "untrusted") {
		t.Fatalf("resource=%q err=%v", got, err)
	}
	prompt, ok := registry.Lookup("mcp__demo__prompt_get")
	if !ok {
		t.Fatal("prompt tool not loaded")
	}
	got, err = prompt.Execute(context.Background(), t.TempDir(), `{"name":"summarize"}`)
	if err != nil || !strings.Contains(got, "prompt text") || !strings.Contains(got, "untrusted") {
		t.Fatalf("prompt=%q err=%v", got, err)
	}
}

func TestMCPResourceRejectsFileOutsideWorkspace(t *testing.T) {
	registry := NewChatRegistry()
	client := fakeMCPContentClient{fakeMCPClient{tools: nil}}
	if err := AttachMCP(context.Background(), registry, "demo", client); err != nil {
		t.Fatal(err)
	}
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"resource","load":["mcp__demo__resource_read"]}`); err != nil {
		t.Fatal(err)
	}
	resource, _ := registry.Lookup("mcp__demo__resource_read")
	if _, err := resource.Execute(context.Background(), t.TempDir(), `{"uri":"file:///outside/secret.txt"}`); err == nil {
		t.Fatal("outside resource accepted")
	}
}
