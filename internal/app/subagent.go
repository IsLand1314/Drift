package app

import (
	"context"
	"path/filepath"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

type childTaskHandle struct{ handle *agent.ChildHandle }

func (h childTaskHandle) Cancel() { h.handle.Cancel() }

func (h childTaskHandle) Wait(ctx context.Context) (tool.TaskExecutionResult, error) {
	result, err := h.handle.Wait(ctx)
	if err != nil {
		return tool.TaskExecutionResult{State: "failed", Err: err}, err
	}
	return tool.TaskExecutionResult{
		State:  string(result.State),
		Output: result.Output,
		Err:    result.Err,
	}, nil
}

// childTaskRunner adapts the agent lifecycle to the task control-plane API.
func childTaskRunner(manager *agent.ChildManager, client llm.Client, root string, sink agent.EventSink) tool.TaskRunner {
	return func(ctx context.Context, task tool.TaskState, prompt string, timeout time.Duration) (tool.TaskHandle, error) {
		worktree := filepath.Join(root, filepath.FromSlash(task.Worktree))
		handle, err := manager.Start(ctx, client, task.ID, worktree, prompt, timeout, sink)
		if err != nil {
			return nil, err
		}
		return childTaskHandle{handle: handle}, nil
	}
}
