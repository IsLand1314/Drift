package app

import (
	"context"
	"encoding/json"
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
	if _, err := os.Stat(filepath.Join(workspace, ".drift", "audits")); err != nil {
		t.Fatalf("session directory missing: %v", err)
	}
}

func TestRunUsesUserConfigTOMLAndAuthJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer config-secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"config answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	workspace := t.TempDir()
	driftDir := filepath.Join(workspace, ".drift")
	if err := os.MkdirAll(driftDir, 0700); err != nil {
		t.Fatal(err)
	}
	configTOML := fmt.Sprintf("version = 1\n[[providers]]\nname = \"deepseek\"\nprotocol = \"openai-compat\"\nbase_url = \"%s\"\nmodel = \"deepseek-test\"\n", server.URL)
	if err := os.WriteFile(filepath.Join(driftDir, "config.toml"), []byte(configTOML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driftDir, "auth.json"), []byte(`{"version":1,"providers":{"deepseek":{"type":"api_key","key":"config-secret"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr strings.Builder
	code := RunWithInput(context.Background(), []string{"-provider", "deepseek", "-w", workspace, "-p", "hello"}, func(string) string { return "" }, strings.NewReader(""), &out, &stderr)
	if code != 0 || out.String() != "config answer\n" {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestRunSwitchesConfiguredProviderProfile(t *testing.T) {
	var selectedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		selectedModel = body.Model
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"selected\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	workspace := t.TempDir()
	driftDir := filepath.Join(workspace, ".drift")
	if err := os.MkdirAll(driftDir, 0700); err != nil {
		t.Fatal(err)
	}
	configTOML := fmt.Sprintf("version = 1\n[[providers]]\nname = \"alpha\"\nprotocol = \"openai-compat\"\nbase_url = \"%s\"\nmodel = \"alpha-model\"\n[[providers]]\nname = \"beta\"\nprotocol = \"openai-compat\"\nbase_url = \"%s\"\nmodel = \"beta-model\"\n", server.URL, server.URL)
	if err := os.WriteFile(filepath.Join(driftDir, "config.toml"), []byte(configTOML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driftDir, "auth.json"), []byte(`{"version":1,"providers":{"alpha":{"type":"api_key","key":"a"},"beta":{"type":"api_key","key":"b"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr strings.Builder
	code := RunWithInput(context.Background(), []string{"-provider", "beta", "-w", workspace, "-p", "hello"}, func(string) string { return "" }, strings.NewReader(""), &out, &stderr)
	if code != 0 || selectedModel != "beta-model" || out.String() != "selected\n" {
		t.Fatalf("code=%d model=%q out=%q stderr=%q", code, selectedModel, out.String(), stderr.String())
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

func TestSkillCommandsRunWithoutProvider(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".drift", "skills", "project-overview", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Use a concise project map."), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr strings.Builder
	if code := RunWithInput(context.Background(), []string{"skill", "list", "-w", workspace}, func(string) string { return "" }, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("list code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if out.String() != "project-overview\n" {
		t.Fatalf("list out=%q", out.String())
	}
	out.Reset()
	if code := RunWithInput(context.Background(), []string{"skill", "show", "project-overview", "-w", workspace}, func(string) string { return "" }, strings.NewReader(""), &out, &stderr); code != 0 || out.String() != "Use a concise project map." {
		t.Fatalf("show code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestRunInjectsSelectedSkill(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".drift", "skills", "project-overview", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Prefer a concise project map."), 0o600); err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-key", "OPENAI_MODEL": "test-model", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr strings.Builder
	code := RunWithInput(context.Background(), []string{"-skill", "project-overview", "-w", workspace, "-p", "hello"}, getenv, strings.NewReader(""), &out, &stderr)
	if code != 0 || out.String() != "ok\n" {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if len(request.Messages) < 2 || !strings.Contains(request.Messages[0].Content, "Prefer a concise project map.") || request.Messages[1].Content != "hello" {
		t.Fatalf("messages=%#v", request.Messages)
	}
}

func TestAnthropicToolRoundTripAndUsage(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("Drift README"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(requests) == 1 {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call-read\",\"name\":\"ReadFile\"}}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":4}}\n\n")
		} else {
			var messages []struct {
				Role    string            `json:"role"`
				Content []json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(body["messages"], &messages); err != nil {
				t.Fatal(err)
			}
			if len(messages) < 3 || messages[len(messages)-1].Role != "user" || !strings.Contains(string(messages[len(messages)-1].Content[0]), "tool_result") {
				t.Errorf("messages do not contain tool_result: %+v", messages)
			}
			_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":20}}}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"README says Drift\"}}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":6}}\n\n")
		}
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"ANTHROPIC_API_KEY": "test-key"}[key]
	}
	var out, stderr strings.Builder
	code := RunWithInput(context.Background(), []string{"-provider", "anthropic", "-base-url", server.URL, "-model", "claude-test", "-w", workspace, "-p", "read README.md"}, getenv, strings.NewReader(""), &out, &stderr)
	if code != 0 || out.String() != "README says Drift\n" || len(requests) != 2 {
		t.Fatalf("code=%d out=%q stderr=%q requests=%d", code, out.String(), stderr.String(), len(requests))
	}
}
