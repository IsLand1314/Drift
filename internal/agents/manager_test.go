package agents

import (
	"context"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	"github.com/IsLand1314/Drift/internal/llm"
)

type fakeChildren struct{ started bool }

func (f *fakeChildren) Start(context.Context, llm.Client, string, string, string, time.Duration, agent.EventSink) (*agent.ChildHandle, error) {
	f.started = true
	return nil, nil
}

func TestManagerDelegatesChildLifecycle(t *testing.T) {
	f := &fakeChildren{}
	m := NewManager(f)
	if _, err := m.StartChild(context.Background(), nil, "task-1", t.TempDir(), "inspect", 0, nil); err != nil {
		t.Fatal(err)
	}
	if !f.started {
		t.Fatal("child manager was not called")
	}
}
