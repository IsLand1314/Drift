package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEditRequiresUniqueMatchAndCommits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := Edit(root, `{"path":"main.txt","old_text":"two","new_text":"three"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitEdit(context.Background(), root, preview); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "one\nthree\n" {
		t.Fatalf("content=%q", got)
	}
	for _, old := range []string{"missing", "one\n"} {
		if _, err := Edit(root, `{"path":"main.txt","old_text":`+quote(old)+`,"new_text":"x"}`); err == nil {
			t.Fatalf("old_text %q unexpectedly matched", old)
		}
	}
}

func quote(value string) string { return `"` + value + `"` }
