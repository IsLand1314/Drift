package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/changes"
	"github.com/IsLand1314/Drift/internal/config"
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
	Registry       tool.Registry
	PermissionMode permissionMode
	SandboxMode    tool.SandboxMode
	Hooks          []config.Hook
}

type chatPersistence struct {
	store      *conversation.Store
	snapshot   conversation.Snapshot
	persistent bool
	usage      usageTotals
	registry   tool.Registry
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
	if tasks, ok := persistence.registry.(tool.TaskRegistry); ok {
		if err := tasks.RestoreTasks(target.Tasks); err != nil {
			return fmt.Errorf("chat: restore tasks: %w", err)
		}
	}
	if plans, ok := persistence.registry.(tool.PlanRegistry); ok {
		if err := plans.RestorePlan(target.Plan); err != nil {
			return fmt.Errorf("chat: restore plan: %w", err)
		}
	}
	runner.RestoreMessages(target.Messages)
	runner.RestorePlanState(target.PlanID, target.PlanPhase)
	persistence.snapshot = target
	persistence.usage = usage
	return nil
}

func startNewChatSession(runner *agent.Runner, persistence *chatPersistence) error {
	if persistence == nil || !persistence.persistent {
		runner.ResetContext()
		if persistence != nil {
			if plans, ok := persistence.registry.(tool.PlanRegistry); ok {
				plans.ResetPlan()
			}
		}
		runner.RestorePlanState("", "")
		return nil
	}
	target, err := persistence.store.Create(persistence.snapshot.Focus)
	if err != nil {
		return err
	}
	runner.ResetContext()
	persistence.snapshot = target
	persistence.usage = usageTotals{}
	if tasks, ok := persistence.registry.(tool.TaskRegistry); ok {
		tasks.ResetTasks()
	}
	if plans, ok := persistence.registry.(tool.PlanRegistry); ok {
		plans.ResetPlan()
	}
	runner.RestorePlanState("", "")
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
	if tasks, ok := p.registry.(tool.TaskRegistry); ok {
		p.snapshot.Tasks = tasks.ExportTasks()
	}
	if plans, ok := p.registry.(tool.PlanRegistry); ok {
		p.snapshot.Plan = plans.ExportPlan()
	}
	p.snapshot.PlanID, p.snapshot.PlanPhase = runner.PlanState()
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
	cleared.Tasks = nil
	cleared.PlanID, cleared.PlanPhase, cleared.Plan = "", "", tool.Plan{}
	cleared.UpdatedAt = time.Now().UTC()
	if err := p.store.Save(cleared); err != nil {
		return err
	}
	p.snapshot = cleared
	if tasks, ok := p.registry.(tool.TaskRegistry); ok {
		tasks.ResetTasks()
	}
	if plans, ok := p.registry.(tool.PlanRegistry); ok {
		plans.ResetPlan()
	}
	runner.ResetContext()
	return nil
}

func runChatLoopWithPersistence(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, persistence *chatPersistence, status chatStatus, interrupt *interruptCoordinator, in io.Reader, out, stderr io.Writer) int {
	return runTuiMainScreenLoop(ctx, runner, audit, traceSink, persistence, status, interrupt, in, out, stderr)
}

