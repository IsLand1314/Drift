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
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

type cancelThenAnswerClient struct {
	signals chan os.Signal
	calls   int
}

type blockingReader struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingReader) Read([]byte) (int, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	<-r.release
	return 0, io.EOF
}

func (c *cancelThenAnswerClient) Stream(ctx context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	c.calls++
	if c.calls == 1 {
		c.signals <- os.Interrupt
		<-ctx.Done()
		return llm.Completion{}, ctx.Err()
	}
	if err := emit(llm.StreamEvent{Text: "continued answer"}); err != nil {
		return llm.Completion{}, err
	}
	return llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "continued answer"}, FinishReason: "stop"}, nil
}

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

func TestChatResumeSendsPriorContext(t *testing.T) {
	root := t.TempDir()
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
			t.Error(err)
		}
		if requests == 2 && (len(request.Messages) < 4 || request.Messages[len(request.Messages)-1].Content != "second") {
			t.Errorf("resumed messages = %#v", request.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		answer := "first answer"
		if requests == 2 {
			answer = "second answer"
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", answer)
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, strings.NewReader("first\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("first code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	items, err := conversation.NewStore(root).List()
	if err != nil || len(items) != 1 {
		t.Fatalf("conversation list=%+v err=%v", items, err)
	}
	out.Reset()
	stderr.Reset()
	if code := RunWithInput(context.Background(), []string{"chat", "--resume", items[0].ID, "-w", root}, getenv, strings.NewReader("second\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("resume code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestChatNoSessionSkipsFullSnapshot(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "--no-session", "-w", root}, getenv, strings.NewReader("hello\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if items, err := conversation.NewStore(root).List(); err != nil || len(items) != 0 {
		t.Fatalf("full snapshots=%+v err=%v", items, err)
	}
	if !strings.Contains(stderr.String(), "--no-session") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestChatRejectsPersistenceFlagConflictsBeforeProvider(t *testing.T) {
	var out, stderr bytes.Buffer
	getenv := func(string) string { return "" }
	if code := RunWithInput(context.Background(), []string{"chat", "--no-session", "--resume", "-w", t.TempDir()}, getenv, strings.NewReader(""), &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "不能同时") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestChatPersistentClearSavesEmptySnapshot(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, strings.NewReader("secret\n/clear\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	items, err := conversation.NewStore(root).List()
	if err != nil || len(items) != 1 || items[0].MessageCount != 0 {
		t.Fatalf("metadata=%+v err=%v", items, err)
	}
}

func TestChatPlainClearSuggestsSlashCommand(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "--no-session", "-w", t.TempDir()}, getenv, strings.NewReader("clear\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if requests != 0 || !strings.Contains(out.String(), "如需清空上下文，请输入 /clear") {
		t.Fatalf("requests=%d out=%q", requests, out.String())
	}
}

func TestChatStatusDoesNotCallProvider(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "--no-session", "-model", "test-model", "-w", t.TempDir()}, getenv, strings.NewReader("/status\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if requests != 0 || !strings.Contains(out.String(), "Drift Status") || !strings.Contains(out.String(), "\n  Session ID:  temporary (not saved)\n") || !strings.Contains(out.String(), "\n  Model:       test-model\n") || !strings.Contains(out.String(), "\n  Context:     100% remaining\n               1.2 KB used / 1048.6 KB total\n") || !strings.Contains(out.String(), "\n  Tokens:      unavailable\n") || !strings.Contains(out.String(), "\n  Tools:       3 enabled\n") || !strings.Contains(out.String(), "\n  Workspace:   ") {
		t.Fatalf("requests=%d out=%q", requests, out.String())
	}
}

func TestChatCancelsCurrentTurnAndContinues(t *testing.T) {
	signals := make(chan os.Signal, 1)
	client := &cancelThenAnswerClient{signals: signals}
	root := t.TempDir()
	runner := agent.NewRunner(client, root, "", tool.NewDefaultRegistry())
	audit, err := session.NewJSONLWriter(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	chatCtx, cancelChat := context.WithCancel(context.Background())
	coordinator := newInterruptCoordinator(signals, cancelChat)
	stop := coordinator.start()
	defer stop()
	var out, stderr bytes.Buffer
	code := runChatLoopWithPersistence(chatCtx, runner, audit, nil, nil, chatStatus{}, coordinator, strings.NewReader("first\nsecond\nexit\n"), &out, &stderr)
	if code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if client.calls != 2 || !strings.Contains(out.String(), "✖ 当前轮已取消；会话仍可继续") || strings.Contains(out.String(), "context canceled") || !strings.Contains(out.String(), "continued answer") {
		t.Fatalf("calls=%d out=%q stderr=%q", client.calls, out.String(), stderr.String())
	}
}

func TestChatInterruptWhileIdleExits130(t *testing.T) {
	signals := make(chan os.Signal, 1)
	chatCtx, cancelChat := context.WithCancel(context.Background())
	coordinator := newInterruptCoordinator(signals, cancelChat)
	stop := coordinator.start()
	defer stop()
	reader := &blockingReader{started: make(chan struct{}), release: make(chan struct{})}
	var out, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runChatLoopWithPersistence(chatCtx, nil, nil, nil, nil, chatStatus{}, coordinator, reader, &out, &stderr)
	}()
	<-reader.started
	signals <- os.Interrupt
	select {
	case <-chatCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("idle interrupt did not cancel chat context")
	}
	close(reader.release)
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("chat did not exit after idle interrupt")
	}
}

func TestChatStatusShowsReportedAndPartialUsage(t *testing.T) {
	runner := agent.NewRunner(nil, t.TempDir(), "", tool.NewDefaultRegistry())
	persistence := &chatPersistence{usage: usageTotals{InputTokens: 17, OutputTokens: 9, ReportedRequests: 1}}
	var out bytes.Buffer
	writeChatStatus(&out, runner, persistence, chatStatus{Model: "test-model", Workspace: t.TempDir(), ToolCount: 3})
	if !strings.Contains(out.String(), "Tokens:      17 in / 9 out") {
		t.Fatalf("status=%q", out.String())
	}
	persistence.usage.UnreportedRequests = 1
	out.Reset()
	writeChatStatus(&out, runner, persistence, chatStatus{Model: "test-model", Workspace: t.TempDir(), ToolCount: 3})
	if !strings.Contains(out.String(), "17 in / 9 out (partial)") {
		t.Fatalf("partial status=%q", out.String())
	}
}

func TestChatPlainStatusSuggestsSlashCommand(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "--no-session", "-w", t.TempDir()}, getenv, strings.NewReader("status\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if requests != 0 || !strings.Contains(out.String(), "如需查看状态，请输入 /status") {
		t.Fatalf("requests=%d out=%q", requests, out.String())
	}
}

func TestChatDisplaysAssistantMarkerAndDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "--no-session", "-w", t.TempDir()}, getenv, strings.NewReader("hello\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "● ok\nDone - ") || strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestChatToolProgressUsesSafeSummary(t *testing.T) {
	var out bytes.Buffer
	call := chatToolCallLine(&out, agent.Event{ToolName: "read_file", Arguments: `{"path":"README.md","secret":"hidden"}`})
	result := chatToolResultLine(&out, agent.Event{ToolName: "read_file", Result: "hello"})
	if call != "> read_file README.md" || result != "+ read_file · 5 B" {
		t.Fatalf("call=%q result=%q", call, result)
	}
	if got := safeToolPath(`{"path":"F:\\secret.txt"}`); got != "" {
		t.Fatalf("absolute path displayed: %q", got)
	}
}

func TestChatCompactUsesNoToolsAndPersistsResult(t *testing.T) {
	root := t.TempDir()
	requests := 0
	var compactHasTools bool
	var thirdRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		_ = json.Unmarshal(body, &request)
		if requests == 2 {
			compactHasTools = len(request.Tools) != 0
		}
		if requests == 3 {
			thirdRequest = string(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		answer := "first"
		if requests == 2 {
			answer = "summary"
		} else if requests == 3 {
			answer = "second"
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", answer)
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, strings.NewReader("first question\n/compact\nsecond question\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if requests != 3 || compactHasTools || !strings.Contains(out.String(), "上下文已压缩") || !strings.Contains(thirdRequest, "summary") {
		t.Fatalf("requests=%d tools=%v out=%q third=%q", requests, compactHasTools, out.String(), thirdRequest)
	}
	items, err := conversation.NewStore(root).List()
	if err != nil || len(items) != 1 || items[0].MessageCount < 2 {
		t.Fatalf("persisted=%+v err=%v", items, err)
	}
}

func TestChatCompactFailureKeepsContext(t *testing.T) {
	root := t.TempDir()
	requests := 0
	var thirdRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 2 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		if requests == 3 {
			body, _ := io.ReadAll(r.Body)
			thirdRequest = string(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "--no-session", "-w", root}, getenv, strings.NewReader("first question\n/compact\nthird question\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if requests != 3 || !strings.Contains(stderr.String(), "压缩失败") || !strings.Contains(thirdRequest, "first question") {
		t.Fatalf("requests=%d stderr=%q third=%q", requests, stderr.String(), thirdRequest)
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
				if key == "DRIFT_PROVIDER" {
					return ""
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

func TestChatClearReportsReset(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test", "OPENAI_BASE_URL": server.URL}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "-w", t.TempDir()}, getenv, strings.NewReader("/clear\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "已清空当前对话上下文") || requests != 0 {
		t.Fatalf("out=%q requests=%d", out.String(), requests)
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
