package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunLoadsDotEnv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"dotenv answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_API_KEY=dotenv-secret\nOPENAI_MODEL=dotenv-model\nOPENAI_BASE_URL="+server.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "hello"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "dotenv answer\n" || stderr.String() != "" {
		t.Fatalf("out=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestRunTraceKeepsAnswerOnStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	root := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--trace", "-p", "hello"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "answer\n" {
		t.Fatalf("stdout = %q, want final answer only", out.String())
	}
	trace := stderr.String()
	if !strings.Contains(trace, "run_started") || !strings.Contains(trace, "run_finished") {
		t.Fatalf("trace = %q, want lifecycle events", trace)
	}
	if strings.Contains(trace, "answer") || strings.Contains(trace, "test-secret") {
		t.Fatalf("trace leaked answer or secret: %q", trace)
	}
}

func TestRunFlagConfigOverridesProcessAndDotEnv(t *testing.T) {
	type observation struct {
		model string
		path  string
	}
	newServer := func(observed chan<- observation) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			observed <- observation{model: request.Model, path: r.URL.Path}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}))
	}
	dotenvObserved := make(chan observation, 1)
	envObserved := make(chan observation, 1)
	flagObserved := make(chan observation, 1)
	dotenvServer := newServer(dotenvObserved)
	envServer := newServer(envObserved)
	flagServer := newServer(flagObserved)
	defer dotenvServer.Close()
	defer envServer.Close()
	defer flagServer.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_API_KEY=dotenv-secret\nOPENAI_MODEL=dotenv-model\nOPENAI_BASE_URL="+dotenvServer.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	getenv := func(key string) string {
		return map[string]string{
			"OPENAI_API_KEY":  "process-secret",
			"OPENAI_MODEL":    "process-model",
			"OPENAI_BASE_URL": envServer.URL,
		}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "process config"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("process config code=%d stderr=%q", code, stderr.String())
	}
	select {
	case got := <-envObserved:
		if got.model != "process-model" || got.path != "/chat/completions" {
			t.Fatalf("process config observation=%+v", got)
		}
	case <-dotenvObserved:
		t.Fatal("dotenv endpoint used despite process override")
	case <-time.After(time.Second):
		t.Fatal("process endpoint did not receive a request")
	}
	out.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"-p", "flag config", "-model", "flag-model", "-base-url", flagServer.URL}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("flag config code=%d stderr=%q", code, stderr.String())
	}
	select {
	case got := <-flagObserved:
		if got.model != "flag-model" || got.path != "/chat/completions" {
			t.Fatalf("flag config observation=%+v", got)
		}
	case <-envObserved:
		t.Fatal("process endpoint used despite flag override")
	case <-dotenvObserved:
		t.Fatal("dotenv endpoint used despite flag override")
	case <-time.After(time.Second):
		t.Fatal("flag endpoint did not receive a request")
	}
	if strings.Contains(out.String()+stderr.String(), "secret") {
		t.Fatal("configuration secret leaked")
	}
}

