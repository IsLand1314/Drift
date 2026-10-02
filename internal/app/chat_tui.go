package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/changes"
	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/session"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ttyChatModel is the sole owner of a real terminal chat screen. Runtime code
// communicates through messages; it never writes transcript text directly.
type ttyChatModel struct {
	ctx              context.Context
	runner           *agent.Runner
	audit            session.Writer
	trace            agent.EventSink
	persistence      *chatPersistence
	status           chatStatus
	interrupt        *interruptCoordinator
	permissionMemory *permissionMemory

	textarea      textarea.Model
	viewport      viewport.Model
	width, height int
	lines         []string
	stream        string
	toolStarted   map[string]time.Time
	events        chan tea.Msg
	running       bool
	started       time.Time
	spinner       int
	approval      *tuiApproval
	resume        *tuiResume
	exitCode      int
}

type tuiAgentEvent struct{ event agent.Event }
type tuiTurnDone struct{ err error }
type tuiPermission struct {
	request agent.PermissionRequest
	reply   chan agent.PermissionDecision
}
type tuiTick time.Time
type tuiApproval struct {
	request  agent.PermissionRequest
	reply    chan agent.PermissionDecision
	selected int
}
type tuiResume struct {
	items  []conversation.Metadata
	cursor int
}

func runTTYChatLoop(ctx context.Context, runner *agent.Runner, audit session.Writer, trace agent.EventSink, persistence *chatPersistence, status chatStatus, interrupt *interruptCoordinator, in io.Reader, out io.Writer) int {
	m := newTTYChatModel(ctx, runner, audit, trace, persistence, status, interrupt)
	final, err := tea.NewProgram(&m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen(), tea.WithoutSignalHandler(), tea.WithoutSignals()).Run()
	if err != nil {
		return 1
	}
	if model, ok := final.(ttyChatModel); ok {
		return model.exitCode
	}
	return m.exitCode
}

func newTTYChatModel(ctx context.Context, runner *agent.Runner, audit session.Writer, trace agent.EventSink, persistence *chatPersistence, status chatStatus, interrupt *interruptCoordinator) ttyChatModel {
	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = "Send a message..."
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	ta.Focus()
	m := ttyChatModel{ctx: ctx, runner: runner, audit: audit, trace: trace, persistence: persistence, status: status, interrupt: interrupt, permissionMemory: newPermissionMemory(), textarea: ta, toolStarted: make(map[string]time.Time), events: make(chan tea.Msg, 32), width: 80, height: 24}
	if persistence != nil && persistence.persistent {
		m.lines = append(m.lines, ttySessionHeader(persistence)...)
		m.lines = append(m.lines, transcriptFromLLMMessages(persistence.snapshot.Messages)...)
	} else if persistence != nil {
		m.lines = append(m.lines, ttySessionHeader(persistence)...)
	}
	if runner != nil {
		runner.SetPermissionPrompt(func(promptCtx context.Context, request agent.PermissionRequest) (agent.PermissionDecision, error) {
			if m.permissionMemory.Allow(request) {
				return agent.PermissionDecision{Allow: true, Reason: "session_pattern_approved"}, nil
			}
			reply := make(chan agent.PermissionDecision, 1)
			select {
			case m.events <- tuiPermission{request, reply}:
			case <-promptCtx.Done():
				return agent.PermissionDecision{}, promptCtx.Err()
			}
			select {
			case decision := <-reply:
				return decision, nil
			case <-promptCtx.Done():
				return agent.PermissionDecision{}, promptCtx.Err()
			}
		})
	}
	return m
}

func (m ttyChatModel) Init() tea.Cmd { return tea.Batch(textarea.Blink, m.waitEvent(), tickTUI()) }
func tickTUI() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tuiTick(t) })
}
func (m ttyChatModel) waitEvent() tea.Cmd { return func() tea.Msg { return <-m.events } }

