package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadThenEditRejectsExternalChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	read, _ := registry.Lookup("ReadFile")
	edit, _ := registry.Lookup("EditFile")
	if _, err := read.Execute(context.Background(), root, `{"path":"main.txt"}`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previewable := edit.(Previewable)
	_, err := previewable.Preview(context.Background(), root, `{"path":"main.txt","old_text":"external","new_text":"changed"}`)
	if err == nil || !strings.Contains(err.Error(), "changed since ReadFile") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadThenEditCommitsAndInvalidatesSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewChatRegistry()
	read, _ := registry.Lookup("ReadFile")
	edit := registry.Lookup
	if _, err := read.Execute(context.Background(), root, `{"path":"main.txt"}`); err != nil {
		t.Fatal(err)
	}
	tool, _ := edit("EditFile")
	preview, err := tool.(Previewable).Preview(context.Background(), root, `{"path":"main.txt","old_text":"one","new_text":"two"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.(Previewable).ExecutePreview(context.Background(), root, preview); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.(Previewable).Preview(context.Background(), root, `{"path":"main.txt","old_text":"two","new_text":"three"}`); err != nil {
		t.Fatalf("snapshot was not invalidated: %v", err)
	}
	_ = path
}
