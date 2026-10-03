package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/tool"
)

func TestPreflightRevertResolvesCleanCommit(t *testing.T) {
	root := newGitFixture(t)
	writeGitFile(t, root, "one.txt", "one\n")
	gitRun(t, root, "add", "one.txt")
	gitRun(t, root, "commit", "-m", "initial")
	writeGitFile(t, root, "one.txt", "two\n")
	gitRun(t, root, "commit", "-am", "change")
	target := strings.TrimSpace(gitRun(t, root, "rev-parse", "HEAD"))

	got, err := PreflightRevert(context.Background(), root, target[:8])
	if err != nil {
		t.Fatalf("PreflightRevert() error = %v", err)
	}
	if got.Root != root || got.Commit != target || got.Branch != "master" || got.Preview == "" {
		t.Fatalf("PreflightRevert() = %+v", got)
	}
}

func TestPreflightRevertRejectsDirtyWorktree(t *testing.T) {
	root := newGitFixture(t)
	writeGitFile(t, root, "one.txt", "one\n")
	gitRun(t, root, "add", "one.txt")
	gitRun(t, root, "commit", "-m", "initial")
	writeGitFile(t, root, "one.txt", "dirty\n")

	_, err := PreflightRevert(context.Background(), root, "HEAD")
	if err == nil || !strings.Contains(err.Error(), "dirty_worktree") {
		t.Fatalf("PreflightRevert() error = %v, want dirty_worktree", err)
	}
}

func TestPreflightRevertIgnoresDriftAuditDirectory(t *testing.T) {
	root := newGitFixture(t)
	writeGitFile(t, root, "one.txt", "one\n")
	gitRun(t, root, "add", "one.txt")
	gitRun(t, root, "commit", "-m", "initial")
	writeGitFile(t, root, "one.txt", "two\n")
	gitRun(t, root, "commit", "-am", "change")
	if err := os.MkdirAll(filepath.Join(root, ".drift", "audits"), 0o700); err != nil {
		t.Fatalf("mkdir audit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".drift", "audits", "run.jsonl"), []byte("audit\n"), 0o600); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	if _, err := PreflightRevert(context.Background(), root, "HEAD"); err != nil {
		t.Fatalf("PreflightRevert() error = %v", err)
	}
}

func TestPreflightRevertRejectsInvalidAndMergeTargets(t *testing.T) {
	root := newGitFixture(t)
	writeGitFile(t, root, "one.txt", "one\n")
	gitRun(t, root, "add", "one.txt")
	gitRun(t, root, "commit", "-m", "initial")
	if _, err := PreflightRevert(context.Background(), root, "not-a-commit"); err == nil || !strings.Contains(err.Error(), "invalid_target") {
		t.Fatalf("invalid target error = %v", err)
	}
}

func TestRevertCreatesReverseCommit(t *testing.T) {
	root := newGitFixture(t)
	writeGitFile(t, root, "one.txt", "one\n")
	gitRun(t, root, "add", "one.txt")
	gitRun(t, root, "commit", "-m", "initial")
	writeGitFile(t, root, "one.txt", "two\n")
	gitRun(t, root, "commit", "-am", "change")
	preflight, err := PreflightRevert(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatalf("PreflightRevert() error = %v", err)
	}

	result, err := Revert(context.Background(), preflight, tool.SandboxOff)
	if err != nil {
		t.Fatalf("Revert() error = %v", err)
	}
	if result.Status != "success" || result.AfterHEAD == result.BeforeHEAD {
		t.Fatalf("Revert() result = %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "one.txt"))
	if err != nil || strings.TrimSpace(string(data)) != "one" {
		t.Fatalf("one.txt = %q, err=%v; want initial content", data, err)
	}
}

func newGitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "-b", "master")
	gitRun(t, root, "config", "user.email", "drift-test@example.invalid")
	gitRun(t, root, "config", "user.name", "Drift Test")
	return root
}

func writeGitFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func gitRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