func (m ttyChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.textarea.SetWidth(maxTUI(1, size.Width-2))
		resizeTTYTextarea(&m)
		m.viewport = viewport.New(maxTUI(1, size.Width), maxTUI(1, size.Height-5))
		return m, nil
	}
	switch v := msg.(type) {
	case tuiTick:
		if m.running {
			m.spinner++
			return m, tickTUI()
		}
	case tuiAgentEvent:
		m.applyEvent(v.event)
		return m, m.waitEvent()
	case tuiPermission:
		m.approval = &tuiApproval{request: v.request, reply: v.reply}
		return m, m.waitEvent()
	case tuiTurnDone:
		m.finishTurn(v.err)
		return m, m.waitEvent()
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.approval != nil {
			return m.handleApproval(key)
		}
		if m.resume != nil {
			return m.handleResume(key)
		}
		if m.running {
			if key.Type == tea.KeyCtrlC && m.interrupt != nil {
				m.interrupt.mu.Lock()
				cancel := m.interrupt.turnCancel
				m.interrupt.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			}
			return m, nil
		}
		switch key.Type {
		case tea.KeyCtrlC:
			m.exitCode = 130
			return m, tea.Quit
		case tea.KeyEnter:
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				return m, nil
			}
			m.textarea.Reset()
			resizeTTYTextarea(&m)
			if text == "exit" || text == "/exit" || text == "quit" {
				return m, tea.Quit
			}
			m.lines = append(m.lines, "❯ "+text)
			if m.handleCommand(text) {
				return m, nil
			}
			m.startTurn(text)
			return m, tickTUI()
		}
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	resizeTTYTextarea(&m)
	return m, cmd
}

func (m *ttyChatModel) handleCommand(text string) bool {
	if text == "/status" {
		var b bytes.Buffer
		writeChatStatus(&b, m.runner, m.persistence, m.status)
		m.lines = append(m.lines, b.String())
		return true
	}
	if text == "/clear" {
		if m.persistence != nil {
			_ = m.persistence.clearRunner(m.runner)
		} else {
			m.runner.ResetContext()
		}
		m.lines = ttySessionHeader(m.persistence)
		return true
	}
	if text == "/new" {
		if err := startNewChatSession(m.runner, m.persistence); err != nil {
			m.lines = append(m.lines, "✖ "+err.Error())
		} else {
			m.lines = ttySessionHeader(m.persistence)
		}
		return true
	}
	if text == "/resume" && m.persistence != nil && m.persistence.persistent {
		items, err := m.persistence.store.List()
		if err != nil {
			m.lines = append(m.lines, "✖ "+err.Error())
			return true
		}
		non := make([]conversation.Metadata, 0, len(items))
		for _, x := range items {
			if x.MessageCount > 0 {
				non = append(non, x)
			}
		}
		if len(non) == 0 {
			m.lines = append(m.lines, "暂无可恢复会话")
			return true
		}
		m.resume = &tuiResume{items: non}
		return true
	}
	if strings.HasPrefix(text, "/rename ") {
		if err := renameChatSession(m.persistence, strings.TrimSpace(strings.TrimPrefix(text, "/rename"))); err != nil {
			m.lines = append(m.lines, "✖ "+err.Error())
		} else {
			m.lines = append(m.lines, "已更新会话名称： "+m.persistence.snapshot.Title)
		}
		return true
	}
	return false
}

func (m *ttyChatModel) startTurn(prompt string) {
	m.running, m.started = true, time.Now()
	m.stream = ""
	go func() {
		turnCtx, cancel := context.WithCancel(m.ctx)
		end := m.interrupt.beginTurn(cancel)
		defer end()
		defer cancel()
		var set *changes.ChangeSet
		if m.status.ToolCount > 3 {
			set, _ = changes.Begin(m.status.Workspace, m.started)
			if set != nil {
				turnCtx = changes.WithChangeSet(turnCtx, set)
			}
		}
		err := m.runner.RunEvents(turnCtx, prompt, func(e agent.Event) error {
			if m.audit != nil {
				if err := m.audit.Append(e); err != nil {
					return err
				}
			}
			if m.trace != nil {
				_ = m.trace(e)
			}
			m.emit(tuiAgentEvent{e})
			return nil
		})
		if set != nil {
			status := "complete"
			if err != nil {
				status = "partial"
			}
			_ = set.Finalize(status)
		}
		m.emit(tuiTurnDone{err})
	}()
}

func (m *ttyChatModel) emit(msg tea.Msg) {
	select {
	case m.events <- msg:
	case <-m.ctx.Done():
	}
}

