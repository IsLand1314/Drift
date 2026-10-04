package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/memory"
)

func TestExperienceProposeDoesNotPersist(t *testing.T) {
	tool := experienceProposeTool{}
	result, err := tool.Execute(context.Background(), "", `{"title":"稳定缩放","trigger":"窗口变化","solution":"保留 viewport","verification":"回归测试通过"}`)
	if err != nil || !strings.Contains(result, "candidate") || !strings.Contains(result, "请用户确认") {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestExperienceSaveRequiresPreviewApproval(t *testing.T) {
	repo, err := memory.NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := experienceSaveTool{repo: repo}
	raw := `{"title":"稳定缩放","trigger":"窗口变化","solution":"保留 viewport","verification":"回归测试通过"}`
	preview, err := tool.Preview(context.Background(), "", raw)
	if err != nil || preview.Operation != "memory_write" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if _, err := tool.ExecutePreview(context.Background(), "", preview); err != nil {
		t.Fatal(err)
	}
	hits, err := repo.Search("viewport", 20)
	if err != nil || len(hits) != 1 || hits[0].Status != "verified" {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
}
