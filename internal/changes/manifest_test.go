package changes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordWritesDatedManifestWithoutContent(t *testing.T) {
	root := t.TempDir()
	dir, err := Record(root, time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), "abc123", Manifest{Operation: "create_file", Path: "demo.txt", OldBytes: 0, NewBytes: 5, Decision: "allow"}, "--- demo.txt\n+++ demo.txt\n", []byte("secret"), "demo.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dir, filepath.Join(".drift", "changes", "2026", "10", "01")) {
		t.Fatalf("dir=%s", dir)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || strings.Contains(string(manifest), "secret") {
		t.Fatalf("manifest=%q err=%v", manifest, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "diff.patch")); err != nil {
		t.Fatal(err)
	}
}
