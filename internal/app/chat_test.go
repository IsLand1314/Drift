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
	"github.com/IsLand1314/Drift/internal/audit"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/memory"
	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

type cancelThenAnswerClient struct {
	signals chan os.Signal
	calls   int
}

type answerClient struct{}

type compactThenAnswerClient struct{ calls int }

func collectLegacyEvents(ctx context.Context, client interface {
	Stream(context.Context, llm.Request, func(llm.StreamEvent) error) (testCompletion, error)
}, request llm.Request, emit func(llm.Event) error) error {
	completion, err := client.Stream(ctx, request, func(event llm.StreamEvent) error {
		if event.Text != "" {
			if err := emit(llm.TextDelta{Text: event.Text}); err != nil {
				return err
			}
		}
		if event.ReasoningContent != "" {
			if err := emit(llm.ThinkingDelta{Text: event.ReasoningContent}); err != nil {
				return err
			}
		}
		if event.ToolCallDelta != nil {
			if err := emit(*event.ToolCallDelta); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for index, call := range completion.Assistant.ToolCalls {
		if err := emit(llm.ToolCallComplete{Index: index, ID: call.ID, Name: call.Name, Arguments: call.Arguments}); err != nil {
			return err
		}
	}
	return emit(llm.StreamEnd{FinishReason: completion.FinishReason, Usage: completion.Usage})
}

func (c *compactThenAnswerClient) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return collectLegacyEvents(ctx, c, request, emit)
}

func (c *compactThenAnswerClient) Stream(_ context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (testCompletion, error) {
	c.calls++
	text := "summary"
	if c.calls > 1 {
		text = "answer after compaction"
	}
	if err := emit(llm.StreamEvent{Text: text}); err != nil {
		return testCompletion{}, err
	}
	return testCompletion{Assistant: llm.Message{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
}

func (answerClient) Stream(_ context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (testCompletion, error) {
	if err := emit(llm.StreamEvent{Text: "main screen answer"}); err != nil {
		return testCompletion{}, err
	}
	return testCompletion{Assistant: llm.Message{Role: "assistant", Content: "main screen answer"}, FinishReason: "stop"}, nil
}

func (c answerClient) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return collectLegacyEvents(ctx, c, request, emit)
}

type blockingReader struct {
	started chan struct{}
	release chan struct{}
}

func TestMemoryCommandsManageShortAndLongTermMemory(t *testing.T) {
	root := t.TempDir()
	repo, err := memory.NewRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.NewRunner(nil, root, "", tool.NewDefaultRegistry())
	persistence := &chatPersistence{memoryRepo: repo}
	var out bytes.Buffer
	if !handleMemoryCommand("/memory remember fact viewport stays stable", runner, persistence, &out) {
		t.Fatal("command not handled")
	}
	if len(runner.ShortTermMemory().Items()) != 1 {
		t.Fatalf("short memory=%v", runner.ShortTermMemory().Items())
	}
	if err := repo.Add(memory.Item{Kind: memory.KindExperience, Text: "viewport stable", Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	handleMemoryCommand("/memory search viewport", runner, persistence, &out)
	if !strings.Contains(out.String(), "viewport stable") {
		t.Fatalf("search output=%q", out.String())
	}
	out.Reset()
	handleMemoryCommand("/memory clear", runner, persistence, &out)
	if len(runner.ShortTermMemory().Items()) != 0 {
		t.Fatal("short memory not cleared")
	}
}

func TestAutomaticMemoryCandidatesArePersistedAsUnverified(t *testing.T) {
	repo, err := memory.NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved := saveAutomaticMemoryCandidates(repo, "以后：默认使用中文回答")
	if len(saved) != 1 || saved[0].Status != "candidate" {
		t.Fatalf("saved=%+v", saved)
	}
	visible, err := repo.Search("中文", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 0 {
		t.Fatalf("candidate leaked into default search: %+v", visible)
	}
	review, err := repo.SearchWithOptions("中文", memory.SearchOptions{Status: "candidate", Limit: 20})
	if err != nil || len(review) != 1 {
		t.Fatalf("review=%+v err=%v", review, err)
	}
	var out bytes.Buffer
	if !handleMemoryCommand("/memory candidates", agent.NewRunner(nil, t.TempDir(), "", tool.NewDefaultRegistry()), &chatPersistence{memoryRepo: repo}, &out) {
		t.Fatal("candidates command not handled")
	}
	if !strings.Contains(out.String(), "默认使用中文回答") {
		t.Fatalf("candidate output=%q", out.String())
	}
	if !handleMemoryCommand("/memory approve 默认使用中文回答", agent.NewRunner(nil, t.TempDir(), "", tool.NewDefaultRegistry()), &chatPersistence{memoryRepo: repo}, &out) {
		t.Fatal("approve command not handled")
	}
	if hits, err := repo.Search("中文", 20); err != nil || len(hits) != 1 || hits[0].Status != "verified" {
		t.Fatalf("approved hits=%+v err=%v", hits, err)
	}
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

func (c *cancelThenAnswerClient) Stream(ctx context.Context, _ llm.Request, emit func(llm.StreamEvent) error) (testCompletion, error) {
	c.calls++
	if c.calls == 1 {
		c.signals <- os.Interrupt
		<-ctx.Done()
		return testCompletion{}, ctx.Err()
	}
	if err := emit(llm.StreamEvent{Text: "continued answer"}); err != nil {
		return testCompletion{}, err
	}
	return testCompletion{Assistant: llm.Message{Role: "assistant", Content: "continued answer"}, FinishReason: "stop"}, nil
}

func (c *cancelThenAnswerClient) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return collectLegacyEvents(ctx, c, request, emit)
}

func TestTuiMainScreenAppendsCompletedTurnsWithoutScreenControl(t *testing.T) {
	root := t.TempDir()
	audit, err := audit.NewJSONLWriter(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()

	runner := agent.NewRunner(answerClient{}, root, "", tool.NewDefaultRegistry())
	var out, stderr bytes.Buffer
	code := runTuiMainScreenLoop(context.Background(), runner, audit, nil, nil, chatStatus{}, nil, strings.NewReader("question\nexit\n"), &out, &stderr)
	if code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	got := out.String()
	for _, sequence := range []string{"\x1b[?1049", "\x1b[1;", "\x1b[r"} {
		if strings.Contains(got, sequence) {
			t.Fatalf("main-screen output contains terminal screen control %q: %q", sequence, got)
		}
	}
	for _, want := range []string{"● main screen answer", "Done -"} {
		if strings.Count(got, want) != 1 {
			t.Fatalf("output count for %q = %d, output=%q", want, strings.Count(got, want), got)
		}
	}
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
	files, err := audit.ListFiles(filepath.Join(root, ".drift", "audits"))
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

func TestSwitchChatSessionStagesTargetBeforeReplacingCurrent(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	current, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	current.Messages = []llm.Message{{Role: "user", Content: "current"}}
	if err := store.Save(current); err != nil {
		t.Fatal(err)
	}
	target, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	target.Messages = []llm.Message{{Role: "user", Content: "target"}}
	target.InputTokens, target.OutputTokens, target.ReportedRequests = 12, 4, 1
	if err := store.Save(target); err != nil {
		t.Fatal(err)
	}
	runner := agent.NewRunner(nil, root, "", tool.NewDefaultRegistry())
	runner.RestoreMessages(current.Messages)
	persistence := &chatPersistence{store: store, snapshot: current, persistent: true}
	if err := switchChatSession(runner, persistence, target.ID); err != nil {
		t.Fatal(err)
	}
	if persistence.snapshot.ID != target.ID || runner.Messages()[0].Content != "target" || persistence.usage.InputTokens != 12 {
		t.Fatalf("snapshot=%+v messages=%+v usage=%+v", persistence.snapshot, runner.Messages(), persistence.usage)
	}
	if err := switchChatSession(runner, persistence, "conv-missing12345678"); err == nil {
		t.Fatal("missing session unexpectedly switched")
	}
	if persistence.snapshot.ID != target.ID || runner.Messages()[0].Content != "target" {
		t.Fatal("failed switch changed current session")
	}
}

func TestChatResumeCommandSwitchesPersistentSession(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	current, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	current.Messages = []llm.Message{{Role: "user", Content: "current"}}
	if err := store.Save(current); err != nil {
		t.Fatal(err)
	}
	target, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	target.Messages = []llm.Message{{Role: "user", Content: "target"}}
	if err := store.Save(target); err != nil {
		t.Fatal(err)
	}
	audit, err := audit.NewJSONLWriter(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	runner := agent.NewRunner(nil, root, "", tool.NewDefaultRegistry())
	runner.RestoreMessages(current.Messages)
	persistence := &chatPersistence{store: store, snapshot: current, persistent: true}
	var out, stderr bytes.Buffer
	code := runChatLoopWithPersistence(context.Background(), runner, audit, nil, persistence, chatStatus{}, nil, strings.NewReader("/resume "+target.ID+"\n/status\nexit\n"), &out, &stderr)
	if code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if persistence.snapshot.ID != target.ID || !strings.Contains(out.String(), target.ID) || runner.Messages()[0].Content != "target" {
		t.Fatalf("out=%q snapshot=%+v messages=%+v", out.String(), persistence.snapshot, runner.Messages())
	}
}

func TestStartNewChatSessionKeepsOldSnapshot(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	current, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	current.Messages = []llm.Message{{Role: "user", Content: "keep me"}}
	if err := store.Save(current); err != nil {
		t.Fatal(err)
	}
	runner := agent.NewRunner(nil, root, "", tool.NewDefaultRegistry())
	runner.RestoreMessages(current.Messages)
	persistence := &chatPersistence{store: store, snapshot: current, persistent: true, usage: usageTotals{InputTokens: 9}}
	if err := startNewChatSession(runner, persistence); err != nil {
		t.Fatal(err)
	}
	if persistence.snapshot.ID == current.ID || len(runner.Messages()) != 0 || persistence.usage != (usageTotals{}) {
		t.Fatalf("snapshot=%+v messages=%+v usage=%+v", persistence.snapshot, runner.Messages(), persistence.usage)
	}
	if loaded, err := store.Load(current.ID); err != nil || len(loaded.Messages) != 1 {
		t.Fatalf("old snapshot changed: %+v %v", loaded, err)
	}
}

func TestChatPersistenceRestoresTaskStateWithSession(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	if _, err := search.Execute(context.Background(), root, `{"query":"task","load":["TaskCreate"]}`); err != nil {
		t.Fatal(err)
	}
	create, _ := registry.Lookup("TaskCreate")
	if _, err := create.Execute(context.Background(), root, `{"subject":"persist through app"}`); err != nil {
		t.Fatal(err)
	}
	runner := agent.NewRunner(nil, root, "", registry)
	persistence := &chatPersistence{store: store, snapshot: snapshot, persistent: true, registry: registry}
	if err := persistence.saveRunner(runner); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil || len(loaded.Tasks) != 1 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}

	otherRegistry := tool.NewChatRegistry()
	otherPersistence := &chatPersistence{store: store, snapshot: snapshot, persistent: true, registry: otherRegistry}
	otherRunner := agent.NewRunner(nil, root, "", otherRegistry)
	if err := switchChatSession(otherRunner, otherPersistence, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if got := otherRegistry.(tool.TaskRegistry).ExportTasks(); len(got) != 1 || got[0].Subject != "persist through app" {
		t.Fatalf("restored tasks=%+v", got)
	}
	if err := startNewChatSession(otherRunner, otherPersistence); err != nil {
		t.Fatal(err)
	}
	if got := otherRegistry.(tool.TaskRegistry).ExportTasks(); len(got) != 0 {
		t.Fatalf("new session retained tasks=%+v", got)
	}
}

func TestChatNewCommandCreatesPersistentSession(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	current, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	audit, err := audit.NewJSONLWriter(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	runner := agent.NewRunner(nil, root, "", tool.NewDefaultRegistry())
	persistence := &chatPersistence{store: store, snapshot: current, persistent: true}
	var out, stderr bytes.Buffer
	code := runChatLoopWithPersistence(context.Background(), runner, audit, nil, persistence, chatStatus{}, nil, strings.NewReader("/new\n/status\nexit\n"), &out, &stderr)
	if code != 0 || persistence.snapshot.ID == current.ID || !strings.Contains(out.String(), "已创建新会话") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestChatRenameCommandRenamesCurrentSession(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	current, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	audit, err := audit.NewJSONLWriter(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	runner := agent.NewRunner(nil, root, "", tool.NewDefaultRegistry())
	persistence := &chatPersistence{store: store, snapshot: current, persistent: true}
	var out, stderr bytes.Buffer
	code := runChatLoopWithPersistence(context.Background(), runner, audit, nil, persistence, chatStatus{}, nil, strings.NewReader("/rename 基本认识\nexit\n"), &out, &stderr)
	if code != 0 || persistence.snapshot.Title != "基本认识" || !strings.Contains(out.String(), "已更新会话名称") {
		t.Fatalf("code=%d out=%q snapshot=%+v", code, out.String(), persistence.snapshot)
	}
	loaded, err := store.Load(current.ID)
	if err != nil || loaded.Title != "基本认识" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
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
	items, err := session.NewStore(root).List()
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
	if items, err := session.NewStore(root).List(); err != nil || len(items) != 0 {
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
	items, err := session.NewStore(root).List()
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
	if requests != 0 || !strings.Contains(out.String(), "Drift Status") || !strings.Contains(out.String(), "\n  Session ID:  temporary (not saved)\n") || !strings.Contains(out.String(), "\n  Model:       test-model\n") || !strings.Contains(out.String(), "\n  Context:     100% remaining\n") || !strings.Contains(out.String(), "KB used / 1048.6 KB total\n") || !strings.Contains(out.String(), "\n  Tokens:      unavailable\n") || !strings.Contains(out.String(), "\n  Tools:       2 enabled\n") || !strings.Contains(out.String(), "\n  Workspace:   ") {
		t.Fatalf("requests=%d out=%q", requests, out.String())
	}
}

func TestChatCancelsCurrentTurnAndContinues(t *testing.T) {
	signals := make(chan os.Signal, 1)
	client := &cancelThenAnswerClient{signals: signals}
	root := t.TempDir()
	runner := agent.NewRunner(client, root, "", tool.NewDefaultRegistry())
	audit, err := audit.NewJSONLWriter(filepath.Join(root, "audit.jsonl"))
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

func TestChatToolResultUsesSafeSummary(t *testing.T) {
	var out bytes.Buffer
	result := chatToolResultLine(&out, agent.Event{ToolName: "ReadFile", Result: "hello"}, "README.md", 200*time.Millisecond)
	if result != "✓ Read README.md · 5 B · 0.2s" {
		t.Fatalf("result=%q", result)
	}
	if got := safeToolPath(`{"path":"F:\\secret.txt"}`); got != "" {
		t.Fatalf("absolute path displayed: %q", got)
	}
}

func TestChatUserPromptLinePreservesSubmittedText(t *testing.T) {
	var out bytes.Buffer
	got := chatUserPromptLine(&out, "请运行命令 echo auto-manual-check；不要修改任何文件。")
	if (!strings.Contains(got, "❯") && !strings.Contains(got, ">")) || !strings.Contains(got, "echo auto-manual-check") {
		t.Fatalf("prompt line=%q", got)
	}
}

func TestChatSearchHistoryPrintsOnlyLocations(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{{Role: "user", Content: "deployment question"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		return map[string]string{"OPENAI_API_KEY": "test-secret", "OPENAI_MODEL": "test-model", "OPENAI_BASE_URL": "http://127.0.0.1:1/v1"}[key]
	}
	var out, stderr bytes.Buffer
	if code := RunWithInput(context.Background(), []string{"chat", "-w", root}, getenv, strings.NewReader("/search deployment\nexit\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), snapshot.ID) || !strings.Contains(out.String(), "matches=user#1") || strings.Contains(out.String(), "deployment question") {
		t.Fatalf("search output=%q", out.String())
	}
}

func TestChatAutomaticallyCompactsBeforeNearLimitTurn(t *testing.T) {
	root := t.TempDir()
	audit, err := audit.NewJSONLWriter(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	client := &compactThenAnswerClient{}
	messages := make([]llm.Message, 0, 8)
	for index := 0; index < 8; index++ {
		messages = append(messages, llm.Message{Role: "user", Content: strings.Repeat("x", agent.CompactionTriggerBytes/8)})
	}
	runner := agent.NewRunnerWithMessages(client, root, "", tool.NewDefaultRegistry(), messages)
	var out, stderr bytes.Buffer
	code := runTuiMainScreenLoop(context.Background(), runner, audit, nil, nil, chatStatus{}, nil, strings.NewReader("next question\nexit\n"), &out, &stderr)
	if code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if client.calls != 2 || !strings.Contains(out.String(), "上下文已自动压缩") || !strings.Contains(out.String(), "answer after compaction") {
		t.Fatalf("calls=%d out=%q stderr=%q", client.calls, out.String(), stderr.String())
	}
}

func TestChatSuppressesExpectedReadMissFromTranscript(t *testing.T) {
	if shouldRenderToolResult(agent.Event{ToolName: "ReadFile", ErrorSummary: "tool execution failed"}) {
		t.Fatal("read-before-create miss should not become a red transcript error")
	}
	if !shouldRenderToolResult(agent.Event{ToolName: "WriteFile", ErrorSummary: "tool execution failed"}) {
		t.Fatal("write failure must remain visible")
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
	items, err := session.NewStore(root).List()
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
	files, err := audit.ListFiles(filepath.Join(root, ".drift", "audits"))
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
