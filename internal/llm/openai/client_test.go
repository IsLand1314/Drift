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

	"gitee.com/island0920/drift/internal/llm"
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
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Stream || req.Model != "test-model" || len(req.Messages) != 1 || req.Messages[0].Content != "你好" {
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
	err = client.Stream(ctx, llm.Request{Model: "test-model", Messages: []llm.Message{{Role: "user", Content: "你好"}}}, func(s string) error {
		got += s
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
			err := readStream(strings.NewReader(tc.body), func(string) error { return nil })
			if (err == nil) != tc.ok {
				t.Fatalf("error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("leaked error body")
			}
		})
	}
	errOutput := errors.New("output closed")
	err := readStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"), func(string) error { return errOutput })
	if !errors.Is(err, errOutput) {
		t.Fatalf("output error lost: %v", err)
	}
}

func TestHTTPErrorAndCancellation(t *testing.T) {
	for _, status := range []int{401, 429, 500, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); io.WriteString(w, "secret") }))
			defer server.Close()
			client, _ := New(server.URL, "key")
			err := client.Stream(context.Background(), llm.Request{}, func(string) error { return nil })
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
	err := client.Stream(ctx, llm.Request{}, func(string) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
