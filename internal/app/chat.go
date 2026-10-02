package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/changes"
	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

func runChatLoop(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, in io.Reader, out, stderr io.Writer) int {
	return runChatLoopWithPersistence(ctx, runner, audit, traceSink, nil, chatStatus{}, nil, in, out, stderr)
}

type chatStatus struct {
	Model          string
	Workspace      string
	ToolCount      int
	PermissionMode permissionMode
	SandboxMode    tool.SandboxMode
}

type chatPersistence struct {
	store      *conversation.Store
	snapshot   conversation.Snapshot
	persistent bool
	usage      usageTotals
}

type usageTotals struct {
	InputTokens, OutputTokens            int
	ReportedRequests, UnreportedRequests int
}

func switchChatSession(runner *agent.Runner, persistence *chatPersistence, targetID string) error {
	if persistence == nil || !persistence.persistent {
		return errors.New("chat: session switching requires persistent mode")
	}
	target, err := persistence.store.Load(strings.TrimSpace(targetID))
	if err != nil {
		return err
	}
	usage := usageTotals{
		InputTokens:        target.InputTokens,
		OutputTokens:       target.OutputTokens,
		ReportedRequests:   target.ReportedRequests,
		UnreportedRequests: target.UnreportedRequests,
	}
	runner.RestoreMessages(target.Messages)
	persistence.snapshot = target
	persistence.usage = usage
	return nil
}

func startNewChatSession(runner *agent.Runner, persistence *chatPersistence) error {
	if persistence == nil || !persistence.persistent {
		runner.ResetContext()
		return nil
	}
	target, err := persistence.store.Create(persistence.snapshot.Focus)
	if err != nil {
		return err
	}
	runner.ResetContext()
	persistence.snapshot = target
	persistence.usage = usageTotals{}
	return nil
}

func renameChatSession(persistence *chatPersistence, title string) error {
	if persistence == nil || !persistence.persistent {
		return errors.New("chat: session naming requires persistent mode")
	}
	updated := persistence.snapshot
	updated.Title = strings.TrimSpace(title)
	updated.UpdatedAt = time.Now().UTC()
	if err := persistence.store.Save(updated); err != nil {
		return err
	}
	persistence.snapshot = updated
	return nil
}

func (u *usageTotals) add(event agent.Event) {
	if event.Type != agent.EventModelUsage {
		return
	}
	if event.UsageAvailable {
		u.InputTokens += event.InputTokens
		u.OutputTokens += event.OutputTokens
		u.ReportedRequests++
	} else {
		u.UnreportedRequests++
	}
}

func (p *chatPersistence) saveRunner(runner *agent.Runner) error {
	if p == nil || !p.persistent {
		return nil
	}
	p.snapshot.Messages = runner.Messages()
	p.snapshot.ContextBytes = runner.ContextBytes()
	p.snapshot.InputTokens = p.usage.InputTokens
	p.snapshot.OutputTokens = p.usage.OutputTokens
	p.snapshot.ReportedRequests = p.usage.ReportedRequests
	p.snapshot.UnreportedRequests = p.usage.UnreportedRequests
	p.snapshot.UpdatedAt = time.Now().UTC()
	return p.store.Save(p.snapshot)
}

func (p *chatPersistence) clearRunner(runner *agent.Runner) error {
	if p == nil || !p.persistent {
		runner.ResetContext()
		return nil
	}
	cleared := p.snapshot
	cleared.Messages = nil
	cleared.ContextBytes = 0
	cleared.InputTokens, cleared.OutputTokens = 0, 0
	cleared.ReportedRequests, cleared.UnreportedRequests = 0, 0
	cleared.UpdatedAt = time.Now().UTC()
	if err := p.store.Save(cleared); err != nil {
		return err
	}
	p.snapshot = cleared
	runner.ResetContext()
	return nil
}

