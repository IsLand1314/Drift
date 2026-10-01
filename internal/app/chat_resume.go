package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/conversation"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type resumePickerModel struct {
	items      []conversation.Metadata
	filtered   []conversation.Metadata
	cursor     int
	search     string
	scrollTop  int
	width      int
	height     int
	selected   bool
	cancelled  bool
	selectedID string
}

func newResumePickerModel(items []conversation.Metadata, width, height int) resumePickerModel {
	nonEmpty := make([]conversation.Metadata, 0, len(items))
	for _, item := range items {
		if item.MessageCount > 0 {
			nonEmpty = append(nonEmpty, item)
		}
	}
	m := resumePickerModel{items: nonEmpty, width: width, height: height}
	m.filter()
	return m
}

func runChatResumePicker(ctx context.Context, in io.Reader, out io.Writer, items []conversation.Metadata) (string, bool, error) {
	model := newResumePickerModel(items, 80, 24)
	result, err := tea.NewProgram(&model, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignalHandler(), tea.WithoutSignals()).Run()
	if err != nil {
		return "", false, err
	}
	switch picked := result.(type) {
	case resumePickerModel:
		return picked.selectedID, picked.selected, nil
	case *resumePickerModel:
		return picked.selectedID, picked.selected, nil
	default:
		return "", false, fmt.Errorf("resume picker returned invalid model")
	}
}

func (m resumePickerModel) Init() tea.Cmd { return nil }

func (m resumePickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.clamp()
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyEnter:
		if len(m.filtered) > 0 {
			m.selected = true
			m.selectedID = m.filtered[m.cursor].ID
		}
		return m, tea.Quit
	case tea.KeyEsc, tea.KeyCtrlC:
		m.cancelled = true
		return m, tea.Quit
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor+1 < len(m.filtered) {
			m.cursor++
		}
	case tea.KeyBackspace:
		if m.search != "" {
			runes := []rune(m.search)
			m.search = string(runes[:len(runes)-1])
			m.filter()
		}
	case tea.KeyRunes:
		m.search += string(key.Runes)
		m.filter()
	}
	m.clamp()
	return m, nil
}

func (m resumePickerModel) View() string {
	width := maxResumeInt(m.width, 1)
	height := maxResumeInt(m.height, 1)
	var lines []string
	current := 0
	if len(m.filtered) > 0 {
		current = m.cursor + 1
	}
	lines = append(lines, truncateResume(fmt.Sprintf("Resume session (%d of %d)", current, len(m.filtered)), width))
	search := "⌕ " + m.search
	if m.search == "" {
		search = "⌕ Search…"
	}
	lines = append(lines, truncateResume(search, width))
	lines = append(lines, "")
	visible := maxResumeInt(height-5, 1)
	for i := m.scrollTop; i < len(m.filtered) && i < m.scrollTop+visible; i++ {
		item := m.filtered[i]
		title := item.Title
		if title == "" {
			title = item.Preview
		}
		if title == "" {
			title = item.ID
		}
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		if i == m.cursor {
			prefix = "❯ "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
		}
		lines = append(lines, prefix+style.Render(truncateResume(title, maxResumeInt(width-lipgloss.Width(prefix), 1))))
		meta := fmt.Sprintf("    %s · %d messages · %.1f KB", resumeRelativeTime(item.UpdatedAt), item.MessageCount, float64(item.ContextBytes)/1000)
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Render(truncateResume(meta, width)))
	}
	lines = append(lines, "", truncateResume("Type to search · Enter to select · Esc to cancel", width))
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (m *resumePickerModel) filter() {
	query := strings.ToLower(strings.TrimSpace(m.search))
	m.filtered = m.filtered[:0]
	for _, item := range m.items {
		if query == "" || strings.Contains(strings.ToLower(item.ID+" "+item.Title+" "+item.Preview), query) {
			m.filtered = append(m.filtered, item)
		}
	}
	m.cursor, m.scrollTop = 0, 0
}

func (m *resumePickerModel) clamp() {
	if len(m.filtered) == 0 {
		m.cursor, m.scrollTop = 0, 0
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	visible := maxResumeInt(m.height-5, 1)
	if m.cursor < m.scrollTop {
		m.scrollTop = m.cursor
	}
	if m.cursor >= m.scrollTop+visible {
		m.scrollTop = m.cursor - visible + 1
	}
}

func resumeRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "unknown time"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

func truncateResume(value string, width int) string {
	value = strings.ToValidUTF8(value, "")
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return string([]rune(value)[:minResumeInt(width, len([]rune(value)))])
	}
	runes := []rune(value)
	return string(runes[:minResumeInt(len(runes), width-3)]) + "..."
}

func maxResumeInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minResumeInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