func (m *ttyChatModel) applyEvent(e agent.Event) {
	if m.persistence != nil {
		m.persistence.usage.add(e)
	}
	switch e.Type {
	case agent.EventTextDelta:
		m.stream += e.Text
	case agent.EventToolCall:
		m.toolStarted[e.ToolCallID] = time.Now()
	case agent.EventToolResult:
		if shouldRenderToolResult(e) {
			elapsed := time.Duration(0)
			if started, ok := m.toolStarted[e.ToolCallID]; ok {
				elapsed = time.Since(started)
				delete(m.toolStarted, e.ToolCallID)
			}
			m.lines = append(m.lines, chatToolResultLine(nil, e, e.Path, elapsed))
		}
	case agent.EventPermissionRequest: /* callback supplies overlay */
	}
}
func (m *ttyChatModel) finishTurn(err error) {
	if m.stream != "" {
		m.lines = append(m.lines, "● "+m.stream)
	}
	if err != nil {
		m.lines = append(m.lines, "✖ "+err.Error())
	}
	m.lines = append(m.lines, fmt.Sprintf("Done - %.1fs", time.Since(m.started).Seconds()))
	m.running = false
	if m.persistence != nil && m.persistence.persistent {
		_ = m.persistence.saveRunner(m.runner)
	}
}
func (m ttyChatModel) handleApproval(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.approval
	switch key.Type {
	case tea.KeyUp:
		a.selected = (a.selected + 2) % 3
	case tea.KeyDown:
		a.selected = (a.selected + 1) % 3
	case tea.KeyEnter:
		a.reply <- approvalDecision(a.selected, m.permissionMemory, a.request)
		m.approval = nil
	case tea.KeyEscape, tea.KeyCtrlC:
		a.reply <- agent.PermissionDecision{Reason: "user_denied"}
		m.approval = nil
	}
	return m, nil
}
func approvalDecision(selected int, memory *permissionMemory, request agent.PermissionRequest) agent.PermissionDecision {
	if selected == 1 {
		memory.Remember(request)
		return agent.PermissionDecision{Allow: true, Reason: "session_pattern_approved"}
	}
	if selected == 0 {
		return agent.PermissionDecision{Allow: true, Reason: "user_approved"}
	}
	return agent.PermissionDecision{Reason: "user_denied"}
}
func (m ttyChatModel) handleResume(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.resume.items) == 0 {
		m.resume = nil
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.resume.cursor > 0 {
			m.resume.cursor--
		}
	case tea.KeyDown:
		if m.resume.cursor+1 < len(m.resume.items) {
			m.resume.cursor++
		}
	case tea.KeyEnter:
		id := m.resume.items[m.resume.cursor].ID
		m.resume = nil
		if err := switchChatSession(m.runner, m.persistence, id); err != nil {
			m.lines = append(m.lines, "✖ "+err.Error())
		} else {
			m.lines = ttySessionHeader(m.persistence)
			m.lines = append(m.lines, transcriptFromLLMMessages(m.runner.Messages())...)
			m.lines = append(m.lines, "已切换会话： "+id)
		}
	case tea.KeyEscape, tea.KeyCtrlC:
		m.resume = nil
	}
	return m, nil
}

func ttySessionHeader(persistence *chatPersistence) []string {
	if persistence == nil {
		return nil
	}
	if persistence.persistent {
		return []string{"Session ID: " + persistence.snapshot.ID, "注意：此会话会保存完整本地上下文，可能包含用户输入和读取结果；使用 --no-session 可关闭"}
	}
	return []string{"已禁用完整会话保存；仍保留脱敏审计"}
}

func transcriptFromLLMMessages(messages []llm.Message) []string {
	lines := make([]string, 0, len(messages))
	for _, x := range messages {
		if x.Role == "user" {
			lines = append(lines, "❯ "+x.Content)
		} else if x.Role == "assistant" && x.Content != "" {
			lines = append(lines, "● "+x.Content)
		}
	}
	return lines
}
func (m ttyChatModel) View() string {
	content := strings.Join(m.lines, "\n")
	if m.running {
		if m.stream != "" {
			content += "\n● " + m.stream
		} else {
			content += "\n" + spinnerFrame(m.spinner) + " Thinking..."
		}
	}
	panel := m.textarea.View()
	if m.approval != nil {
		panel = renderTUIApproval(*m.approval)
	} else if m.resume != nil {
		panel = renderTUIResume(*m.resume)
	}
	panelRows := strings.Count(panel, "\n") + 1
	footer := tuiFooter(m.status.Model, m.width)
	footerRows := strings.Count(footer, "\n") + 1
	m.viewport.SetContent(styleTranscript(content))
	m.viewport.Width = maxTUI(1, m.width)
	m.viewport.Height = maxTUI(1, m.height-panelRows-footerRows-2)
	m.viewport.GotoBottom()
	var b strings.Builder
	b.WriteString(m.viewport.View())
	b.WriteString("\n" + tuiRule(m.width) + "\n" + panel + "\n" + tuiRule(m.width) + "\n" + footer)
	return b.String()
}

