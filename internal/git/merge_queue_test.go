package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeQueueAutomaticallyMergesCompletedWorktree(t *testing.T) {
	root := newWorktreeFixture(t)
	writeWorktreeFile(t, root, "base.txt", "base\n")
	gitWorktreeRun(t, root, "add", "base.txt")
	gitWorktreeRun(t, root, "commit", "-m", "initial")
	created, err := CreateWorktree(context.Background(), root, "agent", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeWorktreeFile(t, created.Path, "feature.txt", "feature\n")
	gitWorktreeRun(t, created.Path, "add", "feature.txt")
	gitWorktreeRun(t, created.Path, "commit", "-m", "feature")

	queue := NewMergeQueue()
	result, err := queue.Submit(context.Background(), root, "task-1", created.Path)
	if err != nil || result.Status != "success" {
		t.Fatalf("submit result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.txt")); err != nil {
		t.Fatal(err)
	}
	if got := queue.Pending(); len(got) != 0 {
		t.Fatalf("pending=%+v", got)
	}
}

func TestMergeQueueKeepsConflictForRetryWithoutChangingMain(t *testing.T) {
	root := newWorktreeFixture(t)
	writeWorktreeFile(t, root, "same.txt", "base\n")
	gitWorktreeRun(t, root, "add", "same.txt")
	gitWorktreeRun(t, root, "commit", "-m", "initial")
	created, err := CreateWorktree(context.Background(), root, "agent", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeWorktreeFile(t, created.Path, "same.txt", "branch\n")
	gitWorktreeRun(t, created.Path, "add", "same.txt")
	gitWorktreeRun(t, created.Path, "commit", "-m", "branch")
	writeWorktreeFile(t, root, "same.txt", "main\n")
	gitWorktreeRun(t, root, "commit", "-am", "main")
	before := strings.TrimSpace(gitWorktreeRun(t, root, "rev-parse", "HEAD"))

	queue := NewMergeQueue()
	result, err := queue.Submit(context.Background(), root, "task-1", created.Path)
	if err != nil || result.Status != "conflict" || len(result.Conflicts) != 1 {
		t.Fatalf("submit result=%+v err=%v", result, err)
	}
	if after := strings.TrimSpace(gitWorktreeRun(t, root, "rev-parse", "HEAD")); after != before {
		t.Fatalf("main changed: %s -> %s", before, after)
	}
	pending := queue.Pending()
	if len(pending) != 1 || pending[0].TaskID != "task-1" || pending[0].Status != "conflict" {
		t.Fatalf("pending=%+v", pending)
	}
}

func TestMergeQueueRejectsDuplicatePendingTask(t *testing.T) {
	queue := NewMergeQueue()
	queue.pending["task-1"] = MergeRequest{TaskID: "task-1", Status: "conflict"}
	if _, err := queue.Submit(context.Background(), t.TempDir(), "task-1", ""); err == nil || !strings.Contains(err.Error(), "already queued") {
		t.Fatalf("err=%v", err)
	}
}

func TestMergeQueueRetryRemovesResolvedConflict(t *testing.T) {
	root := newWorktreeFixture(t)
	writeWorktreeFile(t, root, "same.txt", "base\n")
	gitWorktreeRun(t, root, "add", "same.txt")
	gitWorktreeRun(t, root, "commit", "-m", "initial")
	created, err := CreateWorktree(context.Background(), root, "agent", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeWorktreeFile(t, created.Path, "same.txt", "branch\n")
	gitWorktreeRun(t, created.Path, "add", "same.txt")
	gitWorktreeRun(t, created.Path, "commit", "-m", "branch")
	writeWorktreeFile(t, root, "same.txt", "main\n")
	gitWorktreeRun(t, root, "commit", "-am", "main")
	queue := NewMergeQueue()
	result, err := queue.Submit(context.Background(), root, "task-1", created.Path)
	if err != nil || result.Status != "conflict" {
		t.Fatalf("submit result=%+v err=%v", result, err)
	}
	merge := exec.Command("git", "-C", created.Path, "merge", "master")
	if err := merge.Run(); err == nil {
		t.Fatal("expected source merge conflict")
	}
	writeWorktreeFile(t, created.Path, "same.txt", "resolved\n")
	gitWorktreeRun(t, created.Path, "add", "same.txt")
	gitWorktreeRun(t, created.Path, "commit", "-m", "resolve")
	result, err = queue.Retry(context.Background(), root, "task-1")
	if err != nil || result.Status != "success" {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
	if got := queue.Pending(); len(got) != 0 {
		t.Fatalf("pending=%+v", got)
	}
	data, err := os.ReadFile(filepath.Join(root, "same.txt"))
	if err != nil || string(data) != "resolved\n" {
		t.Fatalf("merged content=%q err=%v", data, err)
	}
}
