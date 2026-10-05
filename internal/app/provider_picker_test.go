package app

import (
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func TestProviderPickerMovesAndSelectsProfile(t *testing.T) {
	m := providerPickerModel{providers: []config.Provider{{Name: "alpha", Model: "a"}, {Name: "beta", Model: "b"}}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(providerPickerModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(providerPickerModel)
	if m.selected != "beta" || m.cancelled {
		t.Fatalf("picker selection=%q cancelled=%v", m.selected, m.cancelled)
	}
}

func TestProviderPickerShowsDriftBrandAndWorkspace(t *testing.T) {
	m := providerPickerModel{providers: []config.Provider{{Name: "alpha", Model: "a"}}, workspace: "F:/work/project"}
	view := m.View()
	if !strings.Contains(view, "DRIFT") || !strings.Contains(view, "F:/work/project") {
		t.Fatalf("picker view missing branding: %q", view)
	}
}

func TestStartupPickerCleanupClearsTemporaryFrame(t *testing.T) {
	if got := startupPickerCleanupSequence(); got != "\x1b[2J\x1b[H" {
		t.Fatalf("cleanup=%q", got)
	}
}
