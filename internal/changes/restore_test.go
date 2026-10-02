package changes

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreCreateAndEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "edit.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := Begin(root, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := set.RecordMutation(Manifest{Operation: "create_file", Path: "new.txt"}, "", nil, false, []byte("new"), "new.txt"); err != nil {
		t.Fatal(err)
	}
	if err := set.RecordMutation(Manifest{Operation: "edit_file", Path: "edit.txt"}, "", []byte("old"), true, []byte("next"), "edit.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "edit.txt"), []byte("next"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := set.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, set.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new file still exists: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "edit.txt"))
	if err != nil || string(b) != "old" {
		t.Fatalf("edit restore=%q err=%v", b, err)
	}
}

func TestRestoreRefusesStaleTargetWithoutChanges(t *testing.T) {
	root := t.TempDir()
	set, err := Begin(root, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := set.RecordMutation(Manifest{Operation: "create_file", Path: "a.txt"}, "", nil, false, []byte("after"), "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("user-change"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := set.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, set.dir); err == nil {
		t.Fatal("expected stale target error")
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "user-change" {
		t.Fatalf("target changed by failed restore: %q", got)
	}
}

func TestRestoreDeletedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	set, err := Begin(root, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Record(Manifest{Operation: "delete_file", Path: "nested/a.txt"}, "", []byte("gone"), "nested/a.txt.before"); err != nil {
		t.Fatal(err)
	}
	if err := set.Finalize("complete"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "a.txt"), []byte("gone"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "nested", "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, set.dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "nested", "a.txt"))
	if err != nil || string(b) != "gone" {
		t.Fatalf("delete restore=%q err=%v", b, err)
	}
}