var (
	tuiAccent    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	tuiUser      = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	tuiAI        = lipgloss.NewStyle().Foreground(lipgloss.Color("99"))
	tuiOK        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	tuiError     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	tuiMuted     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	tuiRuleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func styleTranscript(content string) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "❯ "):
			lines[i] = tuiAccent.Render("❯") + " " + tuiUser.Render(line[2:])
		case strings.HasPrefix(line, "● "):
			lines[i] = tuiAI.Render("●") + " " + tuiUser.Render(line[2:])
		case strings.HasPrefix(line, "✓ "):
			lines[i] = tuiOK.Render("✓") + " " + tuiMuted.Render(line[2:])
		case strings.HasPrefix(line, "✖ "):
			lines[i] = tuiError.Render("✖") + " " + tuiError.Render(line[2:])
		case strings.HasPrefix(line, "Done -"):
			lines[i] = tuiMuted.Render(line)
		case strings.HasPrefix(line, "Session ID:") || strings.HasPrefix(line, "注意：") || strings.HasPrefix(line, "已禁用完整会话"):
			lines[i] = tuiMuted.Render(line)
		default:
			lines[i] = tuiUser.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

func tuiRule(width int) string {
	return tuiRuleStyle.Render(strings.Repeat("─", maxTUI(1, width)))
}

func tuiFooter(model string, width int) string {
	left := "  Enter 发送 · Ctrl+C 取消"
	if model == "" {
		return tuiMuted.Render(left)
	}
	if lipgloss.Width(left)+lipgloss.Width(model)+1 <= width {
		return tuiMuted.Render(left) + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(model)) + tuiMuted.Render(model)
	}
	return tuiMuted.Render(left) + "\n" + strings.Repeat(" ", maxTUI(1, width-lipgloss.Width(model))) + tuiMuted.Render(model)
}

func resizeTTYTextarea(m *ttyChatModel) {
	width := maxTUI(1, m.width-2)
	rows := 1
	for _, line := range strings.Split(m.textarea.Value(), "\n") {
		lineWidth := lipgloss.Width(line)
		if lineWidth > width {
			rows += (lineWidth - 1) / width
		}
	}
	m.textarea.SetHeight(rows)
}

func renderTUIApproval(a tuiApproval) string {
	title := "WriteFile command"
	if a.request.ToolName == "edit_file" {
		title = "EditFile command"
	}
	if a.request.ToolName == "delete_file" {
		title = "DeleteFile command"
	}
	opts := []string{"1. Yes", "2. Yes, and don't ask again for this pattern", "3. No"}
	var b strings.Builder
	b.WriteString(tuiAccent.Bold(true).Render(title) + "\n\n  " + tuiUser.Render(a.request.Path) + "\n\n  " + tuiMuted.Render("This command requires approval") + "\n\n")
	for i, x := range opts {
		if i == a.selected {
			b.WriteString(tuiAccent.Render("❯") + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("236")).Render(x))
		} else {
			b.WriteString("  " + tuiMuted.Render(x))
		}
		b.WriteString("\n")
	}
	return b.String()
}
func renderTUIResume(r tuiResume) string {
	var b strings.Builder
	b.WriteString(tuiAccent.Bold(true).Render(fmt.Sprintf("Resume session (1 of %d)", len(r.items))) + "\n")
	b.WriteString(tuiMuted.Render("⌕ Search…") + "\n")
	for i, x := range r.items {
		p := "  "
		if i == r.cursor {
			p = tuiAccent.Render("❯") + " "
		}
		title := x.Title
		if title == "" {
			title = x.Preview
		}
		if i == r.cursor {
			title = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("236")).Render(title)
		} else {
			title = tuiUser.Render(title)
		}
		b.WriteString(p + title + "\n")
	}
	return b.String()
}
func maxTUI(a, b int) int {
	if a > b {
		return a
	}
	return b
}
