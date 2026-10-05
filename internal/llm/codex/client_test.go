package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

// Test-only access to the protocol parser; production callers use StreamEvents.
func (c *Client) Stream(ctx context.Context, request llm.Request, emit func(llm.StreamEvent) error) (testCompletion, error) {
	return collectStream(func(out func(llm.Event) error) error { return c.StreamEvents(ctx, request, out) }, emit)
}

func collectStream(run func(func(llm.Event) error) error, emit func(llm.StreamEvent) error) (testCompletion, error) {
	completion := testCompletion{Assistant: llm.Message{Role: "assistant"}}
	err := run(func(event llm.Event) error {
		switch e := event.(type) {
		case llm.TextDelta:
			completion.Assistant.Content += e.Text
			return emit(llm.StreamEvent{Text: e.Text})
		case llm.StreamEnd:
			completion.FinishReason, completion.Usage = e.FinishReason, e.Usage
		}
		return nil
	})
	return completion, err
}

func TestStreamUsesAppServerTextProtocol(t *testing.T) {
	t.Setenv("DRIFT_CODEX_TEST_HELPER", "1")
	c, err := NewWithCommand(os.Args[0], []string{"-test.run=TestCodexHelperProcess"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	completion, err := c.Stream(context.Background(), llm.Request{Model: "gpt-5.6-luna", Messages: []llm.Message{{Role: "user", Content: "hello"}}}, func(event llm.StreamEvent) error { got += event.Text; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello from codex" || completion.Assistant.Content != got || completion.FinishReason != "stop" {
		t.Fatalf("got=%q completion=%+v", got, completion)
	}
}

func TestCodexRejectsToolRequests(t *testing.T) {
	t.Setenv("DRIFT_CODEX_TEST_HELPER", "1")
	c, err := NewWithCommand(os.Args[0], []string{"-test.run=TestCodexHelperProcess"}, "")
	if err != nil {
		t.Fatal(err)
	}
	r := llm.Request{Model: "gpt-5.6-luna", Messages: []llm.Message{{Role: "user", Content: "hello"}}}
	r.Tools = []llm.ToolDefinition{{Type: "function"}}
	if _, err := c.Stream(context.Background(), r, func(llm.StreamEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), "text-only") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexReportsMissingAppServer(t *testing.T) {
	c, err := NewWithCommand("drift-command-that-does-not-exist", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "hello"}}}, func(llm.StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "启动失败") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexAuthFailureIsRetryableBeforeOutput(t *testing.T) {
	if !isCodexAuthFailure(&llm.ProviderError{Stage: llm.ErrorStageHTTP, Message: "Codex 登录态无效"}) {
		t.Fatal("Codex auth failure was not recognized")
	}
	if isCodexAuthFailure(&llm.ProviderError{Stage: llm.ErrorStageHTTP, Message: "模型请求失败"}) {
		t.Fatal("generic HTTP failure was classified as auth failure")
	}
}

func TestCodexReloadsAppServerOnceAfterAuthFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "auth-retry.marker")
	t.Setenv("DRIFT_CODEX_TEST_HELPER", "1")
	t.Setenv("DRIFT_CODEX_AUTH_ONCE_FILE", marker)
	c, err := NewWithCommand(os.Args[0], []string{"-test.run=TestCodexHelperProcess"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	_, err = c.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "hello"}}}, func(event llm.StreamEvent) error {
		got += event.Text
		return nil
	})
	if err != nil || got != "hello from codex" {
		t.Fatalf("err=%v output=%q", err, got)
	}
}

func TestCodexKeepsSystemInstructionsOutOfUserInput(t *testing.T) {
	t.Setenv("DRIFT_CODEX_TEST_HELPER", "1")
	t.Setenv("DRIFT_CODEX_ASSERT_PROMPT", "1")
	c, err := NewWithCommand(os.Args[0], []string{"-test.run=TestCodexHelperProcess"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = c.Stream(ctx, llm.Request{Model: "m", Messages: []llm.Message{{Role: "system", Content: "Drift system instruction"}, {Role: "user", Content: "hello"}}}, func(event llm.StreamEvent) error { got += event.Text; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got != "developer-ok" {
		t.Fatalf("got=%q, system prompt was not separated", got)
	}
}

func TestCodexHelperProcess(t *testing.T) {
	if os.Getenv("DRIFT_CODEX_TEST_HELPER") != "1" {
		return
	}
	s := bufio.NewScanner(os.Stdin)
	assertPrompt := os.Getenv("DRIFT_CODEX_ASSERT_PROMPT") == "1"
	for s.Scan() {
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(s.Bytes(), &req) != nil {
			os.Exit(2)
		}
		var out any
		switch req.Method {
		case "initialize":
			if marker := os.Getenv("DRIFT_CODEX_AUTH_ONCE_FILE"); marker != "" {
				if _, err := os.Stat(marker); os.IsNotExist(err) {
					_ = os.WriteFile(marker, []byte("failed once"), 0o600)
					out = map[string]any{"id": req.ID, "error": map[string]any{"code": 401, "message": "401 unauthorized"}}
					break
				}
			}
			out = map[string]any{"id": req.ID, "result": map[string]any{}}
		case "thread/start":
			if assertPrompt {
				if !strings.Contains(req.Params["developerInstructions"].(string), "Drift system instruction") {
					os.Exit(3)
				}
			}
			out = map[string]any{"id": req.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}}
		case "turn/start":
			out = map[string]any{"id": req.ID, "result": map[string]any{"turn": map[string]any{"id": "turn-1"}}}
			b, _ := json.Marshal(out)
			os.Stdout.Write(append(b, '\n'))
			text := "hello from codex"
			if assertPrompt {
				text = "developer-ok"
			}
			for _, n := range []any{map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"delta": text, "itemId": "i", "threadId": "thread-1", "turnId": "turn-1"}}, map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "status": "completed"}}} {
				b, _ := json.Marshal(n)
				os.Stdout.Write(append(b, '\n'))
			}
		default:
			out = map[string]any{"id": req.ID, "result": map[string]any{}}
		}
		if req.Method != "turn/start" {
			b, _ := json.Marshal(out)
			os.Stdout.Write(append(b, '\n'))
		}
	}
}
