package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSelectsAnthropicProvider(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("x-api-key") != "anthropic-test-key" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"anthropic answer\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	workspace := t.TempDir()
	env := map[string]string{"ANTHROPIC_API_KEY": "anthropic-test-key"}
	getenv := func(key string) string { return env[key] }
	var out, stderr strings.Builder
	code := RunWithInput(context.Background(), []string{"-provider", "anthropic", "-base-url", server.URL, "-model", "claude-test", "-w", workspace, "-p", "hello"}, getenv, strings.NewReader(""), &out, &stderr)
	if code != 0 || out.String() != "anthropic answer\n" || requests != 1 {
		t.Fatalf("code=%d out=%q stderr=%q requests=%d", code, out.String(), stderr.String(), requests)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".drift", "sessions")); err != nil {
		t.Fatalf("session directory missing: %v", err)
	}
}

func TestRunRejectsUnknownProviderBeforeRequest(t *testing.T) {
	var out, stderr strings.Builder
	code := RunWithInput(context.Background(), []string{"-provider", "unknown", "-p", "hello"}, func(string) string { return "key" }, strings.NewReader(""), &out, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "Provider") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunRejectsMissingAnthropicKeyBeforeRequest(t *testing.T) {
	var out, stderr strings.Builder
	code := RunWithInput(context.Background(), []string{"-provider", "anthropic", "-model", "claude-test", "-p", "hello"}, func(key string) string {
		if key == "ANTHROPIC_MODEL" {
			return "claude-test"
		}
		return ""
	}, strings.NewReader(""), &out, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "ANTHROPIC_API_KEY") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
