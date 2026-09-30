package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/session"
)

func runChatLoop(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, in io.Reader, out, stderr io.Writer) int {
	return runChatLoopWithPersistence(ctx, runner, audit, traceSink, nil, in, out, stderr)
}

type chatPersistence struct {
	store      *conversation.Store
	snapshot   conversation.Snapshot
	persistent bool
}

func (p *chatPersistence) saveRunner(runner *agent.Runner) error {
	if p == nil || !p.persistent {
		return nil
	}
	p.snapshot.Messages = runner.Messages()
	p.snapshot.ContextBytes = runner.ContextBytes()
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
	cleared.UpdatedAt = time.Now().UTC()
	if err := p.store.Save(cleared); err != nil {
		return err
	}
	p.snapshot = cleared
	runner.ResetContext()
	return nil
}

func runChatLoopWithPersistence(ctx context.Context, runner *agent.Runner, audit session.Writer, traceSink agent.EventSink, persistence *chatPersistence, in io.Reader, out, stderr io.Writer) int {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for {
		// 每次只读取一行；退出命令不会进入 Agent，也不会产生 Provider 请求。
		if _, err := fmt.Fprint(out, "> "); err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
			return 0
		}
		prompt := strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r"))
		if prompt == "" {
			continue
		}
		if prompt == "exit" || prompt == "/exit" || prompt == "quit" {
			return 0
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
		var lastText string
		err := runner.RunEvents(ctx, prompt, func(event agent.Event) error {
			// 同一事件先写脱敏审计，再按需转发 trace 和 stdout。
			if err := audit.Append(event); err != nil {
				return err
			}
			if traceSink != nil {
				_ = traceSink(event)
			}
			if event.Type != agent.EventTextDelta {
				return nil
			}
			lastText = event.Text
			_, err := io.WriteString(out, event.Text)
			return err
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(stderr, "已取消")
				return 130
			}
			if errors.Is(err, agent.ErrContextLimit) {
				fmt.Fprintln(stderr, "错误：对话上下文已达到上限，请输入 /clear 后继续")
				continue
			}
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		if lastText != "" && !strings.HasSuffix(lastText, "\n") {
			if _, err := io.WriteString(out, "\n"); err != nil {
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
