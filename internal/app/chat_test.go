package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestChatPreservesConversationAcrossTurns(t *testing.T) {
	root := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCalls  []any  `json:"tool_calls"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests {
		case 1:
			if len(request.Messages) != 2 || request.Messages[1].Content != "first question" {
				t.Errorf("first request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if len(request.Messages) != 4 || request.Messages[1].Content != "first question" || request.Messages[2].Role != "assistant" || request.Messages[2].Content != "first answer" || request.Messages[3].Content != "second question" {
				t.Errorf("second request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"second answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected request %d", requests)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var in, out, stderr bytes.Buffer
	in.WriteString("first question\nsecond question\nexit\n")
	if code := RunWithInput(context.Background(), []string{"chat"}, getenv, &in, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "first answer") || !strings.Contains(out.String(), "second answer") || requests != 2 {
		t.Fatalf("out=%q requests=%d", out.String(), requests)
	}
	files, err := filepath.Glob(filepath.Join(root, ".drift", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("sessions = %#v, want one chat audit file", files)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "first question") || strings.Contains(string(content), "first answer") || !strings.Contains(string(content), `"type":"run_started"`) {
		t.Fatalf("chat audit leaked context or missed run_started: %s", content)
	}
}

func TestChatExitAndEOFDoNotCallProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{name: "exit", in: "exit\n"},
		{name: "slash-exit", in: "/exit\n"},
		{name: "quit", in: "quit\n"},
		{name: "eof", in: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			getenv := func(key string) string {
				if key == "OPENAI_API_KEY" {
					return "test-secret"
				}
				if key == "OPENAI_MODEL" {
					return "test-model"
				}
				return "http://127.0.0.1:1/v1"
			}
			var out, stderr bytes.Buffer
			if code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, strings.NewReader(tc.in), &out, &stderr); code != 0 {
				t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
			}
		})
	}
}

func TestChatMissingConfigDoesNotReadInput(t *testing.T) {
	getenv := func(string) string { return "" }
	in := strings.NewReader("this must not be sent\n")
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "-w", t.TempDir()}, getenv, in, &out, &stderr); code != 2 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestChatReportsEmptyProviderResponse(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, strings.NewReader("question\n"), &out, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "empty response") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	files, err := filepath.Glob(filepath.Join(root, ".drift", "sessions", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("sessions=%v err=%v", files, err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(content), `"stage":"agent_empty_response"`) {
		t.Fatalf("audit=%s err=%v", content, err)
	}
}

func TestChatClearResetsRunnerContext(t *testing.T) {
	root := t.TempDir()
	requests := 0
	var secondRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if requests == 2 {
			secondRequest = string(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer-%d\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", requests)
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	in := strings.NewReader("first\n/clear\nsecond\nexit\n")
	if code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, in, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if requests != 2 || strings.Contains(secondRequest, "first") {
		t.Fatalf("requests=%d second_request_contains_first=%v", requests, strings.Contains(secondRequest, "first"))
	}
}

func TestChatContextLimitCanRecoverWithClear(t *testing.T) {
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	largePrompt := strings.Repeat("x", agent.MaxConversationBytes-100)
	in := strings.NewReader(largePrompt + "\n/clear\nsecond\nexit\n")
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, in, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if requests != 1 || !strings.Contains(stderr.String(), "上下文已达到上限") || !strings.Contains(out.String(), "recovered") {
		t.Fatalf("requests=%d out=%q stderr=%q", requests, out.String(), stderr.String())
	}
}
