package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestPermissionPolicyMissingFileIsEmpty(t *testing.T) {
	policy, err := loadPermissionPolicy(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if policy.allows(agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "a.txt"}) {
		t.Fatal("missing policy unexpectedly allowed a request")
	}
}

func TestPermissionPolicyMatchesExactFileRequest(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
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
	if loaded.allows(agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/other.txt"}) {
		t.Fatal("different path unexpectedly matched")
	}
}

func TestPermissionPolicyMatchesExactCommandAndCWD(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "Bash", Operation: "run_command", Command: "go test ./...", CWD: "."}
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
	if loaded.allows(agent.PermissionRequest{ToolName: "Bash", Operation: "run_command", Command: "go test ./...", CWD: "subdir"}) {
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
	request := agent.PermissionRequest{ToolName: "DeleteFile", Operation: "delete_file", Path: "old.txt"}
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

func TestPermissionCommandsShowAndClearPolicy(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
	policy := newPermissionPolicy(root)
	if err := policy.remember(request); err != nil {
		t.Fatal(err)
	}
	message, handled := handlePermissionCommand(policy, "/permissions")
	if !handled || !strings.Contains(message, "WriteFile/create_file") || !strings.Contains(message, "tmp/hello.txt") {
		t.Fatalf("summary handled=%t message=%q", handled, message)
	}
	message, handled = handlePermissionCommand(policy, "/permissions clear")
	if !handled || !strings.Contains(message, "已清除") || policy.allows(request) {
		t.Fatalf("clear handled=%t message=%q allowed=%t", handled, message, policy.allows(request))
	}
}

func TestPermissionApprovalChecksPersistentPolicyAfterSessionMemory(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
	policy := newPermissionPolicy(root)
	if err := policy.remember(request); err != nil {
		t.Fatal(err)
	}
	if !permissionAlreadyAllowed(newPermissionMemory(), policy, request) {
		t.Fatal("persistent exact request was not recognized")
	}
	if permissionAlreadyAllowed(newPermissionMemory(), policy, agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/other.txt"}) {
		t.Fatal("different path unexpectedly matched persistent policy")
	}
}

func TestResolvePermissionPersistentAllowCannotOverridePlanDeny(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
	policy := newPermissionPolicy(root)
	if err := policy.remember(request); err != nil {
		t.Fatal(err)
	}
	if got := resolvePermission(permissionModePlan, newPermissionMemory(), policy, request); got.Allow || got.Policy != agent.PolicyDeny || got.Source != agent.PermissionSourceMode {
		t.Fatalf("plan decision=%+v", got)
	}
	if got := resolvePermission(permissionModeDefault, newPermissionMemory(), policy, request); !got.Allow || got.Approval != agent.ApprovalAllowPersistent || got.Source != agent.PermissionSourcePersistent {
		t.Fatalf("persistent decision=%+v", got)
	}
}