func runChatLoopWithPersistence(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, persistence *chatPersistence, status chatStatus, interrupt *interruptCoordinator, in io.Reader, out, stderr io.Writer) int {
	input := newChatInput(in, out, status.Model)
	if _, ok := input.(*ttyChatInput); ok {
		return runTTYChatLoop(ctx, runner, audit, traceSink, persistence, status, interrupt, in, out)
	}
	permissionMemory := newPermissionMemory()
	permissionMode := status.PermissionMode
	if permissionMode == "" {
		permissionMode = permissionModeDefault
	}
	pendingPermissions := make(map[string]agent.PermissionRequest)
	var permissionPolicyStore *permissionPolicy
	if status.Workspace != "" {
		var policyErr error
		permissionPolicyStore, policyErr = loadPermissionPolicy(status.Workspace)
		if policyErr != nil {
			fmt.Fprintln(out, "⚠ 权限策略加载失败，已恢复为每次询问")
			permissionPolicyStore = newPermissionPolicy(status.Workspace)
		}
	}
	var currentActivity *chatActivity
	if runner != nil {
		runner.SetPermissionPrompt(func(promptCtx context.Context, request agent.PermissionRequest) (agent.PermissionDecision, error) {
			if currentActivity != nil {
				currentActivity.Stop()
			}
			defer func() {
				if currentActivity != nil {
					currentActivity.Start()
				}
			}()
			decision, decisionErr := confirmWrite(promptCtx, input, out, permissionMode, permissionMemory, permissionPolicyStore, request)
			if decisionErr == nil && decision.Reason == "persistent_pattern_pending" {
				pendingPermissions[permissionRuleKey(permissionRuleFromRequest(request))] = request
			}
			return decision, decisionErr
		})
	}
	interactiveInput := false
	if _, ok := input.(*ttyChatInput); ok {
		interactiveInput = true
	}
	for {
		// TTY 输入组件自己负责分隔线、占位符和光标；纯文本路径保留原有提示。
		if !interactiveInput {
			if separator := chatSeparator(out); separator != "" {
				if _, err := fmt.Fprintln(out, separator); err != nil {
					fmt.Fprintln(stderr, "错误：", err)
					return 1
				}
			}
			if _, err := fmt.Fprint(out, chatPrompt(out)); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
		}
		prompt, err := input.Read(ctx)
		if err != nil {
			if errors.Is(err, errChatInputCancelled) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return 130
			}
			if errors.Is(err, io.EOF) {
				return 0
			}
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		prompt = strings.TrimSpace(strings.TrimSuffix(prompt, "\r"))
		if prompt == "" {
			continue
		}
		if prompt == "exit" || prompt == "/exit" || prompt == "quit" {
			return 0
		}
		if prompt == "clear" {
			fmt.Fprintln(out, "如需清空上下文，请输入 /clear")
			continue
		}
		if prompt == "status" {
			fmt.Fprintln(out, "如需查看状态，请输入 /status")
			continue
		}
		if prompt == "compact" {
			fmt.Fprintln(out, "如需压缩上下文，请输入 /compact")
			continue
		}
		if mode, handled, modeErr := parsePermissionModeCommand(prompt); handled {
			if modeErr != nil {
				fmt.Fprintln(out, "✖", modeErr)
				continue
			}
			if mode != "" {
				permissionMode = mode
				fmt.Fprintln(out, "已切换权限模式：", mode)
			} else {
				fmt.Fprintln(out, "当前权限模式：", permissionMode)
			}
			continue
		}
		if message, handled := handlePermissionCommand(permissionPolicyStore, prompt); handled {
			fmt.Fprintln(out, message)
			continue
		}
		if prompt == "/resume" || strings.HasPrefix(prompt, "/resume ") {
			if persistence == nil || !persistence.persistent {
				fmt.Fprintln(out, "当前为 --no-session 模式，无法恢复持久会话")
				continue
			}
			targetID := strings.TrimSpace(strings.TrimPrefix(prompt, "/resume"))
			if targetID == "" {
				if !interactiveInput {
					fmt.Fprintln(out, "请在交互终端使用 /resume，或输入 /resume <id>")
					continue
				}
				items, listErr := persistence.store.List()
				if listErr != nil {
					fmt.Fprintln(stderr, "错误：无法列出会话：", listErr)
					continue
				}
				selectedID, selected, pickerErr := runChatResumePicker(ctx, in, out, items)
				if pickerErr != nil {
					fmt.Fprintln(stderr, "错误：会话选择器失败：", pickerErr)
					continue
				}
				if !selected {
					continue
				}
				targetID = selectedID
			}
			if err := switchChatSession(runner, persistence, targetID); err != nil {
				fmt.Fprintln(out, "无法恢复会话：", err)
				continue
			}
			fmt.Fprintln(out, "已切换会话：", persistence.snapshot.ID)
			continue
		}
		if prompt == "/new" {
			if err := startNewChatSession(runner, persistence); err != nil {
				fmt.Fprintln(out, "无法创建新会话：", err)
				continue
			}
			if persistence != nil && persistence.persistent {
				fmt.Fprintln(out, "已创建新会话：", persistence.snapshot.ID)
			} else {
				fmt.Fprintln(out, "已创建新的临时会话")
			}
			continue
		}
		if prompt == "/rename" || strings.HasPrefix(prompt, "/rename ") {
			title := strings.TrimSpace(strings.TrimPrefix(prompt, "/rename"))
			if title == "" {
				fmt.Fprintln(out, "用法：/rename <标题>")
				continue
			}
			if err := renameChatSession(persistence, title); err != nil {
				fmt.Fprintln(out, "无法更新会话名称：", err)
				continue
			}
			fmt.Fprintln(out, "已更新会话名称：", persistence.snapshot.Title)
			continue
		}
		if prompt == "/status" {
			writeChatStatus(out, runner, persistence, status)
			continue
		}
		if prompt == "/compact" {
			oldMessages := runner.Messages()
			oldSnapshot := conversation.Snapshot{}
			if persistence != nil && persistence.persistent {
				oldSnapshot = persistence.snapshot
				oldSnapshot.Messages = append([]llm.Message(nil), oldMessages...)
			}
			beforeBytes := runner.ContextBytes()
			startEvent := agent.Event{Type: agent.EventCompactionStarted, BeforeBytes: beforeBytes, MessageCount: len(oldMessages)}
			if err := appendChatEvent(audit, traceSink, startEvent); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
			result, err := runner.Compact(ctx)
			if err != nil {
				if errors.Is(err, agent.ErrCompactionInsufficient) {
					fmt.Fprintln(out, "当前上下文消息不足，无需压缩")
					continue
				}
				stage := llm.ErrorStageOf(err)
				if stage == "" {
					stage = "agent_compaction"
				}
				_ = appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventCompactionError, Error: err.Error(), Stage: stage, BeforeBytes: beforeBytes, MessageCount: len(oldMessages)})
				fmt.Fprintln(stderr, "错误：压缩失败，当前上下文保持不变")
				continue
			}
			usageEvent := agent.Event{Type: agent.EventModelUsage, UsageAvailable: result.Usage != nil}
			if result.Usage != nil {
				usageEvent.InputTokens = result.Usage.InputTokens
				usageEvent.OutputTokens = result.Usage.OutputTokens
				usageEvent.TotalTokens = result.Usage.TotalTokens
			}
			oldUsage := usageTotals{}
			if persistence != nil {
				oldUsage = persistence.usage
				persistence.usage.add(usageEvent)
			}
			if persistence != nil && persistence.persistent {
				if err := persistence.saveRunner(runner); err != nil {
					runner.RestoreMessages(oldMessages)
					persistence.snapshot = oldSnapshot
					persistence.usage = oldUsage
					_ = appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventCompactionError, Error: err.Error(), Stage: "conversation_save", BeforeBytes: beforeBytes, MessageCount: len(oldMessages)})
					fmt.Fprintln(stderr, "错误：会话保存失败，压缩结果未应用")
					continue
				}
			}
			if err := appendChatEvent(audit, traceSink, usageEvent); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
			if err := appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventCompactionFinished, BeforeBytes: result.BeforeBytes, AfterBytes: result.AfterBytes, MessageCount: len(oldMessages), KeptMessages: len(result.KeptMessages)}); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
			fmt.Fprintf(out, "上下文已压缩：保留最近 %d 条消息\n", len(result.KeptMessages))
			continue
		}
		if prompt == "/clear" {
			if persistence != nil {
				if err := persistence.clearRunner(runner); err != nil {
					fmt.Fprintln(stderr, "错误：会话保存失败，未清空当前对话上下文")
					continue
				}
			} else {
				runner.ResetContext()
			}
			if _, err := fmt.Fprintln(out, "已清空当前对话上下文"); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
			continue
		}
		startedAt := time.Now()
		var lastText string
		wroteAssistantPrefix := false
		turnCtx, turnCancel := context.WithCancel(ctx)
		var changeSet *changes.ChangeSet
		if status.ToolCount > 3 {
			changeSet, err = changes.Begin(status.Workspace, startedAt)
			if err != nil {
				turnCancel()
				fmt.Fprintln(stderr, "错误：无法创建变更记录：", err)
				continue
			}
			turnCtx = changes.WithChangeSet(turnCtx, changeSet)
		}
		endTurn := interrupt.beginTurn(turnCancel)
		usageBefore := usageTotals{}
		if persistence != nil {
			usageBefore = persistence.usage
		}
		var fixedFooter *fixedChatFooter
		if interactiveInput {
			fixedFooter = newFixedChatFooter(out, input.(*ttyChatInput))
			fixedFooter.Begin()
		}
		if interactiveInput {
			currentActivity = newChatActivity(out)
			currentActivity.Start()
		}
		toolStarted := make(map[string]toolProgress)
		err = runner.RunEvents(turnCtx, prompt, func(event agent.Event) error {
			// 同一事件先写脱敏审计，再按需转发 trace 和 stdout。
			if err := audit.Append(event); err != nil {
				return err
			}
			if traceSink != nil {
				_ = traceSink(event)
			}
			if persistence != nil {
				persistence.usage.add(event)
			}
			if event.Type == agent.EventToolResult {
				key := permissionRuleKey(permissionRuleFromRequest(agent.PermissionRequest{ToolName: event.ToolName, Operation: event.Operation, Path: event.Path, Command: event.Command, CWD: event.CWD}))
				if request, ok := pendingPermissions[key]; ok {
					delete(pendingPermissions, key)
					if event.ErrorSummary == "" && permissionPolicyStore != nil {
						if persistErr := permissionPolicyStore.remember(request); persistErr != nil {
							fmt.Fprintln(out, "⚠ 本次已允许，但权限策略持久化失败")
						}
					}
				}
			}
			if interactiveInput {
				switch event.Type {
				case agent.EventToolCall:
					currentActivity.Stop()
					toolStarted[event.ToolCallID] = toolProgress{started: time.Now(), path: safeToolPathForTool(event.ToolName, event.Arguments)}
					currentActivity.Start()
				case agent.EventToolResult:
					currentActivity.Stop()
					progress := toolStarted[event.ToolCallID]
					delete(toolStarted, event.ToolCallID)
					if shouldRenderToolResult(event) {
						if _, err := fmt.Fprintln(out, chatToolResultLine(out, event, progress.path, time.Since(progress.started))); err != nil {
							return err
						}
					}
					currentActivity.Start()
				}
			}
			if event.Type != agent.EventTextDelta {
				return nil
			}
			if interactiveInput {
				currentActivity.Stop()
			}
			if !wroteAssistantPrefix {
				if _, err := fmt.Fprint(out, chatAssistantPrefix(out)); err != nil {
					return err
				}
				wroteAssistantPrefix = true
			}
			lastText = event.Text
			_, err := io.WriteString(out, event.Text)
			return err
		})
		if currentActivity != nil {
			currentActivity.Stop()
			currentActivity = nil
		}
		if fixedFooter != nil {
			fixedFooter.End()
		}
		if changeSet != nil {
			statusText := "complete"
			if err != nil {
				statusText = "partial"
			}
			if finalizeErr := changeSet.Finalize(statusText); finalizeErr != nil && err == nil {
				err = finalizeErr
			}
		}
		endTurn()
		if err != nil {
			if persistence != nil {
				persistence.usage = usageBefore
			}
			if errors.Is(err, context.Canceled) && turnCtx.Err() != nil && ctx.Err() == nil {
				_ = appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventError, Stage: "agent_cancelled"})
				fmt.Fprintln(out, chatCancelMessage(out))
				turnCancel()
				continue
			}
			if errors.Is(err, context.Canceled) {
				turnCancel()
				fmt.Fprintln(stderr, "已取消")
				return 130
			}
			if errors.Is(err, agent.ErrContextLimit) {
				turnCancel()
				fmt.Fprintln(stderr, "错误：对话上下文已达到上限，请输入 /clear 后继续")
				continue
			}
			turnCancel()
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		turnCancel()
		if wroteAssistantPrefix {
			if !strings.HasSuffix(lastText, "\n") {
				if _, err := io.WriteString(out, "\n"); err != nil {
					fmt.Fprintln(stderr, "错误：", err)
					return 1
				}
			}
			if _, err := fmt.Fprintf(out, "%sDone - %.1fs%s\n", chatMuted(out), time.Since(startedAt).Seconds(), chatReset(out)); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
		}
		if persistence != nil && persistence.persistent {
			if err := persistence.saveRunner(runner); err != nil {
				fmt.Fprintln(stderr, "错误：会话保存失败，本次上下文只保留在当前进程")
			}
		}
	}
}

