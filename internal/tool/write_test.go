package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePreviewCreatesFileWithoutChangingWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	preview, err := Write(root, `{"path":"internal/demo.go","content":"package demo\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Operation != "create_file" || preview.OldBytes != 0 || preview.NewBytes != len("package demo\n") {
		t.Fatalf("preview=%+v", preview)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "demo.go")); !os.IsNotExist(err) {
		t.Fatalf("preview changed target, stat err=%v", err)
	}
	if _, err := CommitWrite(context.Background(), root, preview); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "internal", "demo.go"))
	if err != nil || string(content) != "package demo\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestWritePreviewOverwriteRequiresCommit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := Write(root, `{"path":"README.md","content":"new\n"}`)
	if err != nil || preview.Operation != "overwrite_file" || !strings.Contains(preview.Diff, "old") || !strings.Contains(preview.Diff, "new") {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "old\n" {
		t.Fatalf("preview changed content=%q", content)
	}
}

func TestWriteRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"../escape.txt", `C:\escape.txt`, ".drift/x", ".git/config", ".codex-temp/x", ".worktrees/x"} {
		_, err := Write(root, writeJSON(path, "x"))
		if err == nil {
			t.Errorf("Write(%q) succeeded", path)
		}
	}
}

func TestWriteRejectsMissingParentAndUnknownFields(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(root, `{"path":"missing/file.txt","content":"x"}`); err == nil {
		t.Fatal("missing parent accepted")
	}
	if _, err := Write(root, `{"path":"file.txt","content":"x","extra":true}`); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func writeJSON(path, content string) string {
	value, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return string(value)
}
