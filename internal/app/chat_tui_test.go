package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/IsLand1314/Drift/internal/agent"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestTTYChatViewKeepsTranscriptAndSingleFooter(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Model: "test-model"}, nil)
	m.width, m.height = 80, 20
	m.lines = []string{"❯ first request", "● first answer", "Done - 0.1s", "❯ second request", "● second answer", "Done - 0.2s"}

	view := m.View()
	if strings.Count(view, "first request") != 1 || strings.Count(view, "second request") != 1 {
		t.Fatalf("transcript was not preserved: %q", view)
	}
	if strings.Count(view, "权限：default") != 1 {
		t.Fatalf("expected one persistent footer: %q", view)
	}
	if strings.Count(view, "test-model") != 1 {
		t.Fatalf("expected one model label: %q", view)
	}
}

func TestTTYChatToolResultDoesNotRenderProbeFailure(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{}, nil)
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ToolName: "ReadFile", Path: "missing.txt", ErrorSummary: "tool execution failed"})
	if len(m.lines) != 0 {
		t.Fatalf("probe failure should stay out of transcript: %#v", m.lines)
	}
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ToolName: "ReadFile", Path: "ok.txt", Result: "hello", AfterBytes: 5})
	if len(m.lines) != 1 || !strings.Contains(m.lines[0], "ok.txt") {
		t.Fatalf("successful tool result missing: %#v", m.lines)
	}
}

func TestTTYChatToolProgressReplacesStartLine(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{}, nil)
	m.applyEvent(agent.Event{Type: agent.EventToolCall, ToolCallID: "call-1", ToolName: "ReadFile", Arguments: `{"path":"README.md"}`})
	if len(m.lines) != 1 || !strings.Contains(m.lines[0], "● Read README.md ...") {
		t.Fatalf("tool start line missing: %#v", m.lines)
	}
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ToolCallID: "call-1", ToolName: "ReadFile", Path: "README.md", Result: "hello"})
	if len(m.lines) != 1 || !strings.Contains(m.lines[0], "✓ Read README.md") || strings.Contains(m.lines[0], "●") {
		t.Fatalf("tool result did not replace start line: %#v", m.lines)
	}
}

func TestTTYChatTextEventsSurviveBubbleTeaModelCopies(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{}, nil)
	model, _ := m.Update(tuiAgentEvent{event: agent.Event{Type: agent.EventTextDelta, Text: "first"}})
	model, _ = model.Update(tuiAgentEvent{event: agent.Event{Type: agent.EventTextDelta, Text: " second"}})
	modelValue := model.(*ttyChatModel)
	got := modelValue.stream
	if got != "first second" {
		t.Fatalf("stream = %q, want %q", got, "first second")
	}
}

func TestTTYChatLayoutUsesFullWidthRulesAndWrappedInput(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Model: "deepseek-v4-flash"}, nil)
	m.width, m.height = 24, 12
	m.textarea.SetWidth(22)
	m.textarea.SetValue(strings.Repeat("x", 30))
	resizeTTYTextarea(&m)
	if m.textarea.Height() < 2 {
		t.Fatalf("wrapped input height = %d, want at least 2", m.textarea.Height())
	}
	m.lines = []string{"❯ /resume"}
	view := m.View()
	if !strings.Contains(view, strings.Repeat("─", 24)) {
		t.Fatalf("separator did not span terminal width: %q", view)
	}
	if !strings.Contains(view, "/resume") {
		t.Fatalf("submitted command missing from transcript: %q", view)
	}
}

func TestTTYChatLayoutKeepsLongModelName(t *testing.T) {
	name := "provider-with-a-very-long-model-name"
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Model: name}, nil)
	m.width, m.height = 24, 12
	view := m.View()
	if !strings.Contains(view, name) {
		t.Fatalf("model name was truncated: %q", view)
	}
}

func TestTTYFooterLeavesRightMarginForModelName(t *testing.T) {
	footer := tuiFooter("deepseek-v4-flash", 40)
	lines := strings.Split(footer, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(footer, "deepseek-v4-flash") {
		t.Fatalf("model name missing: %q", footer)
	}
	if lipgloss.Width(last) >= 40 {
		t.Fatalf("footer touches terminal edge: %q", footer)
	}
}

func TestStyleTranscriptPreservesUnicodeAfterMarker(t *testing.T) {
	view := styleTranscript("❯ 你好\n● Drift 回复")
	if !utf8.ValidString(view) || strings.Contains(view, "\uFFFD") {
		t.Fatalf("unicode marker was split: %q", view)
	}
	if !strings.Contains(view, "你好") || !strings.Contains(view, "Drift 回复") {
		t.Fatalf("transcript text was lost: %q", view)
	}
}

func TestTTYChatViewKeepsFooterAfterLongAnswer(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Model: "deepseek-v4-flash"}, nil)
	m.width, m.height = 80, 24
	m.lines = []string{"❯ 你好，请介绍一下你自己", "● " + strings.Repeat("这是助手的回答。", 80), "Done - 3.4s"}
	view := m.View()
	if !strings.Contains(view, "权限：default") || !strings.Contains(view, "deepseek-v4-flash") {
		t.Fatalf("footer disappeared after long answer: %q", view)
	}
}

