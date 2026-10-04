package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/memory"
)

func TestMemorySearchReturnsApprovedWorkspaceMemory(t *testing.T) {
	repo, err := memory.NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(memory.Item{Kind: memory.KindExperience, Text: "保留 viewport 生命周期", Source: "session/1", Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	tool := memorySearchTool{repo: repo}
	result, err := tool.Execute(context.Background(), "", `{"query":"viewport","limit":5}`)
	if err != nil || !strings.Contains(result, "保留 viewport 生命周期") || !strings.Contains(result, "session/1") {
		t.Fatalf("result=%q err=%v", result, err)
	}
}
