package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/IsLand1314/Drift/internal/agent"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type approvalChoice int

const (
	approveOnce approvalChoice = iota
	approvePattern
	deny
)

type approvalInputModel struct {
	request   agent.PermissionRequest
	selected  int
	submitted bool
	cancelled bool
	width     int
}

func newApprovalInputModel(request agent.PermissionRequest) approvalInputModel {
	return approvalInputModel{request: request, width: 80}
}

func (m approvalInputModel) Init() tea.Cmd { return nil }

func (m approvalInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		m.selected = (m.selected + 2) % 3
	case tea.KeyDown:
		m.selected = (m.selected + 1) % 3
	case tea.KeyRunes:
		if len(key.Runes) == 1 && key.Runes[0] >= '1' && key.Runes[0] <= '3' {
			m.selected = int(key.Runes[0] - '1')
			m.submitted = true
			return m, tea.Quit
		}
	case tea.KeyEnter:
		m.submitted = true
		return m, tea.Quit
	case tea.KeyEscape, tea.KeyCtrlC:
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m approvalInputModel) View() string {
	titleText := "WriteFile command"
	switch m.request.ToolName {
	case "EditFile":
		titleText = "EditFile command"
	case "DeleteFile":
		titleText = "DeleteFile command"
	case "Bash":
		titleText = "Bash command"
	}
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true).Render(titleText)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	detail := "  " + m.request.Path
	if m.request.ToolName == "Bash" {
		detail = "  " + m.request.Command + "\n\n  cwd: " + m.request.CWD
	}
	text := title + "\n\n" + detail + "\n\n" + muted.Render("  This command requires approval") + "\n\n"
	options := []string{"1. Yes", "2. Yes, and don't ask again for this pattern", "3. No"}
	for i, option := range options {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		if i == m.selected {
			prefix = "❯ "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
		}
		text += style.Render(prefix+option) + "\n"
	}
	return text
}

func (m approvalInputModel) choice() approvalChoice {
	if m.cancelled {
		return deny
	}
	return approvalChoice(m.selected)
}

func (m approvalInputModel) renderedLines() int {
	return strings.Count(m.View(), "\n")
}

func approvalCleanupSequence(lines int) string {
	if lines < 1 {
		return ""
	}
	var text strings.Builder
	for range lines {
		text.WriteString("\r\x1b[1A\x1b[2K")
	}
	return text.String()
}

func readApprovalChoice(ctx context.Context, input chatInput, request agent.PermissionRequest) (approvalChoice, error) {
	if tty, ok := input.(*tuiMainScreenInput); ok {
		model := newApprovalInputModel(request)
		program := tea.NewProgram(&model, tea.WithContext(ctx), tea.WithInput(tty.in), tea.WithOutput(tty.out), tea.WithoutSignalHandler(), tea.WithoutSignals())
		result, err := program.Run()
		if err != nil && ctx.Err() != nil {
			return deny, ctx.Err()
		}
		if err != nil {
			return deny, err
		}
		finalModel, ok := result.(approvalInputModel)
		if !ok {
			if pointer, pointerOK := result.(*approvalInputModel); pointerOK {
				finalModel = *pointer
			} else {
				return deny, fmt.Errorf("approval input returned an invalid model")
			}
		}
		if chatUsesColor(tty.out) {
			_, _ = io.WriteString(tty.out, approvalCleanupSequence(finalModel.renderedLines()))
		}
		return finalModel.choice(), nil
	}
	answer, err := input.Read(ctx)
	if err != nil {
		return deny, err
	}
	switch strings.TrimSpace(strings.ToLower(answer)) {
	case "y", "yes", "1":
		return approveOnce, nil
	case "2":
		return approvePattern, nil
	default:
		return deny, nil
	}
}