func TestTTYChatViewportScrollsAndKeepsManualPosition(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Model: "model"}, nil)
	m.width, m.height = 40, 10
	for i := 0; i < 40; i++ {
		m.lines = append(m.lines, fmt.Sprintf("line %d", i))
	}
	m.View()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	scrolled := model.(*ttyChatModel)
	if scrolled.viewport.YOffset == 0 {
		t.Fatalf("page up did not move viewport")
	}
	scrolled.View()
	if scrolled.viewport.YOffset == 0 {
		t.Fatalf("view reset manually scrolled viewport")
	}
}

func TestTTYChatCancellationUsesSafeMessage(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{}, nil)
	m.started = time.Now()
	m.finishTurn(context.Canceled)
	joined := strings.Join(m.lines, "\n")
	if !strings.Contains(joined, "当前轮已取消；会话仍可继续") {
		t.Fatalf("safe cancellation message missing: %q", joined)
	}
	if strings.Contains(joined, "context canceled") {
		t.Fatalf("raw cancellation error leaked: %q", joined)
	}
}

func TestApprovalDecisionDefersPersistentPolicyUntilSuccess(t *testing.T) {
	memory := newPermissionMemory()
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
	decision := approvalDecision(1, memory, request)
	if decision.Reason != "persistent_pattern_pending" {
		t.Fatalf("reason=%q, want persistent_pattern_pending", decision.Reason)
	}
	if memory.Allow(request) {
		t.Fatal("remembered approval was added before the operation succeeded")
	}
}

func TestTTYApprovalPersistsOnlyAfterSuccessfulToolResult(t *testing.T) {
	root := t.TempDir()
	request := agent.PermissionRequest{ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt"}
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Workspace: root}, nil)
	m.pendingPermissions[permissionRuleKey(permissionRuleFromRequest(request))] = request
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt", Result: "ok"})
	loaded, err := loadPermissionPolicy(root)
	if err != nil || !loaded.allows(request) {
		t.Fatalf("successful operation did not persist policy: err=%v", err)
	}

	failedRoot := t.TempDir()
	failed := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Workspace: failedRoot}, nil)
	failed.pendingPermissions[permissionRuleKey(permissionRuleFromRequest(request))] = request
	failed.applyEvent(agent.Event{Type: agent.EventToolResult, ToolName: "WriteFile", Operation: "create_file", Path: "tmp/hello.txt", ErrorSummary: "failed"})
	failedLoaded, err := loadPermissionPolicy(failedRoot)
	if err != nil || failedLoaded.allows(request) {
		t.Fatalf("failed operation unexpectedly persisted policy: err=%v", err)
	}
}

func TestTTYPolicyLoadFailureFallsBackToAsk(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".drift"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".drift", "permissions.json"), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{Workspace: root}, nil)
	if !strings.Contains(m.View(), "权限策略加载失败") {
		t.Fatal("TTY did not show safe policy-load warning")
	}
}

func TestTTYWrapTextUsesTerminalWidth(t *testing.T) {
	wrapped := wrapTUIText("你好世界abcdefgh", 8)
	if strings.Count(wrapped, "\n") < 1 {
		t.Fatalf("long transcript line was not wrapped: %q", wrapped)
	}
	for _, line := range strings.Split(wrapped, "\n") {
		if lipgloss.Width(line) > 8 {
			t.Fatalf("wrapped line exceeds width: %q", line)
		}
	}
}

func TestPermissionPickerListsModesAndDescriptions(t *testing.T) {
	picker := newPermissionPicker(permissionModeAcceptEdits)
	view := renderTUIPermissionPicker(*picker)
	for _, want := range []string{"default", "acceptEdits", "plan", "bypassPermissions", "自动允许写入和编辑"} {
		if !strings.Contains(view, want) {
			t.Fatalf("permission picker missing %q in %q", want, view)
		}
	}
}

func TestPermissionPickerChangesModeWithoutTranscriptCommand(t *testing.T) {
	m := newTTYChatModel(context.Background(), nil, nil, nil, nil, chatStatus{}, nil)
	if !m.handleCommand("/permissions") || m.permissionPicker == nil {
		t.Fatal("/permissions did not open picker")
	}
	model, _ := m.handlePermissionPicker(tea.KeyMsg{Type: tea.KeyDown})
	model, _ = model.(*ttyChatModel).handlePermissionPicker(tea.KeyMsg{Type: tea.KeyEnter})
	got := model.(*ttyChatModel)
	if *got.permissionMode != permissionModeAcceptEdits {
		t.Fatalf("selected mode = %q, want %q", *got.permissionMode, permissionModeAcceptEdits)
	}
	if len(got.lines) != 1 || !strings.Contains(got.lines[0], "权限模式已切换为 acceptEdits") {
		t.Fatalf("unexpected transcript: %#v", got.lines)
	}
}