// runTuiMainScreenLoop writes completed chat output directly to the terminal's
// main buffer. It deliberately owns no transcript viewport or scroll region.
func runTuiMainScreenLoop(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, persistence *chatPersistence, status chatStatus, interrupt *interruptCoordinator, in io.Reader, out, stderr io.Writer) int {
	permissionMemory := newPermissionMemory()
	permissionMode := status.PermissionMode
	if permissionMode == "" {
		permissionMode = permissionModeDefault
	}
	input := newTuiMainScreenInput(in, out, status.Model, permissionMode)
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
		runner.SetQuestionPrompt(func(promptCtx context.Context, question tool.Question) (tool.QuestionAnswer, error) {
			if currentActivity != nil {
				currentActivity.Stop()
			}
			defer func() {
				if currentActivity != nil {
					currentActivity.Start()
				}
			}()
			return askUserQuestion(promptCtx, input, out, question)
		})
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
		runner.SetPlanModeHooks(agent.PlanModeHooks{
			Enter: func() error {
				permissionMode = permissionModePlan
				if tui, ok := input.(*tuiMainScreenInput); ok {
					tui.permissionMode = permissionModePlan
				}
				return nil
			},
			Exit: func() error {
				if permissionMode != permissionModePlan {
					return fmt.Errorf("not currently in plan mode")
				}
				permissionMode = permissionModeDefault
				if tui, ok := input.(*tuiMainScreenInput); ok {
					tui.permissionMode = permissionModeDefault
				}
				return nil
			},
		})
		hookSpecs := make([]agent.HookSpec, 0, len(status.Hooks))
		for _, hook := range status.Hooks {
			hookSpecs = append(hookSpecs, agent.HookSpec{ID: hook.ID, Event: hook.Event, Tool: hook.Tool, Match: hook.Match, Command: hook.Command, TimeoutMS: hook.TimeoutMS, OnError: hook.OnError})
		}
		runner.SetHooks(hookSpecs, func(hookCtx context.Context, hook agent.HookSpec, event agent.Event) (bool, error) {
			if hook.Match != "" {
				value := event.Path
				if value == "" {
					value = event.ToolName
				}
				matched, matchErr := filepath.Match(hook.Match, filepath.ToSlash(value))
				if matchErr != nil || !matched {
					return true, nil
				}
			}
			raw, err := json.Marshal(map[string]any{"command": hook.Command, "cwd": ".", "timeout_ms": hook.TimeoutMS, "max_output_bytes": tool.MaxCommandOutputBytes})
			if err != nil {
				return false, err
			}
			preview, err := tool.RunCommandPreviewWithSandbox(status.Workspace, string(raw), status.SandboxMode)
			if err != nil {
				return false, err
			}
			request := agent.PermissionRequest{ToolName: "Hook:" + hook.ID, Operation: "hook_command", Command: hook.Command, CWD: status.Workspace}
			decision, err := confirmWrite(hookCtx, input, out, permissionMode, permissionMemory, permissionPolicyStore, request)
			if err != nil || !decision.Allow {
				return false, err
			}
			hookCall := agent.Event{Type: agent.EventToolCall, ToolName: "Hook:" + hook.ID, Operation: "hook_command", Command: hook.Command, CWD: status.Workspace, SandboxMode: string(preview.SandboxMode), SandboxBackend: preview.Sandbox.Backend, SandboxAvailable: preview.Sandbox.Available, SandboxProbe: preview.Sandbox.Probe}
			if err := appendChatEvent(audit, traceSink, hookCall); err != nil {
				return false, err
			}
			result, execErr := tool.ExecuteCommand(hookCtx, status.Workspace, preview)
			statusText := "failed"
			if strings.Contains(result, "run_command status=success ") {
				statusText = "success"
			}
			hookResult := agent.Event{Type: agent.EventToolResult, ToolName: "Hook:" + hook.ID, Operation: "hook_command", Command: hook.Command, CWD: status.Workspace, Result: result, ExecutionStatus: statusText}
			if execErr != nil {
				hookResult.ErrorSummary = execErr.Error()
				hookResult.FailureReason = execErr.Error()
			}
			if auditErr := appendChatEvent(audit, traceSink, hookResult); execErr == nil && auditErr != nil {
				execErr = auditErr
			}
			return execErr == nil, execErr
		})
		if err := runner.EmitHookEvent(ctx, "session_start", agent.Event{Type: agent.EventRunStarted}); err != nil {
			fmt.Fprintln(stderr, "错误：session_start hook：", err)
			return 1
		}
		defer func() {
			if err := runner.EmitHookEvent(context.Background(), "session_end", agent.Event{Type: agent.EventRunFinished}); err != nil {
				fmt.Fprintln(stderr, "错误：session_end hook：", err)
			}
		}()
	}
	var mcpManager *mcpManager
	if status.Registry != nil && status.Workspace != "" {
		manager, managerErr := newMCPManager(status.Workspace, status.Registry)
		if managerErr != nil {
			fmt.Fprintln(out, "⚠ MCP 配置加载失败：", managerErr)
		} else {
			mcpManager = manager
			mcpManager.SetAudit(audit)
			defer mcpManager.Close()
		}
	}
	interactiveInput := false
	if _, ok := input.(*tuiMainScreenInput); ok {
		interactiveInput = true
		for _, line := range tuiSessionHeader(persistence) {
			if _, err := fmt.Fprintln(out, line); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
		}
		if persistence != nil && persistence.persistent {
			for _, line := range transcriptFromLLMMessages(persistence.snapshot.Messages) {
				if _, err := fmt.Fprintln(out, line); err != nil {
					fmt.Fprintln(stderr, "错误：", err)
					return 1
				}
			}
		}
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
		if mcpManager != nil {
			if message, handled := handleMCPCommand(ctx, prompt, mcpManager); handled {
				fmt.Fprintln(out, message)
				continue
			}
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
		if prompt == "/permissions" && interactiveInput {
			mode, selected, pickerErr := readPermissionModeChoice(ctx, input, permissionMode)
			if pickerErr != nil {
				fmt.Fprintln(stderr, "错误：权限模式选择器失败：", pickerErr)
				continue
			}
			if selected {
				permissionMode = mode
				if tui, ok := input.(*tuiMainScreenInput); ok {
					tui.permissionMode = mode
				}
				fmt.Fprintln(out, chatMuted(out)+"权限模式已切换为 "+string(mode)+chatReset(out))
			}
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
		if prompt == "/search" || strings.HasPrefix(prompt, "/search ") {
			query := strings.TrimSpace(strings.TrimPrefix(prompt, "/search"))
			if persistence == nil || !persistence.persistent {
				fmt.Fprintln(out, "当前为 --no-session 模式，无法检索历史会话")
				continue
			}
			if query == "" {
				fmt.Fprintln(out, "用法：/search <关键词>")
				continue
			}
			results, searchErr := persistence.store.Search(query, 20)
			if searchErr != nil {
				fmt.Fprintln(out, "历史检索失败：", searchErr)
				continue
			}
			if len(results) == 0 {
				fmt.Fprintln(out, "没有匹配的历史会话")
				continue
			}
			for _, result := range results {
				writeConversationSearchResult(out, result)
			}
			continue
		}
		if runner.NeedsCompaction(len(prompt)) && len(runner.Messages()) >= 2 {
			result, compactErr := compactChatContext(ctx, runner, audit, traceSink, persistence)
			if compactErr != nil {
				fmt.Fprintln(stderr, "错误：上下文自动压缩失败，本轮未发送：", compactErr)
				continue
			}
			fmt.Fprintf(out, "%s上下文已自动压缩：保留最近 %d 条消息%s\n", chatMuted(out), len(result.KeptMessages), chatReset(out))
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
		if interactiveInput {
			if _, err := fmt.Fprintln(out, chatUserPromptLine(out, prompt)); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
			currentActivity = newChatActivity(out)
			currentActivity.Start()
		}
		toolStarted := make(map[string]toolProgress)
		if err := runner.EmitHookEvent(turnCtx, "turn_start", agent.Event{Type: agent.EventRunStarted, Text: prompt}); err != nil {
			endTurn()
			turnCancel()
			fmt.Fprintln(out, chatError(out)+"✖ turn_start hook："+err.Error()+chatReset(out))
			continue
		}
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
		turnStatus := "complete"
		turnFailure := ""
		if err != nil {
			turnStatus = "failed"
			turnFailure = err.Error()
		}
		turnEndErr := runner.EmitHookEvent(turnCtx, "turn_end", agent.Event{Type: agent.EventRunFinished, ExecutionStatus: turnStatus, FailureReason: turnFailure})
		if err == nil && turnEndErr != nil {
			err = turnEndErr
		}
		if currentActivity != nil {
			currentActivity.Stop()
			currentActivity = nil
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
			if interactiveInput {
				fmt.Fprintln(out, chatError(out)+"✖ "+err.Error()+chatReset(out))
				continue
			}
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

func compactChatContext(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, persistence *chatPersistence) (agent.CompactResult, error) {
	oldMessages := runner.Messages()
	beforeBytes := runner.ContextBytes()
	if err := appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventCompactionStarted, BeforeBytes: beforeBytes, MessageCount: len(oldMessages)}); err != nil {
		return agent.CompactResult{}, err
	}
	result, err := runner.Compact(ctx)
	if err != nil {
		stage := llm.ErrorStageOf(err)
		if stage == "" {
			stage = "agent_compaction"
		}
		_ = appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventCompactionError, Error: err.Error(), Stage: stage, BeforeBytes: beforeBytes, MessageCount: len(oldMessages)})
		return agent.CompactResult{}, err
	}
	oldUsage := usageTotals{}
	if persistence != nil {
		oldUsage = persistence.usage
		if result.Usage != nil {
			persistence.usage.InputTokens += result.Usage.InputTokens
			persistence.usage.OutputTokens += result.Usage.OutputTokens
			persistence.usage.ReportedRequests++
		} else {
			persistence.usage.UnreportedRequests++
		}
	}
	if persistence != nil && persistence.persistent {
		if err := persistence.saveRunner(runner); err != nil {
			runner.RestoreMessages(oldMessages)
			persistence.usage = oldUsage
			_ = appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventCompactionError, Error: err.Error(), Stage: "conversation_save", BeforeBytes: beforeBytes, MessageCount: len(oldMessages)})
			return agent.CompactResult{}, err
		}
	}
	usageEvent := agent.Event{Type: agent.EventModelUsage, UsageAvailable: result.Usage != nil}
	if result.Usage != nil {
		usageEvent.InputTokens = result.Usage.InputTokens
		usageEvent.OutputTokens = result.Usage.OutputTokens
		usageEvent.TotalTokens = result.Usage.TotalTokens
	}
	if err := appendChatEvent(audit, traceSink, usageEvent); err != nil {
		return agent.CompactResult{}, err
	}
	if err := appendChatEvent(audit, traceSink, agent.Event{Type: agent.EventCompactionFinished, BeforeBytes: result.BeforeBytes, AfterBytes: result.AfterBytes, MessageCount: len(oldMessages), KeptMessages: len(result.KeptMessages)}); err != nil {
		return agent.CompactResult{}, err
	}
	return result, nil
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
	if event.ToolName == "WriteFile" && event.NewBytes > 0 {
		bytes = event.NewBytes
	}
	return chatSuccess(out) + "✓ " + toolLabel(event.ToolName) + formatToolPath(path) + " · " + formatToolBytes(bytes) + " · " + formatDuration(elapsed) + chatReset(out)
}

