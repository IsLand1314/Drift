package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/IsLand1314/Drift/internal/config"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type providerPickerModel struct {
	providers []config.Provider
	cursor    int
	selected  string
	cancelled bool
	width     int
}

func runProviderPicker(ctx context.Context, in io.Reader, out io.Writer, providers []config.Provider, current string) (string, bool, error) {
	m := providerPickerModel{providers: providers, width: 80}
	for i, p := range providers {
		if p.Name == current {
			m.cursor = i
			break
		}
	}
	result, err := tea.NewProgram(&m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignalHandler(), tea.WithoutSignals()).Run()
	if err != nil {
		return "", false, err
	}
	picked, ok := result.(providerPickerModel)
	if !ok {
		if pointer, ok := result.(*providerPickerModel); ok {
			picked = *pointer
		} else {
			return "", false, fmt.Errorf("provider picker returned invalid model")
		}
	}
	return picked.selected, !picked.cancelled && picked.selected != "", nil
}

func (m providerPickerModel) Init() tea.Cmd { return nil }

func (m providerPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor+1 < len(m.providers) {
			m.cursor++
		}
	case tea.KeyEnter:
		if len(m.providers) > 0 {
			m.selected = m.providers[m.cursor].Name
		}
		return m, tea.Quit
	case tea.KeyEsc, tea.KeyCtrlC:
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m providerPickerModel) View() string {
	lines := []string{"Select a Provider", ""}
	for i, p := range m.providers {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		if i == m.cursor {
			prefix = "❯ "
			style = style.Foreground(lipgloss.Color("39")).Bold(true)
		}
		lines = append(lines, prefix+style.Render(p.Name)+"  "+lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Render(p.Model))
	}
	lines = append(lines, "", "↑/↓ 选择 · Enter 确认 · Esc 取消")
	return strings.Join(lines, "\n")
}
