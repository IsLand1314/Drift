package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

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
