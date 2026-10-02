package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestPermissionPolicyMissingFileIsEmpty(t *testing.T) {
	policy, err := loadPermissionPolicy(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if policy.allows(agent.PermissionRequest{ToolName: "write_file", Operation: "create_file", Path: "a.txt"}) {
		t.Fatal("missing policy unexpectedly allowed a request")
	}
}

func TestPermissionPolicyMatchesExactFileRequest(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "write_file", Operation: "create_file", Path: "tmp/hello.txt"}
	policy := newPermissionPolicy(root)
	if err := policy.remember(request); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPermissionPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.allows(request) {
		t.Fatal("exact request was not allowed")
	}
	if loaded.allows(agent.PermissionRequest{ToolName: "write_file", Operation: "create_file", Path: "tmp/other.txt"}) {
		t.Fatal("different path unexpectedly matched")
	}
}

func TestPermissionPolicyMatchesExactCommandAndCWD(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "run_command", Operation: "run_command", Command: "go test ./...", CWD: "."}
	policy := newPermissionPolicy(root)
	if err := policy.remember(request); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPermissionPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.allows(request) {
		t.Fatal("exact command was not allowed")
	}
	if loaded.allows(agent.PermissionRequest{ToolName: "run_command", Operation: "run_command", Command: "go test ./...", CWD: "subdir"}) {
		t.Fatal("different cwd unexpectedly matched")
	}
}

func TestPermissionPolicyCorruptVersionFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".drift"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".drift", "permissions.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPermissionPolicy(root); err == nil {
		t.Fatal("unknown version should fail closed with an error")
	}
}

func TestPermissionPolicyDeduplicatesRulesAndClears(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "delete_file", Operation: "delete_file", Path: "old.txt"}
	policy := newPermissionPolicy(root)
	if err := policy.remember(request); err != nil {
		t.Fatal(err)
	}
	if err := policy.remember(request); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".drift", "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Rules) != 1 {
		t.Fatalf("rules=%d, want one", len(document.Rules))
	}
	if err := policy.clear(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPermissionPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.allows(request) {
		t.Fatal("cleared policy still allowed request")
	}
}
