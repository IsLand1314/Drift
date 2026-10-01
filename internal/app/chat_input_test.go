package app

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChatInputScannerReadsLines(t *testing.T) {
	input := newChatInput(strings.NewReader("hello\n\n"), &strings.Builder{})
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
	model := newChatInputModel()
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
	model := newChatInputModel()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(chatInputModel)
	if !model.cancelled || cmd == nil {
		t.Fatalf("cancel state = %+v, cmd=%v", model, cmd)
	}
}