func TestRunReadRoundTrip(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Type     string          `json:"type"`
				Function json.RawMessage `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests {
		case 1:
			if request.Model != "test" || len(request.Messages) != 2 || request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, "native read-only tools") || !strings.Contains(request.Messages[0].Content, "run_command") || !strings.Contains(request.Messages[0].Content, "DSML") || request.Messages[1].Role != "user" || request.Messages[1].Content != "explain app.go" {
				t.Errorf("first request messages = %#v", request.Messages)
			}
			if len(request.Tools) != 3 || request.Tools[0].Type != "function" || request.Tools[1].Type != "function" || request.Tools[2].Type != "function" {
				t.Errorf("first request tools = %#v, want three native tools", request.Tools)
			} else {
				var definition struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(request.Tools[2].Function, &definition); err != nil || definition.Name != "read_file" {
					t.Errorf("read_file schema = %s, %v", request.Tools[2].Function, err)
				}
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"app.go\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if request.Model != "test" || len(request.Tools) != 3 {
				t.Errorf("second request tools = %#v, want three native tools", request.Tools)
			}
			if len(request.Messages) != 3 || request.Messages[1].Role != "assistant" || len(request.Messages[1].ToolCalls) != 1 || request.Messages[1].ToolCalls[0].ID != "call-1" || request.Messages[1].ToolCalls[0].Function.Name != "read_file" || request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "call-1" || !strings.Contains(request.Messages[2].Content, "package app") {
				t.Errorf("second request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"最终解释\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected request %d", requests)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "explain app.go"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "最终解释\n" || stderr.String() != "" || requests != 2 {
		t.Fatalf("out=%q stderr=%q requests=%d", out.String(), stderr.String(), requests)
	}
}

func TestRunReadRoundTripMultipleFiles(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests {
		case 1:
			if request.Model != "test" || len(request.Messages) != 2 || request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, "native read-only tools") || !strings.Contains(request.Messages[0].Content, "run_command") || !strings.Contains(request.Messages[0].Content, "DSML") || request.Messages[1].Role != "user" || request.Messages[1].Content != "总结项目" {
				t.Errorf("first request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-app\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"app.go\\\"}\"}},{\"index\":1,\"id\":\"call-app-test\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"app_test.go\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if request.Model != "test" || len(request.Tools) != 3 {
				t.Errorf("second request model=%q tools=%#v, want three native tools", request.Model, request.Tools)
			}
			if len(request.Messages) != 4 || request.Messages[1].Role != "assistant" || len(request.Messages[1].ToolCalls) != 2 || request.Messages[1].ToolCalls[0].ID != "call-app" || request.Messages[1].ToolCalls[0].Function.Name != "read_file" || request.Messages[1].ToolCalls[0].Function.Arguments != `{"path":"app.go"}` || request.Messages[1].ToolCalls[1].ID != "call-app-test" || request.Messages[1].ToolCalls[1].Function.Name != "read_file" || request.Messages[1].ToolCalls[1].Function.Arguments != `{"path":"app_test.go"}` || request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "call-app" || !strings.Contains(request.Messages[2].Content, "package app") || request.Messages[3].Role != "tool" || request.Messages[3].ToolCallID != "call-app-test" || !strings.Contains(request.Messages[3].Content, "TestRunReadRoundTrip") {
				t.Errorf("second request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"项目摘要\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected request %d", requests)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "总结项目"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "项目摘要\n" || stderr.String() != "" || requests != 2 {
		t.Fatalf("out=%q stderr=%q requests=%d", out.String(), stderr.String(), requests)
	}
}

func TestRunMultiTurnExplorationRoundTripAndSessionAudit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "README.md"), []byte("needle in readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
			Model    string `json:"model"`
			Messages []struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request %d: %v", requests, err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if request.Model != "test" {
			t.Errorf("request %d model = %q, want test", requests, request.Model)
		}
		wantTools := []string{"list_files", "search_text", "read_file"}
		if len(request.Tools) != len(wantTools) {
			t.Errorf("request %d tools = %#v, want three native schemas", requests, request.Tools)
		} else {
			for i, want := range wantTools {
				if request.Tools[i].Type != "function" || request.Tools[i].Function.Name != want {
					t.Errorf("request %d tool %d = %#v, want %q function schema", requests, i, request.Tools[i], want)
				}
			}
		}
		switch requests {
		case 1:
			if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" || request.Messages[1].Content != "探索项目" {
				t.Errorf("first request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-list\",\"type\":\"function\",\"function\":{\"name\":\"list_files\",\"arguments\":\"{\\\"path\\\":\\\"src\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if len(request.Messages) != 3 || request.Messages[1].Role != "assistant" || request.Messages[1].ToolCalls[0].ID != "call-list" || request.Messages[2].Role != "tool" || request.Messages[2].ToolCallID != "call-list" || request.Messages[2].Content != "src/README.md\nsrc/main.go" {
				t.Errorf("second request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-search\",\"type\":\"function\",\"function\":{\"name\":\"search_text\",\"arguments\":\"{\\\"query\\\":\\\"needle\\\",\\\"path\\\":\\\"src\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 3:
			if len(request.Messages) != 5 || request.Messages[3].Role != "assistant" || request.Messages[3].ToolCalls[0].ID != "call-search" || request.Messages[3].ToolCalls[0].Function.Arguments != `{"query":"needle","path":"src"}` || request.Messages[4].Role != "tool" || request.Messages[4].ToolCallID != "call-search" || request.Messages[4].Content != "src/README.md:1: needle in readme" {
				t.Errorf("third request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-read\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"src/README.md\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 4:
			if len(request.Messages) != 7 || request.Messages[5].Role != "assistant" || request.Messages[5].ToolCalls[0].ID != "call-read" || request.Messages[5].ToolCalls[0].Function.Arguments != `{"path":"src/README.md"}` || request.Messages[6].Role != "tool" || request.Messages[6].ToolCallID != "call-read" || request.Messages[6].Content != "needle in readme\n" {
				t.Errorf("fourth request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"项目\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"摘要\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected request %d", requests)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "探索项目"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "项目摘要\n" || stderr.String() != "" || requests != 4 {
		t.Fatalf("out=%q stderr=%q requests=%d", out.String(), stderr.String(), requests)
	}

	files, err := filepath.Glob(filepath.Join(root, ".drift", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("session files = %#v, want one JSONL file", files)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var entry struct {
			Type       string `json:"type"`
			ToolCallID string `json:"tool_call_id"`
			Tool       string `json:"tool"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid session line %q: %v", line, err)
		}
		counts[entry.Type]++
	}
	for eventType, want := range map[string]int{"run_started": 1, "tool_call": 3, "tool_result": 3, "text_delta": 2, "run_finished": 1} {
		if counts[eventType] != want {
			t.Fatalf("session event %q count = %d, want %d; all counts = %#v", eventType, counts[eventType], want, counts)
		}
	}
}