type toolProgress struct {
	started time.Time
	path    string
}

func chatToolResultLine(out io.Writer, event agent.Event, path string, elapsed time.Duration) string {
	if event.ErrorSummary != "" {
		return chatError(out) + "✖ " + toolLabel(event.ToolName) + formatToolPath(path) + " · " + event.ErrorSummary + chatReset(out)
	}
	bytes := len([]byte(event.Result))
	if event.ToolName == "write_file" && event.NewBytes > 0 {
		bytes = event.NewBytes
	}
	return chatSuccess(out) + "✓ " + toolLabel(event.ToolName) + formatToolPath(path) + " · " + formatToolBytes(bytes) + " · " + formatDuration(elapsed) + chatReset(out)
}

func shouldRenderToolResult(event agent.Event) bool {
	// A read-before-create miss is normal discovery work. The model still receives
	// its redacted failure, but the user only sees the eventual write result.
	return !(event.ToolName == "read_file" && event.ErrorSummary == "tool execution failed")
}

func formatToolPath(path string) string {
	if path == "" {
		return ""
	}
	return " " + path
}

func toolLabel(name string) string {
	switch strings.TrimSpace(name) {
	case "read_file":
		return "Read"
	case "list_files":
		return "List"
	case "search_text":
		return "Search"
	case "write_file":
		return "Write"
	case "run_command":
		return "Run"
	default:
		name = strings.TrimSpace(name)
		if name == "" {
			return "Tool"
		}
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

func formatDuration(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	return fmt.Sprintf("%.1fs", elapsed.Seconds())
}

func permissionAlreadyAllowed(memory *permissionMemory, policy *permissionPolicy, request agent.PermissionRequest) bool {
	return memory.Allow(request) || policy.allows(request)
}

func confirmWrite(ctx context.Context, input chatInput, out io.Writer, mode permissionMode, memory *permissionMemory, policy *permissionPolicy, request agent.PermissionRequest) (agent.PermissionDecision, error) {
	if decision := decidePermission(mode, request); decision.Reason != "approval_required" {
		return decision, nil
	}
	if permissionAlreadyAllowed(memory, policy, request) {
		return agent.PermissionDecision{Allow: true, Reason: "session_pattern_approved"}, nil
	}
	if _, ok := input.(*ttyChatInput); ok {
		choice, err := readApprovalChoice(ctx, input, request)
		if err != nil {
			return agent.PermissionDecision{Reason: "approval_cancelled"}, err
		}
		switch choice {
		case approveOnce:
			return agent.PermissionDecision{Allow: true, Reason: "user_approved"}, nil
		case approvePattern:
			return agent.PermissionDecision{Allow: true, Reason: "persistent_pattern_pending"}, nil
		default:
			return agent.PermissionDecision{Reason: "user_denied"}, nil
		}
	}
	if request.ToolName == "run_command" {
		fmt.Fprintf(out, "\nRun command: %s (cwd %s)\n", request.Command, request.CWD)
	} else {
		fmt.Fprintf(out, "\nWrite request: %s %s (%d -> %d bytes)\n", request.Operation, request.Path, request.OldBytes, request.NewBytes)
	}
	fmt.Fprint(out, "Allow this change? [y/N] ")
	choice, err := readApprovalChoice(ctx, input, request)
	if err != nil {
		return agent.PermissionDecision{Reason: "confirmation input unavailable"}, err
	}
	if choice == approveOnce {
		return agent.PermissionDecision{Allow: true, Reason: "user_approved"}, nil
	}
	if choice == approvePattern {
		return agent.PermissionDecision{Allow: true, Reason: "persistent_pattern_pending"}, nil
	}
	return agent.PermissionDecision{Reason: "user_denied"}, nil
}

func safeToolPath(arguments string) string {
	var input struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil || input.Path == "" || len(input.Path) > 256 {
		return ""
	}
	if strings.HasPrefix(input.Path, "/") || strings.HasPrefix(input.Path, "\\") || strings.Contains(input.Path, ":") {
		return ""
	}
	for _, part := range strings.FieldsFunc(input.Path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." || part == "." {
			return ""
		}
	}
	return input.Path
}

func safeToolPathForTool(name, arguments string) string {
	if name != "run_command" {
		return safeToolPath(arguments)
	}
	var input struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil || strings.TrimSpace(input.Command) == "" || len(input.Command) > 160 {
		return ""
	}
	return strings.TrimSpace(input.Command)
}

func formatToolBytes(size int) string {
	if size < 1000 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f KB", float64(size)/1000)
}

func writeChatStatus(out io.Writer, runner *agent.Runner, persistence *chatPersistence, status chatStatus) {
	conversationID := "temporary (not saved)"
	if persistence != nil && persistence.persistent {
		conversationID = persistence.snapshot.ID
	}
	contextBytes := runner.ContextBytes()
	remaining := agent.MaxConversationBytes - contextBytes
	if remaining < 0 {
		remaining = 0
	}
	remainingPercent := int(float64(remaining)*100/float64(agent.MaxConversationBytes) + 0.5)
	totalKB := float64(agent.MaxConversationBytes) / 1000
	usedKB := float64(contextBytes) / 1000
	fmt.Fprintf(out, "%sDrift Status%s\n%s────────────────────────%s\n\n", chatStatusAccent(out), chatReset(out), chatMuted(out), chatReset(out))
	writeChatStatusRow(out, "Session ID", conversationID, "")
	writeChatStatusRow(out, "Model", status.Model, "")
	writeChatStatusRow(out, "Context", fmt.Sprintf("%d%% remaining", remainingPercent), "")
	fmt.Fprintf(out, "%15s%.1f KB used / %.1f KB total\n", "", usedKB, totalKB)
	usage := usageTotals{}
	if persistence != nil {
		usage = persistence.usage
	}
	tokenText, tokenStyle := "unavailable", chatStatusWarning(out)
	if usage.ReportedRequests > 0 {
		tokenText = fmt.Sprintf("%d in / %d out", usage.InputTokens, usage.OutputTokens)
		if usage.UnreportedRequests > 0 {
			tokenText += " (partial)"
		}
		tokenStyle = ""
	}
	writeChatStatusRow(out, "Tokens", tokenText, tokenStyle)
	writeChatStatusRow(out, "Tools", fmt.Sprintf("%d enabled", status.ToolCount), "")
	mode := status.PermissionMode
	if mode == "" {
		mode = permissionModeDefault
	}
	writeChatStatusRow(out, "Permission", string(mode), "")
	sandboxMode := status.SandboxMode
	if sandboxMode == "" {
		sandboxMode = tool.SandboxOff
	}
	writeChatStatusRow(out, "Sandbox", string(sandboxMode), "")
	writeChatStatusRow(out, "Workspace", status.Workspace, chatStatusAccent(out))
}

func writeChatStatusRow(out io.Writer, label, value, valueStyle string) {
	fmt.Fprintf(out, "  %s%-13s%s%s%s%s\n", chatStatusLabel(out), label+":", chatReset(out), valueStyle, value, chatReset(out))
}

func chatPrompt(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[36m❯ \x1b[0m"
	}
	return "> "
}

func chatSeparator(out io.Writer) string {
	if chatUsesColor(out) {
		return chatMuted(out) + "────────────────────────" + chatReset(out)
	}
	return ""
}

func chatAssistantPrefix(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[35m●\x1b[0m "
	}
	return "● "
}

func chatCancelMessage(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[35m✖\x1b[0m \x1b[31m当前轮已取消；会话仍可继续\x1b[0m"
	}
	return "✖ 当前轮已取消；会话仍可继续"
}

func chatMuted(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[2m"
	}
	return ""
}

func chatStatusAccent(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[36m"
	}
	return ""
}

func chatStatusLabel(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[2m"
	}
	return ""
}

func chatStatusWarning(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[33m"
	}
	return ""
}

func chatSuccess(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[32m"
	}
	return ""
}

func chatError(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[31m"
	}
	return ""
}

func chatReset(out io.Writer) string {
	if chatUsesColor(out) {
		return "\x1b[0m"
	}
	return ""
}

func chatUsesColor(out io.Writer) bool {
	if out != os.Stdout {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func appendChatEvent(audit session.Writer, traceSink agent.EventSink, event agent.Event) error {
	if err := audit.Append(event); err != nil {
		return err
	}
	if traceSink != nil {
		_ = traceSink(event)
	}
	return nil
}
