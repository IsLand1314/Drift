package app

import (
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func TestResumePickerFiltersAndSelects(t *testing.T) {
	model := newResumePickerModel([]session.Metadata{
		{ID: "conv-other12345678", MessageCount: 1, Preview: "README 项目说明"},
		{ID: "conv-fox12345678", MessageCount: 1, Preview: "FoxCode 项目分析"},
	}, 40, 12)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fox")})
	model = updated.(resumePickerModel)
	if len(model.filtered) != 1 || model.filtered[0].ID != "conv-fox12345678" {
		t.Fatalf("filtered = %+v", model.filtered)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(resumePickerModel)
	if !model.selected || model.selectedID != "conv-fox12345678" {
		t.Fatalf("selection = %+v", model)
	}
}

func TestResumePickerHidesEmptySessions(t *testing.T) {
	model := newResumePickerModel([]session.Metadata{
		{ID: "conv-empty12345678", MessageCount: 0},
		{ID: "conv-recorded12345678", MessageCount: 2, Preview: "已有记录"},
	}, 40, 12)
	if len(model.items) != 1 || model.items[0].ID != "conv-recorded12345678" {
		t.Fatalf("items = %+v", model.items)
	}
	if len(model.filtered) != 1 || model.filtered[0].ID != "conv-recorded12345678" {
		t.Fatalf("filtered = %+v", model.filtered)
	}
}

func TestResumePickerEscapeCancels(t *testing.T) {
	model := newResumePickerModel([]session.Metadata{{ID: "conv-foo12345678", MessageCount: 1, Preview: "foo"}}, 40, 12)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(resumePickerModel)
	if model.selected || !model.cancelled {
		t.Fatalf("cancel state = %+v", model)
	}
}

func TestResumePickerViewHandlesNarrowUnicode(t *testing.T) {
	model := newResumePickerModel([]session.Metadata{{ID: "conv-foo12345678", MessageCount: 1, Preview: "中文标题 👍🏼"}}, 10, 6)
	view := model.View()
	if !strings.Contains(view, "Resume") {
		t.Fatalf("view = %q", view)
	}
	if strings.ToValidUTF8(view, "") != view {
		t.Fatal("view contains invalid UTF-8")
	}
}
