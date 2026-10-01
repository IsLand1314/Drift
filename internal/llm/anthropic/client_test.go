package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/llm"
)

func TestStreamMapsRequestAndSSE(t *testing.T) {
	const secret = "anthropic-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != secret {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q", got)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		var model string
		if err := json.Unmarshal(body["model"], &model); err != nil || model != "claude-test" {
			t.Fatalf("model = %q, %v", model, err)
		}
		var maxTokens int
		if err := json.Unmarshal(body["max_tokens"], &maxTokens); err != nil || maxTokens != 4096 {
			t.Fatalf("max_tokens = %d, %v", maxTokens, err)
		}
		var system string
		if err := json.Unmarshal(body["system"], &system); err != nil || system != "read-only system" {
			t.Fatalf("system = %q, %v", system, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeAnthropicEvent(w, `{"type":"message_start","message":{"usage":{"input_tokens":11}}}`)
		writeAnthropicEvent(w, `{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`)
		writeAnthropicEvent(w, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`)
		writeAnthropicEvent(w, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`)
		writeAnthropicEvent(w, `{"type":"message_stop"}`)
	}))
	defer server.Close()

	client, err := New(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	completion, err := client.Stream(context.Background(), llm.Request{
		Model: "claude-test",
		Messages: []llm.Message{
			{Role: "system", Content: "read-only system"},
			{Role: "user", Content: "hello"},
		},
	}, func(event llm.StreamEvent) error {
		text.WriteString(event.Text)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "hello" || completion.Assistant.Content != "hello" || completion.FinishReason != "end_turn" {
		t.Fatalf("completion = %+v, text=%q", completion, text.String())
	}
	if completion.Usage == nil || completion.Usage.InputTokens != 11 || completion.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", completion.Usage)
	}
}

func TestStreamMapsToolsAndResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Name        string                 `json:"name"`
				InputSchema map[string]interface{} `json:"input_schema"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Name != "read_file" || body.Tools[0].InputSchema["type"] != "object" {
			t.Fatalf("tools = %+v", body.Tools)
		}
		if len(body.Messages) != 3 || body.Messages[1].Role != "assistant" || body.Messages[2].Role != "user" {
			t.Fatalf("messages = %+v", body.Messages)
		}
		var assistant []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body.Messages[1].Content, &assistant); err != nil {
			t.Fatal(err)
		}
		if len(assistant) != 1 || assistant[0].Type != "tool_use" || assistant[0].ID != "call-1" || assistant[0].Name != "read_file" {
			t.Fatalf("assistant blocks = %+v", assistant)
		}
		var results []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   string `json:"content"`
		}
		if err := json.Unmarshal(body.Messages[2].Content, &results); err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Type != "tool_result" || results[0].ToolUseID != "call-1" || results[0].Content != "file text" {
			t.Fatalf("result blocks = %+v", results)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeAnthropicEvent(w, `{"type":"message_start","message":{"usage":{"input_tokens":1}}}`)
		writeAnthropicEvent(w, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-2","name":"read_file"}}`)
		writeAnthropicEvent(w, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`)
		writeAnthropicEvent(w, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`)
		writeAnthropicEvent(w, `{"type":"message_stop"}`)
	}))
	defer server.Close()
	client, err := New(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.Stream(context.Background(), llm.Request{
		Model: "claude-test",
		Messages: []llm.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "read README"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
			{Role: "tool", ToolCallID: "call-1", Content: "file text"},
		},
		Tools: []llm.ToolDefinition{{Type: "function", Function: json.RawMessage(`{"name":"read_file","description":"read","parameters":{"type":"object"}}`)}},
	}, func(llm.StreamEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.Assistant.ToolCalls) != 1 || completion.Assistant.ToolCalls[0].Arguments != `{"path":"README.md"}` {
		t.Fatalf("tool calls = %+v", completion.Assistant.ToolCalls)
	}
}

func TestStreamRejectsHTTPBodyWithoutLeakingKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("secret response body"))
	}))
	defer server.Close()
	client, err := New(server.URL, "secret-key")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Stream(context.Background(), llm.Request{Model: "test"}, func(llm.StreamEvent) error { return nil })
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %v", err)
	}
}

func writeAnthropicEvent(w http.ResponseWriter, data string) {
	_, _ = w.Write([]byte("data: " + data + "\n\n"))
}
