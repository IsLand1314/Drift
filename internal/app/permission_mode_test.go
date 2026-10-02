package app

import (
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestPermissionModeDecision(t *testing.T) {
	tests := []struct {
		name    string
		mode    permissionMode
		request agent.PermissionRequest
		allow   bool
		reason  string
	}{
		{"default asks", permissionModeDefault, agent.PermissionRequest{ToolName: "write_file", Operation: "create_file"}, false, "approval_required"},
		{"accept edits writes", permissionModeAcceptEdits, agent.PermissionRequest{ToolName: "edit_file", Operation: "edit_file"}, true, "mode_accept_edits"},
		{"accept edits still asks delete", permissionModeAcceptEdits, agent.PermissionRequest{ToolName: "delete_file", Operation: "delete_file"}, false, "approval_required"},
		{"plan denies writes", permissionModePlan, agent.PermissionRequest{ToolName: "write_file", Operation: "create_file"}, false, "plan_read_only"},
		{"plan denies commands", permissionModePlan, agent.PermissionRequest{ToolName: "run_command", Operation: "run_command"}, false, "plan_read_only"},
		{"bypass allows ordinary write", permissionModeBypass, agent.PermissionRequest{ToolName: "write_file", Operation: "create_file"}, true, "mode_bypass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decidePermission(tt.mode, tt.request)
			if got.Allow != tt.allow || got.Reason != tt.reason {
				t.Fatalf("decidePermission(%q, %+v) = %+v, want allow=%v reason=%q", tt.mode, tt.request, got, tt.allow, tt.reason)
			}
		})
	}
}

func TestParsePermissionMode(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  permissionMode
	}{
		{"default", permissionModeDefault},
		{"acceptEdits", permissionModeAcceptEdits},
		{"plan", permissionModePlan},
		{"bypassPermissions", permissionModeBypass},
	} {
		if got, err := parsePermissionMode(tt.input); err != nil || got != tt.want {
			t.Fatalf("parsePermissionMode(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
		}
	}
	if _, err := parsePermissionMode("unsafe"); err == nil {
		t.Fatal("parsePermissionMode accepted unknown mode")
	}
}
