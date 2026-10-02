package app

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

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
	input := newTuiMainScreenInput(strings.NewReader("hello\n\n"), &strings.Builder{}, "test-model")
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
	model := newChatInputModel("test-model", "────────────────────────")
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
	model := newChatInputModel("test-model", "────────────────────────")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(chatInputModel)
	if !model.cancelled || cmd == nil {
		t.Fatalf("cancel state = %+v, cmd=%v", model, cmd)
	}
}

func TestChatInputModelFooterShowsModel(t *testing.T) {
	model := newChatInputModel("deepseek-v4-flash", "────────────────────────")
	view := model.View()
	if !strings.Contains(view, "Enter 发送 · Ctrl+C 取消") || !strings.Contains(view, "deepseek-v4-flash") {
		t.Fatalf("footer missing from %q", view)
	}
}

func TestChatInputModelSubmittedViewDoesNotLeaveFooter(t *testing.T) {
	model := newChatInputModel("test-model", "separator")
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
