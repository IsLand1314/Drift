package app

import (
	"bytes"
	"context"
	"errors"
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

// tuiFullScreen is the alternate-screen renderer. Runtime code
// communicates through messages; it never writes transcript text directly.
type tuiFullScreen struct {
	ctx                context.Context
	runner             *agent.Runner
	audit              session.Writer
	trace              agent.EventSink
	persistence        *chatPersistence
	status             chatStatus
	interrupt          *interruptCoordinator
	permissionMemory   *permissionMemory
	permissionPolicy   *permissionPolicy
	permissionMode     *permissionMode
	pendingPermissions map[string]agent.PermissionRequest

	textarea         textarea.Model
	viewport         viewport.Model
	width, height    int
	lines            []string
	stream           string
	toolStarted      map[string]toolProgressTUI
	events           chan tea.Msg
	running          bool
	followBottom     bool
	started          time.Time
	spinner          int
	approval         *tuiApproval
	resume           *tuiResume
	permissionPicker *permissionPicker
	exitCode         int
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
type permissionPicker struct {
	options []permissionMode
	cursor  int
}
type toolProgressTUI struct {
	started time.Time
	path    string
	lineIdx int
}

func runTuiFullScreen(ctx context.Context, runner *agent.Runner, audit session.Writer, trace agent.EventSink, persistence *chatPersistence, status chatStatus, interrupt *interruptCoordinator, in io.Reader, out io.Writer) int {
	m := newTuiFullScreen(ctx, runner, audit, trace, persistence, status, interrupt)
	// The fullscreen renderer owns the frame in the alternate screen. Mouse
	// reporting remains disabled so terminal selection and paste stay available.
	final, err := tea.NewProgram(&m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen(), tea.WithoutSignalHandler(), tea.WithoutSignals()).Run()
	if err != nil {
		return 1
	}
	if model, ok := final.(*tuiFullScreen); ok {
		return model.exitCode
	}
	return m.exitCode
}

func newTuiFullScreen(ctx context.Context, runner *agent.Runner, audit session.Writer, trace agent.EventSink, persistence *chatPersistence, status chatStatus, interrupt *interruptCoordinator) tuiFullScreen {
	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = "Send a message..."
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	ta.Focus()
	policy := (*permissionPolicy)(nil)
	policyWarning := false
	if status.Workspace != "" {
		var policyErr error
		policy, policyErr = loadPermissionPolicy(status.Workspace)
		policyWarning = policyErr != nil
		if policy == nil {
			policy = newPermissionPolicy(status.Workspace)
		}
	}
	mode := status.PermissionMode
	if mode == "" {
		mode = permissionModeDefault
	}
	m := tuiFullScreen{ctx: ctx, runner: runner, audit: audit, trace: trace, persistence: persistence, status: status, interrupt: interrupt, permissionMemory: newPermissionMemory(), permissionPolicy: policy, permissionMode: &mode, pendingPermissions: make(map[string]agent.PermissionRequest), textarea: ta, toolStarted: make(map[string]toolProgressTUI), events: make(chan tea.Msg, 32), followBottom: true, width: 80, height: 24}
	if persistence != nil && persistence.persistent {
		m.lines = append(m.lines, tuiSessionHeader(persistence)...)
		m.lines = append(m.lines, transcriptFromLLMMessages(persistence.snapshot.Messages)...)
	} else if persistence != nil {
		m.lines = append(m.lines, tuiSessionHeader(persistence)...)
	}
	if policyWarning {
		m.lines = append(m.lines, "⚠ 权限策略加载失败，已恢复为每次询问")
	}
	if runner != nil {
		runner.SetPermissionPrompt(func(promptCtx context.Context, request agent.PermissionRequest) (agent.PermissionDecision, error) {
			if decision := resolvePermission(*m.permissionMode, m.permissionMemory, m.permissionPolicy, request); decision.Policy != agent.PolicyAsk {
				return decision, nil
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

func (m tuiFullScreen) Init() tea.Cmd { return tea.Batch(textarea.Blink, m.waitEvent(), tickTUI()) }
func tickTUI() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tuiTick(t) })
}
func (m tuiFullScreen) waitEvent() tea.Cmd { return func() tea.Msg { return <-m.events } }

func (m *tuiFullScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.textarea.SetWidth(maxTUI(1, size.Width-3))
		resizeTuiFullScreenTextarea(m)
		viewportWidth := maxTUI(1, size.Width)
		viewportHeight := maxTUI(1, size.Height-5)
		if m.viewport.Width == 0 && m.viewport.Height == 0 {
			m.viewport = viewport.New(viewportWidth, viewportHeight)
		} else {
			atBottom := m.viewport.AtBottom()
			offset := m.viewport.YOffset
			m.viewport.Width = viewportWidth
			m.viewport.Height = viewportHeight
			if atBottom {
				m.followBottom = true
			} else {
				m.viewport.SetYOffset(offset)
			}
		}
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
		m.followBottom = true
		return m, m.waitEvent()
	case tuiPermission:
		m.approval = &tuiApproval{request: v.request, reply: v.reply}
		return m, m.waitEvent()
	case tuiTurnDone:
		m.finishTurn(v.err)
		m.followBottom = true
		return m, m.waitEvent()
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.approval != nil {
			return m.handleApproval(key)
		}
		if m.resume != nil {
			return m.handleResume(key)
		}
		if m.permissionPicker != nil {
			return m.handlePermissionPicker(key)
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
		if isTTYViewportKey(key) {
			switch key.Type {
			case tea.KeyHome, tea.KeyCtrlHome:
				m.viewport.GotoTop()
				m.followBottom = false
			case tea.KeyEnd, tea.KeyCtrlEnd:
				m.viewport.GotoBottom()
				m.followBottom = true
			default:
				m.followBottom = false
			}
			var cmd tea.Cmd
			if key.Type != tea.KeyHome && key.Type != tea.KeyEnd && key.Type != tea.KeyCtrlHome && key.Type != tea.KeyCtrlEnd {
				m.viewport, cmd = m.viewport.Update(msg)
			}
			return m, cmd
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
			resizeTuiFullScreenTextarea(m)
			if text == "exit" || text == "/exit" || text == "quit" {
				return m, tea.Quit
			}
			if strings.HasPrefix(text, "/permissions") {
				if m.handleCommand(text) {
					return m, nil
				}
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
	resizeTuiFullScreenTextarea(m)
	return m, cmd
}

func (m *tuiFullScreen) handleCommand(text string) bool {
	if text == "/permissions" {
		m.permissionPicker = newPermissionPicker(*m.permissionMode)
		return true
	}
	if mode, handled, modeErr := parsePermissionModeCommand(text); handled {
		if modeErr != nil {
			m.lines = append(m.lines, "✖ "+modeErr.Error())
		} else if mode == "" {
			current := m.status.PermissionMode
			if current == "" {
				current = permissionModeDefault
			}
			m.lines = append(m.lines, tuiMuted.Render("权限模式： "+string(current)))
		} else {
			*m.permissionMode = mode
			m.status.PermissionMode = mode
			m.lines = append(m.lines, tuiMuted.Render("权限模式已切换为 "+string(mode)))
		}
		return true
	}
	if message, handled := handlePermissionCommand(m.permissionPolicy, text); handled {
		m.lines = append(m.lines, message)
		return true
	}
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
		m.lines = tuiSessionHeader(m.persistence)
		return true
	}
	if text == "/new" {
		if err := startNewChatSession(m.runner, m.persistence); err != nil {
			m.lines = append(m.lines, "✖ "+err.Error())
		} else {
			m.lines = tuiSessionHeader(m.persistence)
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

func newPermissionPicker(current permissionMode) *permissionPicker {
	options := []permissionMode{permissionModeDefault, permissionModeAcceptEdits, permissionModePlan, permissionModeBypass}
	picker := &permissionPicker{options: options}
	for i, mode := range options {
		if mode == current {
			picker.cursor = i
			break
		}
	}
	return picker
}

func (m *tuiFullScreen) handlePermissionPicker(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.permissionPicker
	if p == nil {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor+1 < len(p.options) {
			p.cursor++
		}
	case tea.KeyEnter:
		mode := p.options[p.cursor]
		*m.permissionMode = mode
		m.status.PermissionMode = mode
		m.lines = append(m.lines, tuiMuted.Render("权限模式已切换为 "+string(mode)))
		m.permissionPicker = nil
	case tea.KeyEscape, tea.KeyCtrlC:
		m.permissionPicker = nil
	}
	return m, nil
}

func (m *tuiFullScreen) startTurn(prompt string) {
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

func (m *tuiFullScreen) emit(msg tea.Msg) {
	select {
	case m.events <- msg:
	case <-m.ctx.Done():
	}
}

func (m *tuiFullScreen) applyEvent(e agent.Event) {
	if m.persistence != nil {
		m.persistence.usage.add(e)
	}
	switch e.Type {
	case agent.EventTextDelta:
		m.stream += e.Text
	case agent.EventToolCall:
		path := safeToolPathForTool(e.ToolName, e.Arguments)
		m.lines = append(m.lines, "● "+toolLabel(e.ToolName)+formatToolPath(path)+" ...")
		m.toolStarted[e.ToolCallID] = toolProgressTUI{started: time.Now(), path: path, lineIdx: len(m.lines) - 1}
	case agent.EventToolResult:
		requestKey := permissionRuleKey(permissionRuleFromRequest(agent.PermissionRequest{ToolName: e.ToolName, Operation: e.Operation, Path: e.Path, Command: e.Command, CWD: e.CWD}))
		if request, ok := m.pendingPermissions[requestKey]; ok {
			delete(m.pendingPermissions, requestKey)
			if e.ErrorSummary == "" && m.permissionPolicy != nil {
				if err := m.permissionPolicy.remember(request); err != nil {
					m.lines = append(m.lines, "⚠ 本次已允许，但权限策略持久化失败")
				}
			}
		}
		progress, ok := m.toolStarted[e.ToolCallID]
		if ok {
			delete(m.toolStarted, e.ToolCallID)
		}
		path := e.Path
		if path == "" {
			path = progress.path
		}
		elapsed := time.Duration(0)
		if ok {
			elapsed = time.Since(progress.started)
		}
		if ok && progress.lineIdx < len(m.lines) {
			if shouldRenderToolResult(e) {
				m.lines[progress.lineIdx] = chatToolResultLine(nil, e, path, elapsed)
			} else {
				m.lines = append(m.lines[:progress.lineIdx], m.lines[progress.lineIdx+1:]...)
				for id, item := range m.toolStarted {
					if item.lineIdx > progress.lineIdx {
						item.lineIdx--
						m.toolStarted[id] = item
					}
				}
			}
		} else if shouldRenderToolResult(e) {
			m.lines = append(m.lines, chatToolResultLine(nil, e, path, elapsed))
		}
	case agent.EventPermissionRequest: /* callback supplies overlay */
	}
}
func (m *tuiFullScreen) finishTurn(err error) {
	if m.stream != "" {
		m.lines = append(m.lines, "● "+m.stream)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.lines = append(m.lines, "✖ 当前轮已取消；会话仍可继续")
		} else {
			m.lines = append(m.lines, "✖ "+err.Error())
		}
	}
	m.lines = append(m.lines, fmt.Sprintf("Done - %.1fs", time.Since(m.started).Seconds()))
	m.running = false
	if m.persistence != nil && m.persistence.persistent {
		_ = m.persistence.saveRunner(m.runner)
	}
}
func (m *tuiFullScreen) handleApproval(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.approval
	switch key.Type {
	case tea.KeyUp:
		a.selected = (a.selected + 2) % 3
	case tea.KeyDown:
		a.selected = (a.selected + 1) % 3
	case tea.KeyEnter:
		decision := approvalDecision(a.selected, m.permissionMemory, a.request)
		if decision.Reason == "persistent_pattern_pending" {
			m.pendingPermissions[permissionRuleKey(permissionRuleFromRequest(a.request))] = a.request
		}
		a.reply <- decision
		m.approval = nil
	case tea.KeyEscape, tea.KeyCtrlC:
		a.reply <- agent.PermissionDecision{Reason: "approval_cancelled", Policy: agent.PolicyAsk, Approval: agent.ApprovalCancelled, Source: agent.PermissionSourceUser}
		m.approval = nil
	}
	return m, nil
}
func approvalDecision(selected int, memory *permissionMemory, request agent.PermissionRequest) agent.PermissionDecision {
	if selected == 1 {
		return agent.PermissionDecision{Allow: true, Reason: "persistent_pattern_pending", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowPersistent, Source: agent.PermissionSourceUser}
	}
	if selected == 0 {
		return agent.PermissionDecision{Allow: true, Reason: "user_approved", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowOnce, Source: agent.PermissionSourceUser}
	}
	return agent.PermissionDecision{Reason: "user_denied", Policy: agent.PolicyAsk, Approval: agent.ApprovalDeny, Source: agent.PermissionSourceUser}
}
func (m *tuiFullScreen) handleResume(key tea.KeyMsg) (tea.Model, tea.Cmd) {
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
			m.lines = tuiSessionHeader(m.persistence)
			m.lines = append(m.lines, transcriptFromLLMMessages(m.runner.Messages())...)
			m.lines = append(m.lines, "已切换会话： "+id)
		}
	case tea.KeyEscape, tea.KeyCtrlC:
		m.resume = nil
	}
	return m, nil
}

func tuiSessionHeader(persistence *chatPersistence) []string {
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
func (m *tuiFullScreen) View() string {
	m.textarea.SetWidth(maxTUI(1, m.width-3))
	resizeTuiFullScreenTextarea(m)
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
	} else if m.permissionPicker != nil {
		panel = renderTUIPermissionPicker(*m.permissionPicker)
	}
	panelRows := strings.Count(panel, "\n") + 1
	footer := tuiFooter(m.status.Model, m.width, *m.permissionMode)
	footerRows := strings.Count(footer, "\n") + 1
	availableRows := maxTUI(1, m.height-panelRows-footerRows-2)
	renderWidth := inlineRenderWidth(m.width)
	wrappedContent := wrapTUIText(content, renderWidth)
	contentRows := strings.Count(wrappedContent, "\n") + 1
	m.viewport.SetContent(styleTranscript(wrappedContent))
	m.viewport.Width = renderWidth
	m.viewport.Height = minTUI(availableRows, maxTUI(1, contentRows))
	if m.followBottom {
		m.viewport.GotoBottom()
		m.followBottom = false
	}
	var b strings.Builder
	b.WriteString(m.viewport.View())
	b.WriteString("\n" + tuiRule(m.width) + "\n" + panel + "\n" + tuiRule(m.width) + "\n" + footer)
	return b.String()
}

var (
	tuiAccent    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	tuiUser      = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	tuiAssistant = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
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
	role := "plain"
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "❯ "):
			role = "user"
			lines[i] = tuiAccent.Render("❯") + " " + tuiUser.Render(strings.TrimPrefix(line, "❯ "))
		case strings.HasPrefix(line, "● "):
			role = "assistant"
			lines[i] = tuiAI.Render("●") + " " + tuiAssistant.Render(strings.TrimPrefix(line, "● "))
		case strings.HasPrefix(line, "✓ "):
			role = "plain"
			lines[i] = tuiOK.Render("✓") + " " + tuiMuted.Render(strings.TrimPrefix(line, "✓ "))
		case strings.HasPrefix(line, "✖ "):
			role = "plain"
			lines[i] = tuiError.Render("✖") + " " + tuiError.Render(strings.TrimPrefix(line, "✖ "))
		case strings.HasPrefix(line, "Done -"):
			role = "plain"
			lines[i] = tuiMuted.Render(line)
		case strings.HasPrefix(line, "Session ID:") || strings.HasPrefix(line, "注意：") || strings.HasPrefix(line, "已禁用完整会话"):
			role = "plain"
			lines[i] = tuiMuted.Render(line)
		default:
			if role == "assistant" {
				lines[i] = tuiAssistant.Render(line)
			} else if role == "user" {
				lines[i] = tuiUser.Render(line)
			} else {
				lines[i] = tuiUser.Render(line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func tuiRule(width int) string {
	return tuiRuleStyle.Render(strings.Repeat("─", inlineRenderWidth(width)))
}

// inlineRenderWidth leaves the terminal's last column unused. A line that
// reaches the last column can wrap physically in the main screen buffer;
// after a Windows resize Bubble Tea cannot reliably erase that extra row.
func inlineRenderWidth(width int) int {
	return maxTUI(1, width-1)
}

func tuiFooter(model string, width int, modes ...permissionMode) string {
	mode := permissionModeDefault
	if len(modes) > 0 && modes[0] != "" {
		mode = modes[0]
	}
	left := "  权限：" + string(mode)
	if model == "" {
		return tuiMuted.Render(left)
	}
	if lipgloss.Width(left)+lipgloss.Width(model)+2 <= width {
		return tuiMuted.Render(left) + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(model)-1) + tuiMuted.Render(model)
	}
	return tuiMuted.Render(left) + "\n" + strings.Repeat(" ", maxTUI(1, width-lipgloss.Width(model)-1)) + tuiMuted.Render(model)
}

func renderTUIPermissionPicker(p permissionPicker) string {
	var b strings.Builder
	b.WriteString(tuiAccent.Bold(true).Render("Permission mode") + "\n" + tuiMuted.Render("↑/↓ 选择 · Enter 确认 · Esc 取消") + "\n\n")
	for i, mode := range p.options {
		prefix := "  "
		if i == p.cursor {
			prefix = tuiAccent.Render("❯") + " "
		}
		name := string(mode)
		if i == p.cursor {
			name = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("236")).Render(name)
		} else {
			name = tuiUser.Render(name)
		}
		b.WriteString(prefix + name + "  " + tuiMuted.Render(permissionModeDescription(mode)) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func permissionModeDescription(mode permissionMode) string {
	switch mode {
	case permissionModeDefault:
		return "写入、编辑、删除和命令需要确认"
	case permissionModeAcceptEdits:
		return "自动允许写入和编辑，删除和命令仍确认"
	case permissionModePlan:
		return "只读计划模式，拒绝所有变更和命令"
	case permissionModeBypass:
		return "跳过普通审批，但不越过安全边界"
	default:
		return "未知模式"
	}
}

func wrapTUIText(content string, width int) string {
	width = maxTUI(1, width)
	lines := strings.Split(content, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		var b strings.Builder
		lineWidth := 0
		for _, r := range line {
			chunk := string(r)
			runeWidth := lipgloss.Width(chunk)
			if lineWidth > 0 && lineWidth+runeWidth > width {
				wrapped = append(wrapped, b.String())
				b.Reset()
				lineWidth = 0
			}
			b.WriteString(chunk)
			lineWidth += runeWidth
		}
		wrapped = append(wrapped, b.String())
	}
	return strings.Join(wrapped, "\n")
}

func isTTYViewportKey(key tea.KeyMsg) bool {
	switch key.Type {
	case tea.KeyPgUp, tea.KeyPgDown:
		return true
	case tea.KeyCtrlU, tea.KeyCtrlD:
		return true
	case tea.KeyHome, tea.KeyEnd, tea.KeyCtrlHome, tea.KeyCtrlEnd:
		return true
	default:
		return false
	}
}

func resizeTuiFullScreenTextarea(m *tuiFullScreen) {
	width := maxTUI(1, m.width-3)
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
	if a.request.ToolName == "EditFile" {
		title = "EditFile command"
	}
	if a.request.ToolName == "DeleteFile" {
		title = "DeleteFile command"
	}
	detail := a.request.Path
	if a.request.ToolName == "Bash" {
		title = "RunCommand command"
		detail = a.request.Command + "\n\n  cwd: " + a.request.CWD
	}
	opts := []string{"1. Yes", "2. Yes, and don't ask again for this pattern", "3. No"}
	var b strings.Builder
	b.WriteString(tuiAccent.Bold(true).Render(title) + "\n\n  " + tuiUser.Render(detail) + "\n\n  " + tuiMuted.Render("This command requires approval") + "\n\n")
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

func minTUI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
