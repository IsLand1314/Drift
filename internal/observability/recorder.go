// Package observability records bounded, operational metrics for one Drift
// invocation. It intentionally never accepts prompts, paths, commands, tool
// arguments, results, session IDs, or error text as metric dimensions.
package observability

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

// Outcome is the final, user-visible state of one Drift invocation.
type Outcome string

const (
	OutcomeSuccess   Outcome = "success"
	OutcomeFailed    Outcome = "failed"
	OutcomeCancelled Outcome = "cancelled"
)

// ToolKey and the related key types deliberately have a small, stable label
// surface. Their values are converted into Prometheus labels by Publisher.
type ToolKey struct {
	Tool, Category, Outcome string
}

type PermissionKey struct{ Tool, Decision string }

type SandboxKey struct{ Backend, Sandboxed, Outcome string }

type CommandPolicyKey struct{ Risk, Outcome, RuleID string }

// Snapshot is an immutable-at-publish summary of one invocation.
type Snapshot struct {
	Mode, Provider, Protocol string
	Outcome                  Outcome
	Duration                 time.Duration
	ModelRequests            map[string]uint64
	ToolCalls                map[ToolKey]uint64
	PermissionDecisions      map[PermissionKey]uint64
	SandboxExecutions        map[SandboxKey]uint64
	CommandPolicyDecisions   map[CommandPolicyKey]uint64
}

// Publisher delivers a snapshot. Recorder treats delivery errors as
// best-effort telemetry failures: they never change Drift's command outcome.
type Publisher interface {
	Publish(context.Context, Snapshot) error
}

type Options struct {
	Enabled        bool
	Mode           string
	Provider       string
	Protocol       string
	Publisher      Publisher
	Now            func() time.Time
	PublishTimeout time.Duration
}

// Recorder is safe for the event callbacks used by Drift's runner.
type Recorder struct {
	mu                       sync.Mutex
	enabled                  bool
	mode, provider, protocol string
	publisher                Publisher
	now                      func() time.Time
	started                  time.Time
	timeout                  time.Duration
	finished                 bool
	modelRequests            map[string]uint64
	toolCalls                map[ToolKey]uint64
	permissionDecisions      map[PermissionKey]uint64
	sandboxExecutions        map[SandboxKey]uint64
	commandPolicyDecisions   map[CommandPolicyKey]uint64
}

func NewRecorder(options Options) *Recorder {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	timeout := options.PublishTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Recorder{
		enabled:                options.Enabled && options.Publisher != nil,
		mode:                   boundedLabel(options.Mode, "prompt"),
		provider:               boundedLabel(options.Provider, "unknown"),
		protocol:               boundedLabel(options.Protocol, "unknown"),
		publisher:              options.Publisher,
		now:                    now,
		started:                now(),
		timeout:                timeout,
		modelRequests:          map[string]uint64{},
		toolCalls:              map[ToolKey]uint64{},
		permissionDecisions:    map[PermissionKey]uint64{},
		sandboxExecutions:      map[SandboxKey]uint64{},
		commandPolicyDecisions: map[CommandPolicyKey]uint64{},
	}
}

// Observe consumes agent runtime events without retaining their sensitive
// payloads. Unknown tools are intentionally collapsed into a stable bucket.
func (r *Recorder) Observe(event agent.Event) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	switch event.Type {
	case agent.EventStreamEnded:
		r.modelRequests[streamOutcome(event.StreamStatus)]++
	case agent.EventToolResult:
		outcome := executionOutcome(event.ExecutionStatus)
		tool, category := toolDimension(event.ToolName)
		r.toolCalls[ToolKey{Tool: tool, Category: category, Outcome: outcome}]++
		if event.SandboxBackend != "" || event.SandboxMode != "" {
			r.sandboxExecutions[SandboxKey{Backend: sandboxBackend(event.SandboxBackend), Sandboxed: sandboxed(event), Outcome: outcome}]++
		}
	case agent.EventPermissionDecision:
		tool, _ := toolDimension(event.ToolName)
		r.permissionDecisions[PermissionKey{Tool: tool, Decision: permissionDecision(event.PermissionOutcome)}]++
		if event.CommandRisk != "" {
			outcome := "asked"
			if !event.Allowed {
				outcome = "denied"
			} else if event.AutoAllowed {
				outcome = "allowed"
			}
			r.commandPolicyDecisions[CommandPolicyKey{Risk: boundedLabel(event.CommandRisk, "unknown"), Outcome: outcome, RuleID: boundedLabel(event.CommandRuleID, "none")}]++
		}
	}
}

// Finish publishes exactly once. Transport failures are intentionally ignored:
// observability must never turn a successful Drift command into a failed one.
func (r *Recorder) Finish(parent context.Context, outcome Outcome) error {
	if r == nil || !r.enabled {
		return nil
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return nil
	}
	r.finished = true
	snapshot := Snapshot{
		Mode: r.mode, Provider: r.provider, Protocol: r.protocol, Outcome: normalizeOutcome(outcome),
		Duration:               r.now().Sub(r.started),
		ModelRequests:          copyStringCounts(r.modelRequests),
		ToolCalls:              copyToolCounts(r.toolCalls),
		PermissionDecisions:    copyPermissionCounts(r.permissionDecisions),
		SandboxExecutions:      copySandboxCounts(r.sandboxExecutions),
		CommandPolicyDecisions: copyCommandPolicyCounts(r.commandPolicyDecisions),
	}
	publisher, timeout := r.publisher, r.timeout
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	_ = publisher.Publish(ctx, snapshot)
	return nil
}

func streamOutcome(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "completed", "success":
		return "success"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "failed"
	}
}

func executionOutcome(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "success", "completed", "allowed":
		return "success"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "failed"
	}
}

func normalizeOutcome(outcome Outcome) Outcome {
	if outcome == OutcomeSuccess || outcome == OutcomeCancelled {
		return outcome
	}
	return OutcomeFailed
}

func permissionDecision(value agent.ApprovalDecision) string {
	switch value {
	case agent.ApprovalAllowOnce:
		return "allow_once"
	case agent.ApprovalAllowPersistent:
		return "allow_persistent"
	case agent.ApprovalDeny:
		return "deny"
	case agent.ApprovalCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func sandboxed(event agent.Event) string {
	if event.SandboxMode != "off" && event.SandboxAvailable && event.SandboxBackend != "" && event.SandboxBackend != "none" {
		return "true"
	}
	return "false"
}

func sandboxBackend(value string) string { return boundedLabel(value, "none") }

func toolDimension(name string) (string, string) {
	if strings.HasPrefix(name, "mcp__") {
		return "mcp", "mcp"
	}
	switch name {
	case "ReadFile", "Glob", "Grep":
		return name, "read"
	case "WriteFile", "EditFile", "DeleteFile":
		return name, "write"
	case "Bash", "GitRevert":
		return name, "command"
	case "ToolSearch", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate":
		return name, "control"
	default:
		return "other", "other"
	}
}

func boundedLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() == 48 {
			break
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

func copyStringCounts(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func copyToolCounts(in map[ToolKey]uint64) map[ToolKey]uint64 {
	out := make(map[ToolKey]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func copyPermissionCounts(in map[PermissionKey]uint64) map[PermissionKey]uint64 {
	out := make(map[PermissionKey]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func copySandboxCounts(in map[SandboxKey]uint64) map[SandboxKey]uint64 {
	out := make(map[SandboxKey]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func copyCommandPolicyCounts(in map[CommandPolicyKey]uint64) map[CommandPolicyKey]uint64 {
	out := make(map[CommandPolicyKey]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
