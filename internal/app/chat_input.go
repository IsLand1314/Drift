package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/IsLand1314/Drift/internal/tool"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var errChatInputCancelled = errors.New("chat input cancelled")

type chatInput interface {
	Read(context.Context) (string, error)
}

func askUserQuestion(ctx context.Context, input chatInput, out io.Writer, question tool.Question) (tool.QuestionAnswer, error) {
	fmt.Fprintln(out, "\n● "+question.Question)
	for index, option := range question.Options {
		line := fmt.Sprintf("  %d. %s", index+1, option.Label)
		if option.Description != "" {
			line += " — " + option.Description
		}
		fmt.Fprintln(out, line)
	}
	prompt := "输入选项编号"
	if question.MultiSelect {
		prompt += "（多选用逗号分隔）"
	}
	if question.AllowFreeText {
		prompt += "或文本"
	}
	fmt.Fprint(out, prompt+"；/cancel 取消: ")
	value, err := input.Read(ctx)
	if errors.Is(err, errChatInputCancelled) {
		return tool.QuestionAnswer{Cancelled: true}, nil
	}
	if err != nil {
		return tool.QuestionAnswer{}, err
	}
	return parseQuestionAnswer(question, value)
}

func parseQuestionAnswer(question tool.Question, raw string) (tool.QuestionAnswer, error) {
	value := strings.TrimSpace(raw)
	if value == "/cancel" || strings.EqualFold(value, "cancel") {
		return tool.QuestionAnswer{Cancelled: true}, nil
	}
	if value == "" {
		return tool.QuestionAnswer{}, errors.New("请选择一个选项或输入文本")
	}
	byID := make(map[string]string, len(question.Options))
	for _, option := range question.Options {
		byID[option.ID] = option.ID
	}
	parts := strings.Split(value, ",")
	selected := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		index, err := strconv.Atoi(part)
		if err == nil && index >= 1 && index <= len(question.Options) {
			selected = append(selected, question.Options[index-1].ID)
			continue
		}
		if id, ok := byID[part]; ok {
			selected = append(selected, id)
			continue
		}
		if question.AllowFreeText && len(parts) == 1 {
			return tool.QuestionAnswer{Text: value}, nil
		}
		return tool.QuestionAnswer{}, errors.New("选项无效")
	}
	if !question.MultiSelect && len(selected) != 1 {
		return tool.QuestionAnswer{}, errors.New("该问题只能选择一个选项")
	}
	return tool.QuestionAnswer{Selected: selected}, nil
}

func newTuiMainScreenInput(in io.Reader, out io.Writer, modelName string) chatInput {
	if ttyInput, ttyOutput, ok := tuiMainScreenFiles(in, out); ok {
		return &tuiMainScreenInput{in: ttyInput, out: ttyOutput, modelName: modelName}
	}
	return newScannerChatInput(in)
}

func tuiMainScreenFiles(in io.Reader, out io.Writer) (*os.File, *os.File, bool) {
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

// tuiMainScreenInput owns only the transient input editor. Completed turns are
// printed by runTuiMainScreenLoop and remain in terminal scrollback.
type tuiMainScreenInput struct {
	in        io.Reader
	out       io.Writer
	modelName string
	width     int
	height    int
}

func (t *tuiMainScreenInput) Read(ctx context.Context) (string, error) {
	if separator := chatSeparator(t.out); separator != "" {
		if _, err := fmt.Fprintln(t.out, separator); err != nil {
			return "", err
		}
	}
	model := newChatInputModel(t.modelName, chatSeparator(t.out))
	program := tea.NewProgram(&model, tea.WithContext(ctx), tea.WithInputTTY(), tea.WithOutput(t.out), tea.WithoutSignalHandler(), tea.WithoutSignals())
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

type permissionModeInputModel struct {
	picker    permissionPicker
	submitted bool
	cancelled bool
}

func newPermissionModeInputModel(current permissionMode) permissionModeInputModel {
	return permissionModeInputModel{picker: *newPermissionPicker(current)}
}

func (m permissionModeInputModel) Init() tea.Cmd { return nil }

func (m permissionModeInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.picker.cursor > 0 {
			m.picker.cursor--
		}
	case tea.KeyDown:
		if m.picker.cursor+1 < len(m.picker.options) {
			m.picker.cursor++
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

func (m permissionModeInputModel) View() string { return renderTUIPermissionPicker(m.picker) }

func (m permissionModeInputModel) mode() permissionMode {
	if m.cancelled || len(m.picker.options) == 0 {
		return ""
	}
	return m.picker.options[m.picker.cursor]
}

func (m permissionModeInputModel) renderedLines() int {
	return strings.Count(m.View(), "\n")
}

func readPermissionModeChoice(ctx context.Context, input chatInput, current permissionMode) (permissionMode, bool, error) {
	tui, ok := input.(*tuiMainScreenInput)
	if !ok {
		return "", false, nil
	}
	model := newPermissionModeInputModel(current)
	result, err := tea.NewProgram(&model, tea.WithContext(ctx), tea.WithInput(tui.in), tea.WithOutput(tui.out), tea.WithoutSignalHandler(), tea.WithoutSignals()).Run()
	if err != nil {
		return "", false, err
	}
	final, ok := result.(permissionModeInputModel)
	if !ok {
		if pointer, pointerOK := result.(*permissionModeInputModel); pointerOK {
			final = *pointer
		} else {
			return "", false, errors.New("permission mode input returned an invalid model")
		}
	}
	if chatUsesColor(tui.out) {
		_, _ = io.WriteString(tui.out, approvalCleanupSequence(final.renderedLines()))
	}
	if final.cancelled {
		return "", false, nil
	}
	return final.mode(), true, nil
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
