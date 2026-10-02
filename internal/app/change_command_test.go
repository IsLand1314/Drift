package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/changes"
)

func TestChangeRestoreCommandRestoresCompletedSet(t *testing.T) {
	root := t.TempDir()
	started := time.Now().UTC()
	dir, err := changes.RecordMutation(root, started, "test123", changes.Manifest{Operation: "create_file", Path: "a.txt"}, "", nil, false, []byte("a"), "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	var out, stderr bytes.Buffer
	if code := runChangeCommand([]string{"restore", "-w", root, dir, "--yes"}, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("restored file still exists: %v", err)
	}
	if !strings.Contains(out.String(), "已恢复 change set") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestChangeRestoreCommandRequiresConfirmation(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := runChangeCommand([]string{"restore", "x"}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestChangeRestoreCommandRejectsWrongWorkspace(t *testing.T) {
	root := t.TempDir()
	dir, err := changes.Record(root, time.Now().UTC(), "test123", changes.Manifest{Operation: "create_file", Path: "a.txt"}, "", []byte("a"), "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := runChangeCommand([]string{"restore", "-w", t.TempDir(), dir, "--yes"}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "outside workspace") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
