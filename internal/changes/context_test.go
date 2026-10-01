package changes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChangeSetAggregatesFiles(t *testing.T) {
	root := t.TempDir()
	set, err := Begin(root, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithChangeSet(context.Background(), set)
	if FromContext(ctx) != set {
		t.Fatal("context lost change set")
	}
	if err := set.Record(Manifest{Operation: "create_file", Path: "a.txt", NewBytes: 1}, "a", []byte("a"), "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := set.Record(Manifest{Operation: "edit_file", Path: "b.txt", NewBytes: 1}, "b", []byte("b"), "b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := set.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(set.dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) == "" || len(set.manifest.Files) != 2 {
		t.Fatalf("manifest=%s", manifest)
	}
}
