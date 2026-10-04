package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepositorySearchesByKeywordAndDeletesWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	repo, err := NewRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(Item{Kind: KindFact, Text: "项目使用 Go", Source: "session/1"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(Item{Kind: KindDecision, Text: "默认使用 inline 模式", Source: "session/2"}); err != nil {
		t.Fatal(err)
	}
	hits, err := repo.Search("inline", 20)
	if err != nil || len(hits) != 1 || hits[0].Kind != KindDecision {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	for _, name := range []string{"facts.jsonl", "decisions.jsonl", "index.json"} {
		if _, err := os.Stat(filepath.Join(root, ".drift", "memory", name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if err := repo.Delete(KindDecision, "默认使用 inline 模式"); err != nil {
		t.Fatal(err)
	}
	hits, err = repo.Search("inline", 20)
	if err != nil || len(hits) != 0 {
		t.Fatalf("hits after delete=%+v err=%v", hits, err)
	}
}

func TestRepositoryReviewsExperienceWithoutAutoPromotingIt(t *testing.T) {
	repo, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := Item{Kind: KindExperience, Text: "窗口缩放时保留 viewport", Source: "session/1", Status: "candidate"}
	if err := repo.Add(item); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(Item{Kind: KindExperience, Text: item.Text, Source: item.Source, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReviewExperience(item.Text, "verified"); err != nil {
		t.Fatal(err)
	}
	hits, err := repo.Search("viewport", 20)
	if err != nil || len(hits) != 1 || hits[0].Status != "verified" {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
}

func TestRepositoryRejectsSensitiveItems(t *testing.T) {
	repo, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(Item{Kind: KindFact, Text: "api_key=secret"}); err != ErrInvalidItem {
		t.Fatalf("Add() error=%v, want ErrInvalidItem", err)
	}
}

func TestRepositorySearchFiltersUnverifiedExperienceAndPrunes(t *testing.T) {
	repo, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(Item{Kind: KindExperience, Text: "candidate viewport", Status: "candidate"}); err != nil {
		t.Fatal(err)
	}
	if hits, err := repo.Search("viewport", 20); err != nil || len(hits) != 0 {
		t.Fatalf("unverified hits=%v err=%v", hits, err)
	}
	if hits, err := repo.SearchWithOptions("viewport", SearchOptions{Status: "candidate"}); err != nil || len(hits) != 1 {
		t.Fatalf("filtered hits=%v err=%v", hits, err)
	}
	if err := repo.Add(Item{Kind: KindFact, Text: "old fact", UpdatedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	removed, err := repo.PruneBefore(time.Now().Add(-24 * time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
}
