package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

type recordingPublisher struct {
	snapshots []Snapshot
	err       error
}

func (p *recordingPublisher) Publish(_ context.Context, snapshot Snapshot) error {
	p.snapshots = append(p.snapshots, snapshot)
	return p.err
}

func TestRecorderCollectsBoundedOperationalDimensions(t *testing.T) {
	publisher := &recordingPublisher{}
	clock := func() time.Time { return time.Unix(100, 0) }
	recorder := NewRecorder(Options{
		Enabled:   true,
		Mode:      "chat",
		Provider:  "deepseek",
		Protocol:  "openai-compat",
		Now:       clock,
		Publisher: publisher,
	})
	recorder.Observe(agent.Event{Type: agent.EventStreamEnded, StreamStatus: "completed"})
	recorder.Observe(agent.Event{Type: agent.EventToolResult, ToolName: "ReadFile", ExecutionStatus: "success", Path: "private/source.go", Result: "secret content"})
	recorder.Observe(agent.Event{Type: agent.EventToolResult, ToolName: "mcp__browser__navigate", ExecutionStatus: "failed", Command: "contains a token"})
	recorder.Observe(agent.Event{Type: agent.EventPermissionDecision, ToolName: "Bash", PermissionOutcome: agent.ApprovalAllowOnce, Command: "private command"})
	recorder.Observe(agent.Event{Type: agent.EventToolResult, ToolName: "Bash", ExecutionStatus: "success", SandboxMode: "auto", SandboxBackend: "appcontainer", SandboxAvailable: true})

	if err := recorder.Finish(context.Background(), OutcomeSuccess); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if len(publisher.snapshots) != 1 {
		t.Fatalf("published snapshots = %d, want 1", len(publisher.snapshots))
	}
	snapshot := publisher.snapshots[0]
	if snapshot.Mode != "chat" || snapshot.Provider != "deepseek" || snapshot.Protocol != "openai-compat" || snapshot.Outcome != OutcomeSuccess {
		t.Fatalf("unexpected run dimensions: %#v", snapshot)
	}
	if snapshot.ModelRequests["success"] != 1 {
		t.Fatalf("model requests = %#v", snapshot.ModelRequests)
	}
	if snapshot.ToolCalls[ToolKey{Tool: "ReadFile", Category: "read", Outcome: "success"}] != 1 {
		t.Fatalf("read tool calls = %#v", snapshot.ToolCalls)
	}
	if snapshot.ToolCalls[ToolKey{Tool: "mcp", Category: "mcp", Outcome: "failed"}] != 1 {
		t.Fatalf("mcp tool calls = %#v", snapshot.ToolCalls)
	}
	if snapshot.PermissionDecisions[PermissionKey{Tool: "Bash", Decision: "allow_once"}] != 1 {
		t.Fatalf("permissions = %#v", snapshot.PermissionDecisions)
	}
	if snapshot.SandboxExecutions[SandboxKey{Backend: "appcontainer", Sandboxed: "true", Outcome: "success"}] != 1 {
		t.Fatalf("sandbox executions = %#v", snapshot.SandboxExecutions)
	}
}

func TestRecorderCollectsCommandPolicyDimensions(t *testing.T) {
	publisher := &recordingPublisher{}
	recorder := NewRecorder(Options{Enabled: true, Publisher: publisher})
	recorder.Observe(agent.Event{Type: agent.EventPermissionDecision, ToolName: "Bash", CommandRisk: "dangerous", CommandRuleID: "git_reset_hard", DecisionLayer: "command_policy", Allowed: false})
	if err := recorder.Finish(context.Background(), OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	if len(publisher.snapshots) != 1 {
		t.Fatalf("snapshots=%d", len(publisher.snapshots))
	}
	if publisher.snapshots[0].CommandPolicyDecisions[CommandPolicyKey{Risk: "dangerous", Outcome: "denied", RuleID: "git_reset_hard"}] != 1 {
		t.Fatalf("command policy=%#v", publisher.snapshots[0].CommandPolicyDecisions)
	}
}

func TestRecorderNeverPublishesWhenDisabled(t *testing.T) {
	publisher := &recordingPublisher{}
	recorder := NewRecorder(Options{Enabled: false, Publisher: publisher})
	recorder.Observe(agent.Event{Type: agent.EventToolResult, ToolName: "Bash", ExecutionStatus: "success"})
	if err := recorder.Finish(context.Background(), OutcomeSuccess); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if len(publisher.snapshots) != 0 {
		t.Fatalf("disabled recorder published %#v", publisher.snapshots)
	}
}

func TestRecorderPublisherFailureIsNonFatal(t *testing.T) {
	recorder := NewRecorder(Options{Enabled: true, Publisher: &recordingPublisher{err: errors.New("offline")}})
	if err := recorder.Finish(context.Background(), OutcomeSuccess); err != nil {
		t.Fatalf("Finish() error = %v, want telemetry failure to be ignored", err)
	}
}
