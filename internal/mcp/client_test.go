package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
