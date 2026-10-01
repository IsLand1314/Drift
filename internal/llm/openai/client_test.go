package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

func TestStreamIsIncremental(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			llm.Request
			Stream        bool `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Stream || !req.StreamOptions.IncludeUsage || req.Model != "test-model" || len(req.Messages) != 1 || req.Messages[0].Content != "你好" {
			t.Errorf("unexpected body: %+v, %v", req, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-first:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"好\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := New(server.URL+"/v1/", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	_, err = client.Stream(ctx, llm.Request{Model: "test-model", Messages: []llm.Message{{Role: "user", Content: "你好"}}}, func(event llm.StreamEvent) error {
		got += event.Text
		if got == "你" {
			close(first)
		}
		return nil
	})
	if err != nil || got != "你好" {
		t.Fatalf("got %q, error %v", got, err)
	}
}

func TestStreamFailuresAndFraming(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"CRLF and multiline", ": heartbeat\r\ndata: {\r\ndata: \"choices\":[]}\r\n\r\ndata: [DONE]\r\n\r\n", true},
		{"truncated", "data: {\"choices\":[]}\n\n", false},
		{"invalid JSON", "data: broken\n\n", false},
		{"provider error", "data: {\"error\":{\"message\":\"secret\"}}\n\n", false},
		{"length limit", "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\n", false},
		{"large event", "data: " + strings.Repeat("x", 1<<20) + "\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readStream(strings.NewReader(tc.body), func(llm.StreamEvent) error { return nil })
			if (err == nil) != tc.ok {
				t.Fatalf("error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("leaked error body")
			}
		})
	}
	errOutput := errors.New("output closed")
	_, err := readStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"), func(llm.StreamEvent) error { return errOutput })
	if !errors.Is(err, errOutput) {
		t.Fatalf("output error lost: %v", err)
	}
}

func TestStreamReportsTimeoutAndOversizedLinePrecisely(t *testing.T) {
	client := &Client{
		endpoint: "https://example.test/chat/completions",
		key:      "test-key",
		http: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		})},
	}
	if _, err := client.Stream(context.Background(), llm.Request{}, func(llm.StreamEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("Stream() error = %v, want timeout detail", err)
	}
	_, err := readStream(strings.NewReader("data: "+strings.Repeat("x", 1<<20)+"\n\n"), func(llm.StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "单行超过") {
		t.Fatalf("readStream() error = %v, want oversized-line detail", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestHTTPErrorAndCancellation(t *testing.T) {
	for _, status := range []int{401, 429, 500, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); io.WriteString(w, "secret") }))
			defer server.Close()
			client, _ := New(server.URL, "key")
			_, err := client.Stream(context.Background(), llm.Request{}, func(llm.StreamEvent) error { return nil })
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error: %v", err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := New(server.URL, "key")
	_, err := client.Stream(ctx, llm.Request{}, func(llm.StreamEvent) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestStreamAggregatesToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools    []llm.ToolDefinition `json:"tools"`
			Messages []struct {
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tools) != 1 || req.Tools[0].Type != "function" || len(req.Messages) != 1 || len(req.Messages[0].ToolCalls) != 1 {
			t.Fatalf("unexpected tools: %+v, %v", req.Tools, err)
		}
		call := req.Messages[0].ToolCalls[0]
		if call.ID != "call_1" || call.Type != "function" || call.Function.Name != "read_file" || call.Function.Arguments != `{"path":"README.md"}` {
			t.Fatalf("unexpected serialized call: %+v", call)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"REA\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"checking file\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"DME.md\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := New(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.Stream(context.Background(), llm.Request{
		Model: "test-model",
		Messages: []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`,
		}}}},
		Tools: []llm.ToolDefinition{{
			Type:     "function",
			Function: json.RawMessage(`{"name":"read_file","parameters":{"type":"object"}}`),
		}},
	}, func(llm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if completion.FinishReason != "tool_calls" || completion.Assistant.ReasoningContent != "checking file" || len(completion.Assistant.ToolCalls) != 1 {
		t.Fatalf("unexpected completion: %+v", completion)
	}
	call := completion.Assistant.ToolCalls[0]
	if call.ID != "call_1" || call.Type != "function" || call.Name != "read_file" || call.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected call: %+v", call)
	}
}

func TestReadStreamProtocolRegressions(t *testing.T) {
	t.Run("usage-only chunk", func(t *testing.T) {
		body := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":9,\"total_tokens\":26}}\n\ndata: [DONE]\n\n"
		completion, err := readStream(strings.NewReader(body), func(llm.StreamEvent) error { return nil })
		if err != nil || completion.Usage == nil || completion.Usage.InputTokens != 17 || completion.Usage.OutputTokens != 9 || completion.Usage.TotalTokens != 26 {
			t.Fatalf("unexpected usage: %+v, %v", completion.Usage, err)
		}
	})

	t.Run("missing usage remains unavailable", func(t *testing.T) {
		completion, err := readStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), func(llm.StreamEvent) error { return nil })
		if err != nil || completion.Usage != nil {
			t.Fatalf("unexpected completion: %+v, %v", completion, err)
		}
	})

	t.Run("stop text", func(t *testing.T) {
		completion, err := readStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), func(llm.StreamEvent) error { return nil })
		if err != nil || completion.Assistant.Content != "hello" || completion.FinishReason != "stop" {
			t.Fatalf("unexpected completion: %+v, %v", completion, err)
		}
	})

	t.Run("two indexes preserve malformed arguments", func(t *testing.T) {
		body := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_2\",\"type\":\"function\",\"function\":{\"name\":\"second\",\"arguments\":\"[broken\"}},{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"first\",\"arguments\":\"{}\"}}]}}]}\n\ndata: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
		completion, err := readStream(strings.NewReader(body), func(llm.StreamEvent) error { return nil })
		if err != nil || len(completion.Assistant.ToolCalls) != 2 || completion.Assistant.ToolCalls[0].Name != "first" || completion.Assistant.ToolCalls[1].Arguments != "[broken" {
			t.Fatalf("unexpected completion: %+v, %v", completion, err)
		}
	})

	t.Run("callback failure", func(t *testing.T) {
		errCallback := errors.New("output closed")
		_, err := readStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"), func(llm.StreamEvent) error { return errCallback })
		if !errors.Is(err, errCallback) {
			t.Fatalf("callback error lost: %v", err)
		}
	})

	t.Run("length finish reason", func(t *testing.T) {
		completion, err := readStream(strings.NewReader("data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"), func(llm.StreamEvent) error { return nil })
		if err != nil || completion.FinishReason != "length" {
			t.Fatalf("unexpected completion: %+v, %v", completion, err)
		}
	})
}
