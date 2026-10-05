package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

type blockingReadTool struct {
	name   string
	active *int32
	max    *int32
}

type schedulerClient struct{ turns int }

func (c *schedulerClient) StreamEvents(_ context.Context, _ llm.Request, emit func(llm.Event) error) error {
	if c.turns == 0 {
		c.turns++
		if err := emit(llm.ToolCallComplete{Index: 0, ID: "call-a", Name: "Glob", Arguments: `{}`}); err != nil {
			return err
		}
		if err := emit(llm.ToolCallComplete{Index: 1, ID: "call-b", Name: "Grep", Arguments: `{}`}); err != nil {
			return err
		}
		return emit(llm.StreamEnd{Status: llm.StreamCompleted, FinishReason: "tool_calls"})
	}
	if err := emit(llm.TextDelta{Text: "done"}); err != nil {
		return err
	}
	return emit(llm.StreamEnd{Status: llm.StreamCompleted, FinishReason: "stop"})
}

func (t blockingReadTool) Name() string { return t.name }
func (t blockingReadTool) Definition() llm.ToolDefinition {
	raw, _ := json.Marshal(map[string]any{"name": t.name, "description": "test read", "parameters": map[string]any{"type": "object"}})
	return llm.ToolDefinition{Type: "function", Function: raw}
}
func (t blockingReadTool) Execute(ctx context.Context, _ string, _ string) (string, error) {
	current := atomic.AddInt32(t.active, 1)
	for {
		old := atomic.LoadInt32(t.max)
		if current <= old || atomic.CompareAndSwapInt32(t.max, old, current) {
			break
		}
	}
	defer atomic.AddInt32(t.active, -1)
	select {
	case <-time.After(40 * time.Millisecond):
		return t.name + " done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestExecuteReadOnlyBatchRunsAdjacentReadsConcurrently(t *testing.T) {
	var active, max int32
	registry, err := tool.NewRegistry(
		blockingReadTool{name: "Glob", active: &active, max: &max},
		blockingReadTool{name: "Grep", active: &active, max: &max},
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{registry: registry, root: t.TempDir()}
	start := time.Now()
	results, err := runner.executeReadOnlyBatch(context.Background(), []llm.ToolCall{
		{ID: "a", Name: "Glob", Arguments: `{}`},
		{ID: "b", Name: "Grep", Arguments: `{}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 75*time.Millisecond {
		t.Fatalf("batch took %s; adjacent reads were not concurrent", elapsed)
	}
	if atomic.LoadInt32(&max) != 2 {
		t.Fatalf("max concurrent tools = %d, want 2", max)
	}
	if len(results) != 2 || results[0].result != "Glob done" || results[1].result != "Grep done" {
		t.Fatalf("results = %#v", results)
	}
}

func TestRunnerSchedulesAdjacentReadToolsConcurrently(t *testing.T) {
	var active, max int32
	registry, err := tool.NewRegistry(
		blockingReadTool{name: "Glob", active: &active, max: &max},
		blockingReadTool{name: "Grep", active: &active, max: &max},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := &schedulerClient{}
	start := time.Now()
	if err := RunEventsWithRegistry(context.Background(), client, t.TempDir(), "inspect", "", registry, nil); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 75*time.Millisecond {
		t.Fatalf("runner took %s; adjacent reads were not concurrent", elapsed)
	}
	if atomic.LoadInt32(&max) != 2 {
		t.Fatalf("max concurrent tools = %d, want 2", max)
	}
}

func TestReadOnlyBatchStopsBeforeWriteTool(t *testing.T) {
	var active, max int32
	registry, err := tool.NewRegistry(
		blockingReadTool{name: "Glob", active: &active, max: &max},
		blockingReadTool{name: "WriteFile", active: &active, max: &max},
		blockingReadTool{name: "Grep", active: &active, max: &max},
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{registry: registry, root: t.TempDir()}
	calls := []llm.ToolCall{
		{ID: "read", Name: "Glob", Arguments: `{}`},
		{ID: "write", Name: "WriteFile", Arguments: `{}`},
		{ID: "read-after", Name: "Grep", Arguments: `{}`},
	}
	if got := runner.readOnlyBatchEnd(calls, 0); got != 1 {
		t.Fatalf("batch end = %d, want 1 before WriteFile", got)
	}
	if got := runner.readOnlyBatchEnd(calls, 1); got != 1 {
		t.Fatalf("write batch end = %d, want 1", got)
	}
	if got := runner.readOnlyBatchEnd(calls, 2); got != 3 {
		t.Fatalf("post-write read batch end = %d, want 3", got)
	}
}