func shouldRenderToolResult(event agent.Event) bool {
	// A read-before-create miss is normal discovery work. The model still receives
	// its redacted failure, but the user only sees the eventual write result.
	return !(event.ToolName == "ReadFile" && event.ErrorSummary == "tool execution failed")
}

func formatToolPath(path string) string {
	if path == "" {
		return ""
	}
	return " " + path
}

func toolLabel(name string) string {
	switch strings.TrimSpace(name) {
	case "ReadFile":
		return "Read"
	case "Glob":
		return "Glob"
	case "Grep":
		return "Grep"
	case "WriteFile":
		return "Write"
	case "EditFile":
		return "Edit"
	case "DeleteFile":
		return "Delete"
	case "Bash":
		return "Bash"
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

func resolvePermission(mode permissionMode, memory *permissionMemory, policy *permissionPolicy, request agent.PermissionRequest) agent.PermissionDecision {
	decision := decidePermission(mode, request)
	if decision.Policy != agent.PolicyAsk {
		return decision
	}
	if policy != nil && policy.allows(request) {
		return agent.PermissionDecision{Allow: true, Reason: "persistent_pattern_approved", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowPersistent, Source: agent.PermissionSourcePersistent}
	}
	if memory != nil && memory.Allow(request) {
		return agent.PermissionDecision{Allow: true, Reason: "session_pattern_approved", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowOnce, Source: agent.PermissionSourceSession}
	}
	return decision
}

func confirmWrite(ctx context.Context, input chatInput, out io.Writer, mode permissionMode, memory *permissionMemory, policy *permissionPolicy, request agent.PermissionRequest) (agent.PermissionDecision, error) {
	if decision := resolvePermission(mode, memory, policy, request); decision.Policy != agent.PolicyAsk {
		return decision, nil
	}
	if _, ok := input.(*tuiMainScreenInput); ok {
		choice, err := readApprovalChoice(ctx, input, request)
		if err != nil {
			return agent.PermissionDecision{Reason: "approval_cancelled", Policy: agent.PolicyAsk, Approval: agent.ApprovalCancelled, Source: agent.PermissionSourceUser}, err
		}
		switch choice {
		case approveOnce:
			return agent.PermissionDecision{Allow: true, Reason: "user_approved", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowOnce, Source: agent.PermissionSourceUser}, nil
		case approvePattern:
			return agent.PermissionDecision{Allow: true, Reason: "persistent_pattern_pending", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowPersistent, Source: agent.PermissionSourceUser}, nil
		default:
			return agent.PermissionDecision{Reason: "user_denied", Policy: agent.PolicyAsk, Approval: agent.ApprovalDeny, Source: agent.PermissionSourceUser}, nil
		}
	}
	if request.ToolName == "Bash" {
		fmt.Fprintf(out, "\nRun command: %s (cwd %s)\n", request.Command, request.CWD)
	} else {
		fmt.Fprintf(out, "\nWrite request: %s %s (%d -> %d bytes)\n", request.Operation, request.Path, request.OldBytes, request.NewBytes)
	}
	fmt.Fprint(out, "Allow this change? [y/N] ")
	choice, err := readApprovalChoice(ctx, input, request)
	if err != nil {
		return agent.PermissionDecision{Reason: "confirmation input unavailable", Policy: agent.PolicyAsk, Approval: agent.ApprovalCancelled, Source: agent.PermissionSourceUser}, err
	}
	if choice == approveOnce {
		return agent.PermissionDecision{Allow: true, Reason: "user_approved", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowOnce, Source: agent.PermissionSourceUser}, nil
	}
	if choice == approvePattern {
		return agent.PermissionDecision{Allow: true, Reason: "persistent_pattern_pending", Policy: agent.PolicyAsk, Approval: agent.ApprovalAllowPersistent, Source: agent.PermissionSourceUser}, nil
	}
	return agent.PermissionDecision{Reason: "user_denied", Policy: agent.PolicyAsk, Approval: agent.ApprovalDeny, Source: agent.PermissionSourceUser}, nil
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
	if name != "Bash" {
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

func chatUserPromptLine(out io.Writer, prompt string) string {
	prefix := chatPrompt(out)
	return prefix + strings.ReplaceAll(prompt, "\n", "\n"+prefix)
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
