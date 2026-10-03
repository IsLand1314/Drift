package app

import (
	"context"
	"testing"

	"github.com/IsLand1314/Drift/internal/tool"
)

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
