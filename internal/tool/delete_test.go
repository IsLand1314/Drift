package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRequiresApprovalAndRemovesRegularFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "old.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := Delete(root, `{"path":"old.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitDelete(context.Background(), root, preview); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("target still exists: %v", err)
	}
}

func TestDeleteRejectsExternalChangeAfterPreview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "old.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := Delete(root, `{"path":"old.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitDelete(context.Background(), root, preview); err == nil {
		t.Fatal("stale delete unexpectedly committed")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "external" {
		t.Fatalf("external content removed: %q", got)
	}
}

func TestDeleteRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(root, `{"path":"dir"}`); err == nil {
		t.Fatal("directory deletion accepted")
	}
}
