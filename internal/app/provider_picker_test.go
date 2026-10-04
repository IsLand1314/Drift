package app

import (
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
