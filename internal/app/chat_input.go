package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var errChatInputCancelled = errors.New("chat input cancelled")

type chatInput interface {
	Read(context.Context) (string, error)
}

func newChatInput(in io.Reader, out io.Writer, modelName string) chatInput {
	if ttyInput, ttyOutput, ok := ttyChatFiles(in, out); ok {
		return &ttyChatInput{in: ttyInput, out: ttyOutput, modelName: modelName}
	}
	return newScannerChatInput(in)
}

func ttyChatFiles(in io.Reader, out io.Writer) (*os.File, *os.File, bool) {
	inputFile, inputOK := in.(*os.File)
	outputFile, outputOK := out.(*os.File)
	if !inputOK || !outputOK || !isTTY(inputFile) || !isTTY(outputFile) {
		return nil, nil, false
	}
	return inputFile, outputFile, true
}

func isTTY(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type scannerChatInput struct {
	scanner *bufio.Scanner
}

func newScannerChatInput(in io.Reader) *scannerChatInput {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	return &scannerChatInput{scanner: scanner}
}

func (s *scannerChatInput) Read(context.Context) (string, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return s.scanner.Text(), nil
}

type ttyChatInput struct {
	in        io.Reader
	out       io.Writer
	modelName string
	width     int
	height    int
}

func (t *ttyChatInput) Read(ctx context.Context) (string, error) {
	if separator := chatSeparator(t.out); separator != "" {
		if _, err := fmt.Fprintln(t.out, separator); err != nil {
			return "", err
		}
	}
	model := newChatInputModel(t.modelName, chatSeparator(t.out))
	program := tea.NewProgram(
		&model,
		tea.WithContext(ctx),
		tea.WithInput(t.in),
		tea.WithOutput(t.out),
		tea.WithoutSignalHandler(),
		tea.WithoutSignals(),
	)
	result, runErr := program.Run()
	var finalModel chatInputModel
	switch model := result.(type) {
	case chatInputModel:
		finalModel = model
	case *chatInputModel:
		finalModel = *model
	default:
		if runErr != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if runErr != nil {
			return "", runErr
		}
		return "", errors.New("chat input returned an invalid model")
	}
	t.width, t.height = finalModel.width, finalModel.height
	if finalModel.cancelled {
		return "", errChatInputCancelled
	}
	if runErr != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if runErr != nil && !finalModel.submitted {
		if errors.Is(runErr, tea.ErrProgramKilled) {
			return "", errChatInputCancelled
		}
		return "", runErr
	}
	return finalModel.editor.Value(), nil
}

type chatInputModel struct {
	editor    textarea.Model
	modelName string
	separator string
	width     int
	height    int
	submitted bool
	cancelled bool
}

func newChatInputModel(modelName, separator string) chatInputModel {
	editor := textarea.New()
	editor.Placeholder = "Send a message..."
	editor.Prompt = "❯ "
	editor.CharLimit = 0
	editor.SetHeight(1)
	editor.ShowLineNumbers = false
	editor.FocusedStyle.Base = lipgloss.NewStyle()
	editor.FocusedStyle.CursorLine = lipgloss.NewStyle()
	editor.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	editor.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	editor.BlurredStyle = editor.FocusedStyle
	editor.Focus()
	return chatInputModel{editor: editor, modelName: modelName, separator: separator, width: 80, height: 24}
}

func (m chatInputModel) Init() tea.Cmd {
	return m.editor.Cursor.BlinkCmd()
}

func (m chatInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		m.editor.SetWidth(size.Width)
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.Type {
		case tea.KeyEnter:
			m.submitted = true
			return m, tea.Quit
		case tea.KeyCtrlC:
			m.cancelled = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	return m, cmd
}

func (m chatInputModel) View() string {
	if m.submitted {
		return m.editor.View()
	}
	left := "  Enter 发送 · Ctrl+C 取消"
	right := m.modelName
	spaces := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if right == "" || spaces < 1 {
		right = ""
		spaces = 1
	}
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(left + strings.Repeat(" ", spaces) + right)
	return m.editor.View() + "\n" + footer + "\n" + m.separator
}
