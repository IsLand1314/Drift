package memory

import (
	"os"
	"path/filepath"
	"testing"
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

func TestRepositoryRejectsSensitiveItems(t *testing.T) {
	repo, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(Item{Kind: KindFact, Text: "api_key=secret"}); err != ErrInvalidItem {
		t.Fatalf("Add() error=%v, want ErrInvalidItem", err)
	}
}
