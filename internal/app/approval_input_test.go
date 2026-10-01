package app

import (
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/agent"
	tea "github.com/charmbracelet/bubbletea"
)

func TestApprovalInputModelRendersThreeChoices(t *testing.T) {
	model := newApprovalInputModel(agent.PermissionRequest{Path: "tmp/hello.txt"})
	view := model.View()
	for _, want := range []string{"WriteFile command", "tmp/hello.txt", "This command requires approval", "1. Yes", "2. Yes, and don't ask again for this pattern", "3. No", "❯ 1. Yes"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %q", want, view)
		}
	}
}

func TestApprovalInputModelMovesAndSelects(t *testing.T) {
	model := newApprovalInputModel(agent.PermissionRequest{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(approvalInputModel)
	if model.selected != 1 {
		t.Fatalf("selected after down = %d", model.selected)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(approvalInputModel)
	if !model.submitted || model.choice() != approvePattern {
		t.Fatalf("submitted=%v choice=%v", model.submitted, model.choice())
	}
}

func TestApprovalInputModelEscapeDenies(t *testing.T) {
	model := newApprovalInputModel(agent.PermissionRequest{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = updated.(approvalInputModel)
	if !model.cancelled || model.choice() != deny {
		t.Fatalf("cancelled=%v choice=%v", model.cancelled, model.choice())
	}
}
