package app

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

func TestMCPAppHelperProcess(t *testing.T) {
	if os.Getenv("DRIFT_MCP_APP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		if request.Method == "notifications/initialized" {
			continue
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}}}}
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		_, _ = os.Stdout.Write(append(response, '\n'))
	}
	os.Exit(0)
}

func TestMCPConnectRequiresNamedConfiguredServer(t *testing.T) {
	manager, err := newMCPManager(t.TempDir(), tool.NewChatRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Connect(context.Background(), "missing"); err == nil {
		t.Fatal("Connect() error = nil")
	}
	if manager.Connected("demo") {
		t.Fatal("unexpected connection")
	}
}

func TestHandleMCPCommandRecognizesListAndConnect(t *testing.T) {
	manager, err := newMCPManager(t.TempDir(), tool.NewChatRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	message, handled := handleMCPCommand(context.Background(), "/mcp list", manager)
	if !handled || message == "" {
		t.Fatalf("message=%q handled=%v", message, handled)
	}
	if _, handled := handleMCPCommand(context.Background(), "hello", manager); handled {
		t.Fatal("ordinary prompt was consumed")
	}
}

func TestMCPManagerConnectsExplicitlyAndAuditsWithoutEnablingTool(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".drift"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"servers":[{"name":"demo","transport":"stdio","command":` + strconv.Quote(os.Args[0]) + `,"args":["-test.run=TestMCPAppHelperProcess"],"env_refs":["DRIFT_MCP_APP_HELPER"]}]}`
	if err := os.WriteFile(filepath.Join(root, ".drift", "mcp.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DRIFT_MCP_APP_HELPER", "1"); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("DRIFT_MCP_APP_HELPER")
	registry := tool.NewChatRegistry()
	manager, err := newMCPManager(root, registry)
	if err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(root, ".drift", "audit.jsonl")
	audit, err := session.NewJSONLWriter(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetAudit(audit)
	defer func() { _ = audit.Close(); _ = manager.Close() }()
	if err := manager.Connect(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if !manager.Connected("demo") {
		t.Fatal("manager did not retain explicit connection")
	}
	if _, ok := registry.Lookup("mcp__demo__echo"); ok {
		t.Fatal("MCP tool enabled before ToolSearch load")
	}
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"echo","load":["mcp__demo__echo"]}`); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Lookup("mcp__demo__echo"); !ok {
		t.Fatal("MCP tool not loaded after ToolSearch")
	}
	message, handled := handleMCPCommand(context.Background(), "/mcp disconnect demo", manager)
	if !handled || !strings.Contains(message, "已断开") || manager.Connected("demo") {
		t.Fatalf("disconnect message=%q handled=%v", message, handled)
	}
	raw, err := os.ReadFile(auditPath)
	if err != nil || !strings.Contains(string(raw), `"mcp_server":"demo"`) || !strings.Contains(string(raw), `"execution_status":"connected"`) {
		t.Fatalf("audit=%s err=%v", raw, err)
	}
}
