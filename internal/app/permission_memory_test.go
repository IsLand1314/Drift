package app

import (
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestPermissionMemoryMatchesOnlySameWritePattern(t *testing.T) {
	memory := newPermissionMemory()
	request := agent.PermissionRequest{ToolName: "write_file", Operation: "create_file", Path: "tmp/hello.txt"}
	if memory.Allow(request) {
		t.Fatal("empty permission memory allowed request")
	}
	memory.Remember(request)
	if !memory.Allow(request) {
		t.Fatal("remembered request was not allowed")
	}
	if memory.Allow(agent.PermissionRequest{ToolName: "write_file", Operation: "overwrite_file", Path: request.Path}) {
		t.Fatal("different operation matched")
	}
	if memory.Allow(agent.PermissionRequest{ToolName: "write_file", Operation: request.Operation, Path: "tmp/other.txt"}) {
		t.Fatal("different path matched")
	}
}

func TestPermissionMemoryIsProcessLocal(t *testing.T) {
	request := agent.PermissionRequest{ToolName: "write_file", Operation: "create_file", Path: "tmp/hello.txt"}
	first := newPermissionMemory()
	first.Remember(request)
	second := newPermissionMemory()
	if second.Allow(request) {
		t.Fatal("new memory inherited process state")
	}
}
