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
