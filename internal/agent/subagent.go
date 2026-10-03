package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

type ChildState string

const (
	ChildRunning   ChildState = "running"
	ChildCompleted ChildState = "completed"
	ChildFailed    ChildState = "failed"
	ChildCancelled ChildState = "cancelled"
	ChildTimeout   ChildState = "timeout"
)

type ChildResult struct {
	TaskID    string
	Worktree  string
	State     ChildState
	StartedAt time.Time
	EndedAt   time.Time
	Output    string
	Err       error
}

type ChildManager struct {
	mu     sync.Mutex
	active bool
}

type ChildHandle struct {
	manager *ChildManager
	cancel  context.CancelFunc
	done    chan ChildResult
	once    sync.Once
}

// Start launches one child Runner. M4.1 deliberately rejects a second child.
func (m *ChildManager) Start(parent context.Context, client llm.Client, taskID, worktree, prompt string, timeout time.Duration, sink EventSink) (*ChildHandle, error) {
	if client == nil {
		return nil, errors.New("child agent client is nil")
	}
	info, err := os.Stat(worktree)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("child worktree unavailable: %s", worktree)
	}
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return nil, errors.New("child agent already running")
	}
	m.active = true
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	}
	handle := &ChildHandle{manager: m, cancel: cancel, done: make(chan ChildResult, 1)}
	started := time.Now().UTC()
	go func() {
		defer func() {
			m.mu.Lock()
			m.active = false
			m.mu.Unlock()
		}()
		if sink != nil {
			_ = sink(Event{Type: EventRunStarted, TaskID: taskID, CWD: worktree, ExecutionStatus: string(ChildRunning)})
		}
		runner := NewRunner(client, worktree, "", tool.NewChatRegistry())
		err := runner.RunEvents(ctx, prompt, sink)
		result := ChildResult{TaskID: taskID, Worktree: worktree, StartedAt: started, EndedAt: time.Now().UTC(), Output: lastChildText(runner), Err: err}
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			result.State = ChildTimeout
		case errors.Is(ctx.Err(), context.Canceled):
			result.State = ChildCancelled
		case err != nil:
			result.State = ChildFailed
		default:
			result.State = ChildCompleted
		}
		if sink != nil {
			_ = sink(Event{Type: EventRunFinished, TaskID: taskID, CWD: worktree, ExecutionStatus: string(result.State), ChildState: string(result.State), FailureReason: childFailure(result)})
		}
		handle.done <- result
	}()
	return handle, nil
}

func childFailure(result ChildResult) string {
	if result.Err == nil {
		return ""
	}
	return string(result.State)
}

func (h *ChildHandle) Cancel() { h.once.Do(h.cancel) }

func (h *ChildHandle) Wait(ctx context.Context) (ChildResult, error) {
	select {
	case result := <-h.done:
		return result, nil
	case <-ctx.Done():
		return ChildResult{}, ctx.Err()
	}
}

func lastChildText(runner *Runner) string {
	messages := runner.Messages()
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" && messages[index].Content != "" {
			return messages[index].Content
		}
	}
	return ""
}
