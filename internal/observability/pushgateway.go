package observability

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// PushgatewayPublisher sends one invocation summary using Pushgateway's Add
// semantics. Add preserves counters across short-lived local CLI processes.
type PushgatewayPublisher struct {
	endpoint string
	instance string
	client   *http.Client
}

func NewPushgatewayPublisher(endpoint, instance string) (*PushgatewayPublisher, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("observability: invalid pushgateway URL")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("observability: pushgateway URL must not include a path")
	}
	return &PushgatewayPublisher{endpoint: strings.TrimRight(u.String(), "/"), instance: boundedLabel(instance, "local"), client: http.DefaultClient}, nil
}

func (p *PushgatewayPublisher) Publish(ctx context.Context, snapshot Snapshot) error {
	if p == nil {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/metrics/job/drift/instance/"+url.PathEscape(p.instance), bytes.NewBufferString(exposition(snapshot)))
	if err != nil {
		return fmt.Errorf("observability: create Pushgateway request: %w", err)
	}
	request.Header.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("observability: Pushgateway request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("observability: Pushgateway returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func exposition(snapshot Snapshot) string {
	base := labels(map[string]string{"mode": snapshot.Mode, "provider": snapshot.Provider, "protocol": snapshot.Protocol})
	var b strings.Builder
	b.WriteString("# TYPE drift_runs_total counter\n")
	fmt.Fprintf(&b, "drift_runs_total%s 1\n", mergeLabels(base, "outcome", string(snapshot.Outcome)))
	b.WriteString("# TYPE drift_run_duration_seconds histogram\n")
	writeHistogram(&b, "drift_run_duration_seconds", base, snapshot.Duration.Seconds())
	if len(snapshot.ModelRequests) > 0 {
		b.WriteString("# TYPE drift_model_requests_total counter\n")
		for _, outcome := range sortedStringKeys(snapshot.ModelRequests) {
			fmt.Fprintf(&b, "drift_model_requests_total%s %d\n", mergeLabels(base, "outcome", outcome), snapshot.ModelRequests[outcome])
		}
	}
	if len(snapshot.ToolCalls) > 0 {
		b.WriteString("# TYPE drift_tool_calls_total counter\n")
		for _, key := range sortedToolKeys(snapshot.ToolCalls) {
			fmt.Fprintf(&b, "drift_tool_calls_total%s %d\n", mergeLabels(base, "tool", key.Tool, "category", key.Category, "outcome", key.Outcome), snapshot.ToolCalls[key])
		}
	}
	if len(snapshot.PermissionDecisions) > 0 {
		b.WriteString("# TYPE drift_permission_decisions_total counter\n")
		for _, key := range sortedPermissionKeys(snapshot.PermissionDecisions) {
			fmt.Fprintf(&b, "drift_permission_decisions_total%s %d\n", mergeLabels(base, "tool", key.Tool, "decision", key.Decision), snapshot.PermissionDecisions[key])
		}
	}
	if len(snapshot.SandboxExecutions) > 0 {
		b.WriteString("# TYPE drift_sandbox_executions_total counter\n")
		for _, key := range sortedSandboxKeys(snapshot.SandboxExecutions) {
			fmt.Fprintf(&b, "drift_sandbox_executions_total%s %d\n", mergeLabels(base, "backend", key.Backend, "sandboxed", key.Sandboxed, "outcome", key.Outcome), snapshot.SandboxExecutions[key])
		}
	}
	if len(snapshot.CommandPolicyDecisions) > 0 {
		b.WriteString("# TYPE drift_command_policy_decisions_total counter\n")
		for _, key := range sortedCommandPolicyKeys(snapshot.CommandPolicyDecisions) {
			fmt.Fprintf(&b, "drift_command_policy_decisions_total%s %d\n", mergeLabels(base, "risk", key.Risk, "outcome", key.Outcome, "rule_id", key.RuleID), snapshot.CommandPolicyDecisions[key])
		}
	}
	return b.String()
}

func writeHistogram(b *strings.Builder, name, base string, seconds float64) {
	buckets := []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120}
	for _, bound := range buckets {
		count := 0
		if seconds <= bound {
			count = 1
		}
		fmt.Fprintf(b, "%s_bucket%s %d\n", name, mergeLabels(base, "le", strconv.FormatFloat(bound, 'f', -1, 64)), count)
	}
	fmt.Fprintf(b, "%s_bucket%s 1\n", name, mergeLabels(base, "le", "+Inf"))
	fmt.Fprintf(b, "%s_sum%s %s\n", name, base, strconv.FormatFloat(seconds, 'f', -1, 64))
	fmt.Fprintf(b, "%s_count%s 1\n", name, base)
}

func labels(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var pairs []string
	for _, key := range keys {
		pairs = append(pairs, key+"=\""+escapeLabel(values[key])+"\"")
	}
	return "{" + strings.Join(pairs, ",") + "}"
}
func mergeLabels(base string, pairs ...string) string {
	values := map[string]string{}
	for index := 0; index+1 < len(pairs); index += 2 {
		values[pairs[index]] = pairs[index+1]
	}
	if len(values) == 0 {
		return base
	}
	suffix := make([]string, 0, len(values))
	for key, value := range values {
		suffix = append(suffix, key+"=\""+escapeLabel(value)+"\"")
	}
	sort.Strings(suffix)
	return strings.TrimSuffix(base, "}") + "," + strings.Join(suffix, ",") + "}"
}
func escapeLabel(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(value)
}
func sortedStringKeys(values map[string]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func sortedToolKeys(values map[ToolKey]uint64) []ToolKey {
	keys := make([]ToolKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Tool+keys[i].Category+keys[i].Outcome < keys[j].Tool+keys[j].Category+keys[j].Outcome
	})
	return keys
}
func sortedPermissionKeys(values map[PermissionKey]uint64) []PermissionKey {
	keys := make([]PermissionKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Tool+keys[i].Decision < keys[j].Tool+keys[j].Decision })
	return keys
}
func sortedSandboxKeys(values map[SandboxKey]uint64) []SandboxKey {
	keys := make([]SandboxKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Backend+keys[i].Sandboxed+keys[i].Outcome < keys[j].Backend+keys[j].Sandboxed+keys[j].Outcome
	})
	return keys
}
func sortedCommandPolicyKeys(values map[CommandPolicyKey]uint64) []CommandPolicyKey {
	keys := make([]CommandPolicyKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Risk+keys[i].Outcome+keys[i].RuleID < keys[j].Risk+keys[j].Outcome+keys[j].RuleID
	})
	return keys
}
