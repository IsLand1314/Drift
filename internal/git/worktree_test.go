package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateListAndRemoveManagedWorktree(t *testing.T) {
	root := newWorktreeFixture(t)
	writeWorktreeFile(t, root, "one.txt", "one\n")
	gitWorktreeRun(t, root, "add", "one.txt")
	gitWorktreeRun(t, root, "commit", "-m", "initial")

	created, err := CreateWorktree(context.Background(), root, "agent-1", "HEAD")
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	wantPath := filepath.Join(root, ".worktrees", "agent-1")
	if created.Path != wantPath {
		t.Fatalf("created path = %q, want %q", created.Path, wantPath)
	}
	if created.Branch != "drift/agent-1" {
		t.Fatalf("created branch = %q", created.Branch)
	}
	if got, err := os.ReadFile(filepath.Join(created.Path, "one.txt")); err != nil || string(got) != "one\n" {
		t.Fatalf("worktree file = %q, err=%v", got, err)
	}

	listed, err := ListWorktrees(context.Background(), root)
	if err != nil {
		t.Fatalf("ListWorktrees() error = %v", err)
	}
	found := false
	for _, item := range listed {
		if filepath.Clean(item.Path) == filepath.Clean(created.Path) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListWorktrees() = %+v, created worktree missing", listed)
	}

	if err := RemoveWorktree(context.Background(), root, "agent-1"); err != nil {
		t.Fatalf("RemoveWorktree() error = %v", err)
	}
	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("removed path still exists, stat err = %v", err)
	}
}

func TestMergeWorktreeCreatesMergeCommit(t *testing.T) {
	root := newWorktreeFixture(t)
	writeWorktreeFile(t, root, "one.txt", "one\n")
	gitWorktreeRun(t, root, "add", "one.txt")
	gitWorktreeRun(t, root, "commit", "-m", "initial")
	created, err := CreateWorktree(context.Background(), root, "agent", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeWorktreeFile(t, created.Path, "feature.txt", "feature\n")
	gitWorktreeRun(t, created.Path, "add", "feature.txt")
	gitWorktreeRun(t, created.Path, "commit", "-m", "feature")
	result, err := MergeWorktree(context.Background(), root, "agent")
	if err != nil || result.Status != "success" || result.BeforeHEAD == result.AfterHEAD {
		t.Fatalf("MergeWorktree() result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.txt")); err != nil {
		t.Fatalf("merged file missing: %v", err)
	}
}

func TestMergeWorktreeRejectsDirtyMainAndLeavesConflictUnchanged(t *testing.T) {
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
	writeWorktreeFile(t, root, "dirty.txt", "user\n")
	if _, err := MergeWorktree(context.Background(), root, "agent"); err == nil || !strings.Contains(err.Error(), "dirty_main_worktree") {
		t.Fatalf("dirty main error=%v", err)
	}
	if err := os.Remove(filepath.Join(root, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	writeWorktreeFile(t, root, "same.txt", "main\n")
	gitWorktreeRun(t, root, "commit", "-am", "main change")
	before := strings.TrimSpace(gitWorktreeRun(t, root, "rev-parse", "HEAD"))
	result, err := MergeWorktree(context.Background(), root, "agent")
	if err != nil || result.Status != "conflict" || len(result.Conflicts) != 1 || result.Conflicts[0] != "same.txt" {
		t.Fatalf("conflict result=%+v err=%v", result, err)
	}
	if after := strings.TrimSpace(gitWorktreeRun(t, root, "rev-parse", "HEAD")); after != before {
		t.Fatalf("main HEAD changed on conflict: before=%s after=%s", before, after)
	}
}

func TestCreateWorktreeRejectsUnsafeNameAndExistingPath(t *testing.T) {
	root := newWorktreeFixture(t)
	gitWorktreeRun(t, root, "commit", "--allow-empty", "-m", "initial")
	for _, name := range []string{"../escape", "nested/name", ".", ""} {
		if _, err := CreateWorktree(context.Background(), root, name, "HEAD"); err == nil || !strings.Contains(err.Error(), "invalid_name") {
			t.Errorf("name %q error = %v, want invalid_name", name, err)
		}
	}
	if _, err := CreateWorktree(context.Background(), root, "agent", "HEAD"); err != nil {
		t.Fatalf("first CreateWorktree() error = %v", err)
	}
	if _, err := CreateWorktree(context.Background(), root, "agent", "HEAD"); err == nil || !strings.Contains(err.Error(), "worktree_exists") {
		t.Fatalf("duplicate CreateWorktree() error = %v, want worktree_exists", err)
	}
}

func newWorktreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitWorktreeRun(t, root, "init", "-b", "master")
	gitWorktreeRun(t, root, "config", "user.email", "drift-test@example.invalid")
	gitWorktreeRun(t, root, "config", "user.name", "Drift Test")
	gitWorktreeRun(t, root, "config", "core.autocrlf", "false")
	return root
}

func writeWorktreeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitWorktreeRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
