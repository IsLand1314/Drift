package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/session"
)

func runChatLoop(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, in io.Reader, out, stderr io.Writer) int {
	return runChatLoopWithPersistence(ctx, runner, audit, traceSink, nil, chatStatus{}, nil, in, out, stderr)
}

type chatStatus struct {
	Model     string
	Workspace string
	ToolCount int
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
	input := newChatInput(in, out)
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
		endTurn := interrupt.beginTurn(turnCancel)
		usageBefore := usageTotals{}
		if persistence != nil {
			usageBefore = persistence.usage
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
			if event.Type != agent.EventTextDelta {
				return nil
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
			if _, err := fmt.Fprintf(out, "%s完成 · %.1fs%s\n", chatMuted(out), time.Since(startedAt).Seconds(), chatReset(out)); err != nil {
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
