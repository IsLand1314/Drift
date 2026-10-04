package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientInitializesListsAndCallsTool(t *testing.T) {
	client, err := Start(context.Background(), Server{Name: "demo", Transport: "stdio", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess"}}, []string{"DRIFT_MCP_TEST_HELPER=1"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%+v err=%v", tools, err)
	}
	if tools[0].Description != "Echo text (path inherited)" {
		t.Fatalf("server environment was not inherited: description=%q", tools[0].Description)
	}
	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || result.Text != "hi" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestMergeEnvKeepsRuntimePathButExcludesUnreferencedSecrets(t *testing.T) {
	merged := mergeEnv([]string{"PATH=/runtime", "DRIFT_SECRET=not-for-mcp"}, []string{"DRIFT_TOKEN=explicit"})
	joined := strings.Join(merged, "\x00")
	if !strings.Contains(joined, "PATH=/runtime") || !strings.Contains(joined, "DRIFT_TOKEN=explicit") {
		t.Fatalf("runtime/explicit environment missing: %q", joined)
	}
	if strings.Contains(joined, "DRIFT_SECRET=not-for-mcp") {
		t.Fatalf("unreferenced secret leaked into MCP environment: %q", joined)
	}
}

func TestClientCancellationClosesTheServerProcess(t *testing.T) {
	client, err := Start(context.Background(), Server{Name: "demo", Transport: "stdio", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess"}}, []string{"DRIFT_MCP_TEST_HELPER=1", "DRIFT_MCP_BLOCK_CALL=1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = client.CallTool(ctx, "echo", json.RawMessage(`{"text":"blocked"}`))
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "EOF")) {
		t.Fatalf("CallTool error=%v, want cancellation or closed pipe", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"after"}`)); !strings.Contains(err.Error(), "client is closed") {
		t.Fatalf("follow-up error=%v, want closed client", err)
	}
}

func TestHTTPClientSupportsToolsResourcesAndPrompts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
		case "resources/list":
			result = map[string]any{"resources": []any{map[string]any{"uri": "memory://one", "name": "one"}}}
		case "resources/read":
			result = map[string]any{"contents": []any{map[string]any{"type": "text", "text": "resource"}}}
		case "prompts/list":
			result = map[string]any{"prompts": []any{map[string]any{"name": "summarize"}}}
		case "prompts/get":
			result = map[string]any{"messages": []any{map[string]any{"type": "text", "text": "prompt"}}}
		default:
			result = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	client, err := Start(context.Background(), Server{Transport: "streamable-http", URL: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil || result.Text != "ok" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	resources, err := client.ListResources(context.Background())
	if err != nil || len(resources) != 1 {
		t.Fatalf("resources=%v err=%v", resources, err)
	}
	contents, err := client.ReadResource(context.Background(), resources[0].URI)
	if err != nil || contents[0].Text != "resource" {
		t.Fatalf("contents=%v err=%v", contents, err)
	}
	prompts, err := client.ListPrompts(context.Background())
	if err != nil || len(prompts) != 1 {
		t.Fatalf("prompts=%v err=%v", prompts, err)
	}
	messages, err := client.GetPrompt(context.Background(), prompts[0].Name, nil)
	if err != nil || messages[0].Text != "prompt" {
		t.Fatalf("messages=%v err=%v", messages, err)
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("DRIFT_MCP_TEST_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.Method == "notifications/initialized" {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "mock", "version": "1"}}
		case "tools/list":
			description := "Echo text (path missing)"
			if os.Getenv("PATH") != "" || os.Getenv("Path") != "" {
				description = "Echo text (path inherited)"
			}
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": description, "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			if os.Getenv("DRIFT_MCP_BLOCK_CALL") == "1" {
				select {}
			}
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "hi"}}}
		default:
			result = map[string]any{}
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		_, _ = os.Stdout.Write(append(response, '\n'))
	}
	os.Exit(0)
}
