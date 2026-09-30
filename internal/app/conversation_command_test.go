package app

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/llm"
)

func TestConversationShowDoesNotPrintMessageBodies(t *testing.T) {
	root := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	store := conversation.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{{Role: "user", Content: "secret prompt"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"conversation", "show", snapshot.ID}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if strings.Contains(out.String(), "secret prompt") || !strings.Contains(out.String(), snapshot.ID) || !strings.Contains(out.String(), "messages=1") {
		t.Fatalf("show leaked or missed metadata: %q", out.String())
	}
}

func TestConversationListEmpty(t *testing.T) {
	root := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"conversation", "list"}, func(string) string { return "" }, &out, &stderr); code != 0 || !strings.Contains(out.String(), "暂无完整会话") {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestConversationDeleteRequiresConfirmation(t *testing.T) {
	root := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	store := conversation.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"conversation", "delete", snapshot.ID}, func(string) string { return "" }, &out, &stderr); code != 2 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if _, err := store.Load(snapshot.ID); err != nil {
		t.Fatalf("snapshot removed without confirmation: %v", err)
	}
	if code := Run(context.Background(), []string{"conversation", "delete", snapshot.ID, "--yes"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("confirmed delete code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}
