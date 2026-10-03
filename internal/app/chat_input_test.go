package app

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/tool"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTTYChatFilesIdentifiesTerminalPair(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readPipe.Close()
	defer writePipe.Close()
	if _, _, ok := tuiMainScreenFiles(readPipe, writePipe); ok {
		t.Fatal("pipe pair must not be treated as an interactive TTY")
	}
	if _, _, ok := tuiMainScreenFiles(strings.NewReader(""), &strings.Builder{}); ok {
		t.Fatal("buffered pair must not be treated as an interactive TTY")
	}
}

func TestChatInputScannerReadsLines(t *testing.T) {
	input := newTuiMainScreenInput(strings.NewReader("hello\n\n"), &strings.Builder{}, "test-model", permissionModeDefault)
	if _, ok := input.(*scannerChatInput); !ok {
		t.Fatalf("expected scanner input, got %T", input)
	}
	got, err := input.Read(context.Background())
	if err != nil || got != "hello" {
		t.Fatalf("first read = %q, %v", got, err)
	}
	got, err = input.Read(context.Background())
	if err != nil || got != "" {
		t.Fatalf("empty read = %q, %v", got, err)
	}
	if _, err := input.Read(context.Background()); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestChatInputModelPlaceholderAndSubmit(t *testing.T) {
	model := newChatInputModel("test-model", "────────────────────────", permissionModeDefault)
	if !strings.Contains(model.View(), "Send a message...") {
		t.Fatalf("placeholder missing from %q", model.View())
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	model = updated.(chatInputModel)
	if strings.Contains(model.View(), "Send a message...") || !strings.Contains(model.View(), "hello") {
		t.Fatalf("typed view = %q", model.View())
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(chatInputModel)
	if !model.submitted || cmd == nil {
		t.Fatalf("submit state = %+v, cmd=%v", model, cmd)
	}
}

func TestChatInputModelCtrlC(t *testing.T) {
	model := newChatInputModel("test-model", "────────────────────────", permissionModeDefault)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(chatInputModel)
	if !model.cancelled || cmd == nil {
		t.Fatalf("cancel state = %+v, cmd=%v", model, cmd)
	}
}

func TestChatInputModelTreatsMultilineBracketedPasteAsText(t *testing.T) {
	model := newChatInputModel("test-model", "separator", permissionModeDefault)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one\nline two"), Paste: true})
	model = updated.(chatInputModel)
	if model.submitted {
		t.Fatalf("multiline paste submitted input: %+v", model)
	}
	if got := model.editor.Value(); got != "line one\nline two" {
		t.Fatalf("pasted value = %q", got)
	}
}

func TestChatInputModelFooterShowsModel(t *testing.T) {
	model := newChatInputModel("deepseek-v4-flash", "────────────────────────", permissionModeAcceptEdits)
	view := model.View()
	if !strings.Contains(view, "权限：acceptEdits") || !strings.Contains(view, "deepseek-v4-flash") {
		t.Fatalf("footer missing from %q", view)
	}
}

func TestChatInputModelSubmittedViewDoesNotLeaveFooter(t *testing.T) {
	model := newChatInputModel("test-model", "separator", permissionModeDefault)
	model.editor.SetValue("create file")
	model.submitted = true
	view := model.View()
	if strings.Contains(view, "Enter 发送") || strings.Contains(view, "test-model") || strings.Contains(view, "separator") {
		t.Fatalf("submitted input left stale footer: %q", view)
	}
	if !strings.Contains(view, "create file") {
		t.Fatalf("submitted input lost prompt: %q", view)
	}
}

func TestPermissionModeInputModelSelectsMode(t *testing.T) {
	model := newPermissionModeInputModel(permissionModeDefault)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(permissionModeInputModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(permissionModeInputModel)
	if !model.submitted || cmd == nil || model.mode() != permissionModeAcceptEdits {
		t.Fatalf("submitted=%v mode=%q cmd=%v", model.submitted, model.mode(), cmd)
	}
}

func TestParseQuestionAnswerAcceptsChoicesFreeTextAndCancellation(t *testing.T) {
	question := tool.Question{Options: []tool.QuestionOption{{ID: "fast", Label: "Fast"}, {ID: "safe", Label: "Safe"}}, AllowFreeText: true}
	answer, err := parseQuestionAnswer(question, "2")
	if err != nil || len(answer.Selected) != 1 || answer.Selected[0] != "safe" {
		t.Fatalf("numbered choice = %+v, %v", answer, err)
	}
	answer, err = parseQuestionAnswer(question, "explain more")
	if err != nil || answer.Text != "explain more" {
		t.Fatalf("free text = %+v, %v", answer, err)
	}
	answer, err = parseQuestionAnswer(question, "/cancel")
	if err != nil || !answer.Cancelled {
		t.Fatalf("cancel = %+v, %v", answer, err)
	}
}
