package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPushgatewayPublisherWritesOnlyMetricExposition(t *testing.T) {
	var method, path, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	publisher, err := NewPushgatewayPublisher(server.URL, "local")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{
		Mode: "prompt", Provider: "deepseek", Protocol: "openai-compat", Outcome: OutcomeSuccess,
		Duration:      3 * time.Second,
		ModelRequests: map[string]uint64{"success": 1},
		ToolCalls:     map[ToolKey]uint64{{Tool: "mcp", Category: "mcp", Outcome: "failed"}: 1},
	}
	if err := publisher.Publish(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/metrics/job/drift/instance/local" {
		t.Fatalf("request = %s %s", method, path)
	}
	for _, required := range []string{"drift_runs_total", "drift_run_duration_seconds", "drift_model_requests_total", "drift_tool_calls_total"} {
		if !strings.Contains(body, required) {
			t.Fatalf("metric exposition missing %q:\n%s", required, body)
		}
	}
	for _, forbidden := range []string{"private", "token", "command", "path="} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metric exposition leaked %q:\n%s", forbidden, body)
		}
	}
}
