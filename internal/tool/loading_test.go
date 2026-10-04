package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/mcp"
)

func TestEagerLoadingExposesAllRegisteredSchemas(t *testing.T) {
	registry := NewChatRegistry()
	if _, ok := registry.Lookup("WriteFile"); ok {
		t.Fatal("WriteFile loaded before eager strategy")
	}
	loaded, ok := registry.(interface{ LoadAll() })
	if !ok {
		t.Fatal("chat registry does not expose LoadAll")
	}
	loaded.LoadAll()
	if _, ok := registry.Lookup("WriteFile"); !ok {
		t.Fatal("WriteFile not loaded by eager strategy")
	}
}

func TestNativeStrategyFallsBackUntilProviderSupportsNativeReferences(t *testing.T) {
	actual, native := ResolveToolLoadingStrategy(LoadingNative, llm.Capabilities{NativeToolCalls: true})
	if actual != LoadingDispatch || native {
		t.Fatalf("actual=%q native=%v", actual, native)
	}
}

func TestNativeStrategyUsesProviderNativeReferences(t *testing.T) {
	actual, native := ResolveToolLoadingStrategy(LoadingNative, llm.Capabilities{NativeToolReferences: true})
	if actual != LoadingNative || !native {
		t.Fatalf("actual=%q native=%v", actual, native)
	}
}

type loadingMCPClient struct{}

func (loadingMCPClient) ListTools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
}
func (loadingMCPClient) CallTool(context.Context, string, json.RawMessage) (mcp.Result, error) {
	return mcp.Result{Text: "ok"}, nil
}

func TestEagerStrategyLoadsMCPToolsAttachedLater(t *testing.T) {
	registry := NewChatRegistry()
	registry.(interface{ LoadAll() }).LoadAll()
	if err := AttachMCP(context.Background(), registry, "demo", loadingMCPClient{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Lookup("mcp__demo__echo"); !ok {
		t.Fatal("MCP tool was not eagerly loaded")
	}
}

func TestToolLoadingStrategyParsing(t *testing.T) {
	for _, value := range []LoadingStrategy{LoadingEager, LoadingDispatch, LoadingNative} {
		parsed, err := ParseToolLoadingStrategy(string(value))
		if err != nil || parsed != value {
			t.Fatalf("value=%q parsed=%q err=%v", value, parsed, err)
		}
	}
	if _, err := ParseToolLoadingStrategy("unknown"); err == nil {
		t.Fatal("unknown strategy accepted")
	}
}
