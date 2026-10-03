package agent

import "testing"

func TestPermissionDecisionCarriesPolicyAndApprovalKinds(t *testing.T) {
	decision := PermissionDecision{
		Allow:    true,
		Policy:   PolicyAsk,
		Approval: ApprovalAllowOnce,
		Source:   PermissionSourceUser,
	}
	if decision.Policy != PolicyAsk || decision.Approval != ApprovalAllowOnce || decision.Source != PermissionSourceUser {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestEventCarriesStructuredSandboxAndExecutionOutcome(t *testing.T) {
	event := Event{
		PermissionSource:  PermissionSourcePersistent,
		PermissionOutcome: ApprovalAllowPersistent,
		SandboxMode:       "required",
		SandboxBackend:    "appcontainer",
		SandboxAvailable:  true,
		SandboxProbe:      "passed",
		ExecutionStatus:   "success",
		FailureReason:     "",
	}
	if event.SandboxBackend != "appcontainer" || !event.SandboxAvailable || event.ExecutionStatus != "success" {
		t.Fatalf("event=%+v", event)
	}
}
