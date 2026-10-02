package app

import (
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestPermissionMemoryMatchesOnlySameWritePattern(t *testing.T) {
	memory := newPermissionMemory()
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
	if memory.Allow(request) {
		t.Fatal("empty permission memory allowed request")
	}
	memory.Remember(request)
	if !memory.Allow(request) {
		t.Fatal("remembered request was not allowed")
	}
	if memory.Allow(agent.PermissionRequest{ToolName: "WriteFile", Operation: "overwrite_file", Path: request.Path}) {
		t.Fatal("different operation matched")
	}
	if memory.Allow(agent.PermissionRequest{ToolName: "WriteFile", Operation: request.Operation, Path: "tmp/other.txt"}) {
		t.Fatal("different path matched")
	}
}

func TestPermissionMemoryIsProcessLocal(t *testing.T) {
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
	first := newPermissionMemory()
	first.Remember(request)
	second := newPermissionMemory()
	if second.Allow(request) {
		t.Fatal("new memory inherited process state")
	}
}

func TestPermissionMemoryMatchesExactCommandAndCWD(t *testing.T) {
	memory := newPermissionMemory()
	request := agent.PermissionRequest{ToolName: "Bash", Operation: "run_command", Command: "go test ./...", CWD: "."}
	memory.Remember(request)
	if !memory.Allow(request) {
		t.Fatal("remembered command was not allowed")
	}
	for _, different := range []agent.PermissionRequest{
		{ToolName: "Bash", Operation: "run_command", Command: "go test ./internal/tool", CWD: "."},
		{ToolName: "Bash", Operation: "run_command", Command: request.Command, CWD: "internal"},
	} {
		if memory.Allow(different) {
			t.Fatalf("different command pattern matched: %+v", different)
		}
	}
}
