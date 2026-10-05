package app

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/session"
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
	store := session.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{{Role: "user", Content: "secret prompt"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "show", snapshot.ID}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if strings.Contains(out.String(), "secret prompt") || !strings.Contains(out.String(), snapshot.ID) || !strings.Contains(out.String(), "messages=1") {
		t.Fatalf("show leaked or missed metadata: %q", out.String())
	}
}

func TestConversationTimelineShowsStructureWithoutBodies(t *testing.T) {
	root := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	store := session.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{
		{Role: "user", Content: "secret prompt"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "ReadFile"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "secret result"},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "timeline", snapshot.ID}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	got := out.String()
	for _, required := range []string{"01 user", "02 assistant", "ReadFile", "03 tool", "bytes="} {
		if !strings.Contains(got, required) {
			t.Fatalf("timeline missing %q: %q", required, got)
		}
	}
	if strings.Contains(got, "secret prompt") || strings.Contains(got, "secret result") {
		t.Fatalf("timeline leaked body: %q", got)
	}
}

func TestConversationSearchPrintsLocationsWithoutBodies(t *testing.T) {
	root := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	store := session.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{{Role: "user", Content: "find the deployment"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "search", "deployment"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), snapshot.ID) || !strings.Contains(out.String(), "matches=user#1") || strings.Contains(out.String(), "find the deployment") {
		t.Fatalf("search output=%q", out.String())
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
	if code := Run(context.Background(), []string{"session", "list"}, func(string) string { return "" }, &out, &stderr); code != 0 || !strings.Contains(out.String(), "暂无完整会话") {
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
	store := session.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "delete", snapshot.ID}, func(string) string { return "" }, &out, &stderr); code != 2 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if _, err := store.Load(snapshot.ID); err != nil {
		t.Fatalf("snapshot removed without confirmation: %v", err)
	}
	if code := Run(context.Background(), []string{"session", "delete", snapshot.ID, "--yes"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("confirmed delete code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestConversationRenameAndListLimit(t *testing.T) {
	root := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	store := session.NewStore(root)
	first, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Rename(first.ID, "FoxCode 分析"); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "list", "--limit", "1"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("list code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "FoxCode 分析") || strings.Contains(out.String(), second.ID) {
		t.Fatalf("list output=%q", out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"session", "rename", second.ID, "第二个会话"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("rename code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	loaded, err := store.Load(second.ID)
	if err != nil || loaded.Title != "第二个会话" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestConversationPruneWithoutYesDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	store := session.NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.UpdatedAt = time.Now().UTC().Add(-time.Hour)
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	threshold := time.Now().UTC().Format(time.RFC3339)
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "prune", "--before", threshold}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("prune preview code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), snapshot.ID) {
		t.Fatalf("preview=%q", out.String())
	}
	if _, err := store.Load(snapshot.ID); err != nil {
		t.Fatalf("preview deleted snapshot: %v", err)
	}
	out.Reset()
	if code := Run(context.Background(), []string{"session", "prune", "--before", threshold, "--yes"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("prune delete code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	if _, err := store.Load(snapshot.ID); err == nil {
		t.Fatal("confirmed prune kept expired snapshot")
	}
}
