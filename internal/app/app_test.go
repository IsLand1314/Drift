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
			if request.Model != "test" || len(request.Messages) != 2 || request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, "read_file") || !strings.Contains(request.Messages[0].Content, "run_command") || !strings.Contains(request.Messages[0].Content, "DSML") || request.Messages[1].Role != "user" || request.Messages[1].Content != "explain app.go" {
				t.Errorf("first request messages = %#v", request.Messages)
			}
			if len(request.Tools) != 1 || request.Tools[0].Type != "function" {
				t.Errorf("first request tools = %#v", request.Tools)
			} else {
				var definition struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(request.Tools[0].Function, &definition); err != nil || definition.Name != "read_file" {
					t.Errorf("read_file schema = %s, %v", request.Tools[0].Function, err)
				}
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"app.go\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if request.Model != "test" || len(request.Tools) != 0 {
				t.Errorf("second request tools = %#v, want none", request.Tools)
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
			if request.Model != "test" || len(request.Messages) != 2 || request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, "read_file") || !strings.Contains(request.Messages[0].Content, "run_command") || !strings.Contains(request.Messages[0].Content, "DSML") || request.Messages[1].Role != "user" || request.Messages[1].Content != "总结项目" {
				t.Errorf("first request messages = %#v", request.Messages)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-app\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"app.go\\\"}\"}},{\"index\":1,\"id\":\"call-app-test\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"app_test.go\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if request.Model != "test" || len(request.Tools) != 0 {
				t.Errorf("second request model=%q tools=%#v", request.Model, request.Tools)
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
