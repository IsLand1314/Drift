package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

type childModelProbe struct{ model chan string }

func (p childModelProbe) Stream(_ context.Context, request llm.Request, _ func(llm.StreamEvent) error) (llm.Completion, error) {
	p.model <- request.Model
	return llm.Completion{Assistant: llm.Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}, nil
}

func TestChildTaskRunnerPassesConfiguredModelToChild(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "agent-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	probe := childModelProbe{model: make(chan string, 1)}
	manager := agent.NewChildManager(1)
	runner := childTaskRunner(manager, modelClient{Client: probe, model: "deepseek-chat"}, root, nil)
	handle, err := runner(context.Background(), tool.TaskState{ID: "task-1", Worktree: ".worktrees/agent-1"}, "read", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case model := <-probe.model:
		if model != "deepseek-chat" {
			t.Fatalf("child model=%q", model)
		}
	default:
		t.Fatal("child did not issue a model request")
	}
}