func TestRunWritesSessionAudit(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests {
		case 1:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"最终回答\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("session fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	getenv := func(key string) string {
		return map[string]string{
			"OPENAI_API_KEY":  "test-secret",
			"OPENAI_MODEL":    "test",
			"OPENAI_BASE_URL": server.URL,
		}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "解释 README.md"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "最终回答\n" || stderr.String() != "" || requests != 2 {
		t.Fatalf("out=%q stderr=%q requests=%d", out.String(), stderr.String(), requests)
	}

	files, err := filepath.Glob(filepath.Join(root, ".drift", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("session files = %#v, want one JSONL file", files)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var entry struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid session line %q: %v", line, err)
		}
		seen[entry.Type] = true
	}
	for _, eventType := range []string{"run_started", "tool_call", "tool_result", "text_delta", "run_finished"} {
		if !seen[eventType] {
			t.Fatalf("session events = %#v, missing %q", seen, eventType)
		}
	}
	if strings.Contains(string(content), "test-secret") || strings.Contains(string(content), root) || strings.Contains(string(content), "OPENAI_API_KEY") {
		t.Fatalf("session leaked sensitive data: %s", content)
	}
}

func TestRunDirectAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"直接回答\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "answer directly"}, getenv, &out, &stderr); code != 0 || out.String() != "直接回答\n" || stderr.String() != "" {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestRunWorkspaceDirectory(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("workspace readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Messages []struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
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
			if len(request.Messages) != 2 || request.Messages[0].Role != "system" || strings.Contains(request.Messages[0].Content, "initial focus target") || request.Messages[1].Content != "analyze target" {
				t.Errorf("first request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-read\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if len(request.Messages) != 3 || request.Messages[2].Role != "tool" || request.Messages[2].Content != "workspace readme\n" {
				t.Errorf("second request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"workspace answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected request %d", requests)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-w", workspace, "-p", "analyze target"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "workspace answer\n" || stderr.String() != "" || requests != 2 {
		t.Fatalf("out=%q stderr=%q requests=%d", out.String(), stderr.String(), requests)
	}
	files, err := filepath.Glob(filepath.Join(workspace, ".drift", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("workspace sessions = %#v, want one file", files)
	}
}

func TestRunWorkspaceFileFocusDoesNotAutoRead(t *testing.T) {
	workspace := t.TempDir()
	focus := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(focus, []byte("focus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, `"README.md"`) || request.Messages[1].Content != "explain file" {
			t.Errorf("request messages = %#v", request.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"direct file answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-w", focus, "-p", "explain file"}, getenv, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if out.String() != "direct file answer\n" || stderr.String() != "" || requests != 1 {
		t.Fatalf("out=%q stderr=%q requests=%d", out.String(), stderr.String(), requests)
	}
	files, err := filepath.Glob(filepath.Join(workspace, ".drift", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("workspace sessions = %#v, want one file", files)
	}
}

func TestRunInvalidWorkspaceDoesNotCallProvider(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing")
	if code := Run(context.Background(), []string{"-w", missing, "-p", "x"}, getenv, &out, &stderr); code != 2 || requests != 0 {
		t.Fatalf("code/requests = %d/%d, want 2/0; stderr=%q", code, requests, stderr.String())
	}
}

func TestRunRecordsProviderErrorStage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: broken\n\n")
	}))
	defer server.Close()
	root := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-p", "x"}, getenv, &out, &stderr); code != 1 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	files, err := filepath.Glob(filepath.Join(root, ".drift", "sessions", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files = %#v, %v", files, err)
	}
	var entry struct {
		Type  string `json:"type"`
		Stage string `json:"stage"`
	}
	foundError := false
	for _, line := range strings.Split(strings.TrimSpace(string(mustReadFile(t, files[0]))), "\n") {
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Type == "error" {
			foundError = true
			if entry.Stage != "provider_sse_invalid_json" {
				t.Fatalf("error stage = %q, want provider_sse_invalid_json", entry.Stage)
			}
		}
	}
	if !foundError {
		t.Fatal("session has no error event")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestRunValidation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		configured bool
		code       int
		text       string
	}{
		{"help", []string{"-h"}, false, 0, ""},
		{"missing config", []string{"-p", "hello"}, false, 2, ""},
		{"missing prompt", nil, true, 2, ""},
		{"unknown flag", []string{"-unknown"}, true, 2, ""},
		{"extra argument", []string{"-p", "hello", "extra"}, true, 2, ""},
		{"bad URL", []string{"-p", "hello", "-base-url", "file:///tmp"}, true, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if !tc.configured {
					return ""
				}
				return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test"}[key]
			}
			var out, stderr bytes.Buffer
			code := Run(context.Background(), tc.args, getenv, &out, &stderr)
			if code != tc.code || out.String() != tc.text {
				t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "test-secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}
