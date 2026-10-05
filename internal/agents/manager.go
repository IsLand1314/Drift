// Package agents owns multi-agent orchestration boundaries. Execution stays
// in agent and scheduling/merge stays in coordinator; this package only
// composes those existing services.
package agents

import (
	"context"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/llm"
)

type ChildManager interface {
	Start(context.Context, llm.Client, string, string, string, time.Duration, agent.EventSink) (*agent.ChildHandle, error)
}

type Manager struct{ children ChildManager }

func NewManager(children ChildManager) *Manager { return &Manager{children: children} }

func (m *Manager) StartChild(ctx context.Context, client llm.Client, taskID, worktree, prompt string, timeout time.Duration, sink agent.EventSink) (*agent.ChildHandle, error) {
	return m.children.Start(ctx, client, taskID, worktree, prompt, timeout, sink)
}
