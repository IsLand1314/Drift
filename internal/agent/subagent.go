package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/message"
	"github.com/IsLand1314/Drift/internal/tool"
)

type ChildState string

const DefaultChildConcurrency = 4

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
	active map[string]*ChildHandle
	limit  int
	bus    *message.Bus
}

func (m *ChildManager) SetMessageBus(bus *message.Bus) {
	m.mu.Lock()
	m.bus = bus
	m.mu.Unlock()
}

type ChildHandle struct {
	manager *ChildManager
	cancel  context.CancelFunc
	done    chan ChildResult
	once    sync.Once
	taskID  string
}

// NewChildManager creates a manager with a bounded number of concurrent children.
func NewChildManager(limit int) *ChildManager {
	if limit < 1 {
		limit = 1
	}
	return &ChildManager{active: make(map[string]*ChildHandle), limit: limit}
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
	ctx, cancel := context.WithCancel(parent)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	}
	handle := &ChildHandle{manager: m, cancel: cancel, done: make(chan ChildResult, 1), taskID: taskID}
	m.mu.Lock()
	if m.active == nil {
		m.active = make(map[string]*ChildHandle)
	}
	limit := m.limit
	if limit < 1 {
		limit = 1
	}
	if _, exists := m.active[taskID]; exists || len(m.active) >= limit {
		m.mu.Unlock()
		cancel()
		return nil, errors.New("child agent concurrency limit reached")
	}
	m.active[taskID] = handle
	m.mu.Unlock()
	started := time.Now().UTC()
	go func() {
		release := func() {
			m.mu.Lock()
			if m.active[taskID] == handle {
				delete(m.active, taskID)
			}
			m.mu.Unlock()
		}
		defer release()
		if sink != nil {
			_ = sink(Event{Type: EventRunStarted, TaskID: taskID, CWD: worktree, ExecutionStatus: string(ChildRunning)})
		}
		m.publishMessage(taskID, "progress", "child started")
		childRegistry := tool.NewChatRegistry()
		m.mu.Lock()
		bus := m.bus
		m.mu.Unlock()
		if messages, ok := childRegistry.(tool.MessageRegistry); ok {
			messages.SetMessageBus(bus)
		}
		runner := NewRunner(client, worktree, "", childRegistry)
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
		text := result.Output
		if text == "" && result.Err != nil {
			text = result.Err.Error()
		}
		if text == "" {
			text = string(result.State)
		}
		m.publishMessage(taskID, "result", text)
		release()
		handle.done <- result
	}()
	return handle, nil
}

func (m *ChildManager) publishMessage(taskID, kind, text string) {
	m.mu.Lock()
	bus := m.bus
	m.mu.Unlock()
	if bus != nil {
		_, _ = bus.Send("child-"+taskID, "main", taskID, kind, text)
	}
}

func (m *ChildManager) Cancel(taskID string) error {
	m.mu.Lock()
	handle := m.active[taskID]
	m.mu.Unlock()
	if handle == nil {
		return fmt.Errorf("child agent task %q is not running", taskID)
	}
	handle.Cancel()
	return nil
}

// Shutdown cancels the active child and waits for it to release its process
// slot before the parent closes its audit/session resources.
func (m *ChildManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	handles := make([]*ChildHandle, 0, len(m.active))
	for _, handle := range m.active {
		handles = append(handles, handle)
	}
	m.mu.Unlock()
	if len(handles) == 0 {
		return nil
	}
	for _, handle := range handles {
		handle.Cancel()
	}
	for _, handle := range handles {
		if _, err := handle.Wait(ctx); err != nil {
			return err
		}
	}
	return nil
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
