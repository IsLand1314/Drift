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
		policy  agent.PolicyDecision
		source  agent.PermissionSource
	}{
		{"default asks", permissionModeDefault, agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file"}, false, "approval_required", agent.PolicyAsk, ""},
		{"accept edits writes", permissionModeAcceptEdits, agent.PermissionRequest{ToolName: "EditFile", Operation: "edit_file"}, true, "mode_accept_edits", agent.PolicyAllow, agent.PermissionSourceMode},
		{"accept edits still asks delete", permissionModeAcceptEdits, agent.PermissionRequest{ToolName: "DeleteFile", Operation: "delete_file"}, false, "approval_required", agent.PolicyAsk, ""},
		{"plan denies writes", permissionModePlan, agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file"}, false, "plan_read_only", agent.PolicyDeny, agent.PermissionSourceMode},
		{"plan denies commands", permissionModePlan, agent.PermissionRequest{ToolName: "Bash", Operation: "run_command"}, false, "plan_read_only", agent.PolicyDeny, agent.PermissionSourceMode},
		{"bypass allows ordinary write", permissionModeBypass, agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file"}, true, "mode_bypass", agent.PolicyAllow, agent.PermissionSourceMode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decidePermission(tt.mode, tt.request)
			if got.Allow != tt.allow || got.Reason != tt.reason || got.Policy != tt.policy || got.Source != tt.source {
				t.Fatalf("decidePermission(%q, %+v) = %+v, want allow=%v reason=%q policy=%q source=%q", tt.mode, tt.request, got, tt.allow, tt.reason, tt.policy, tt.source)
			}
		})
	}
}

func TestMCPCallsAlwaysAskExceptPlan(t *testing.T) {
	request := agent.PermissionRequest{ToolName: "mcp__demo__echo", Operation: "mcp_call", Path: "demo/echo"}
	for _, mode := range []permissionMode{permissionModeDefault, permissionModeAcceptEdits, permissionModeBypass} {
		if got := decidePermission(mode, request); got.Policy != agent.PolicyAsk {
			t.Fatalf("mode %q decision=%+v, want ask", mode, got)
		}
	}
	if got := decidePermission(permissionModePlan, request); got.Policy != agent.PolicyDeny {
		t.Fatalf("plan decision=%+v, want deny", got)
	}
}

func TestPlanModeTransitions(t *testing.T) {
	enter := decidePermission(permissionModeDefault, agent.PermissionRequest{ToolName: "EnterPlanMode", Operation: "enterplanmode"})
	if !enter.Allow || enter.Policy != agent.PolicyAllow {
		t.Fatalf("enter=%+v", enter)
	}
	exit := decidePermission(permissionModePlan, agent.PermissionRequest{ToolName: "ExitPlanMode", Operation: "exitplanmode"})
	if exit.Allow || exit.Policy != agent.PolicyAsk {
		t.Fatalf("exit=%+v", exit)
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

func TestParsePermissionModeCommandAcceptsDirectModeName(t *testing.T) {
	mode, handled, err := parsePermissionModeCommand("/permissions plan")
	if err != nil || !handled || mode != permissionModePlan {
		t.Fatalf("parsePermissionModeCommand = %q, %v, %v", mode, handled, err)
	}
	if _, handled, _ := parsePermissionModeCommand("/permissions clear"); handled {
		t.Fatal("/permissions clear should remain the persistence command")
	}
}
