package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

func TestGitRevertRequiresExplicitConfirmation(t *testing.T) {
	root := newGitCommandFixture(t)
	writeGitCommandFile(t, root, "one.txt", "one\n")
	gitCommandRun(t, root, "add", "one.txt")
	gitCommandRun(t, root, "commit", "-m", "initial")
	writeGitCommandFile(t, root, "one.txt", "two\n")
	gitCommandRun(t, root, "commit", "-am", "change")
	target := strings.TrimSpace(gitCommandRun(t, root, "rev-parse", "HEAD"))

	var out, stderr strings.Builder
	if code := runGitCommand([]string{"revert", "-w", root, target}, &out, &stderr); code != 2 {
		t.Fatalf("runGitCommand() code = %d, want 2; stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitCommandRun(t, root, "rev-parse", "HEAD")); got != target {
		t.Fatalf("HEAD changed without --yes: %s", got)
	}
}

func TestGitRevertCreatesCommitAndAudit(t *testing.T) {
	root := newGitCommandFixture(t)
	if !tool.DetectSandboxForWorkspace(root).Reliable {
		t.Skip("required sandbox backend unavailable on this host")
	}
	writeGitCommandFile(t, root, "one.txt", "one\n")
	gitCommandRun(t, root, "add", "one.txt")
	gitCommandRun(t, root, "commit", "-m", "initial")
	writeGitCommandFile(t, root, "one.txt", "two\n")
	gitCommandRun(t, root, "commit", "-am", "change")
	target := strings.TrimSpace(gitCommandRun(t, root, "rev-parse", "HEAD"))

	var out, stderr strings.Builder
	if code := runGitCommand([]string{"revert", "-w", root, target, "--yes"}, &out, &stderr); code != 0 {
		t.Fatalf("runGitCommand() code = %d, stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if got := strings.TrimSpace(gitCommandRun(t, root, "rev-parse", "HEAD")); got == target {
		t.Fatal("git revert did not create a new commit")
	}
	data, err := os.ReadFile(filepath.Join(root, "one.txt"))
	if err != nil || strings.TrimSpace(string(data)) != "one" {
		t.Fatalf("one.txt = %q, err=%v", data, err)
	}
	audits, err := session.ListFiles(filepath.Join(root, ".drift", "audits"))
	if err != nil || len(audits) == 0 {
		t.Fatalf("audit files = %v, err=%v", audits, err)
	}
}

func TestGitWorktreeLifecycleRequiresConfirmation(t *testing.T) {
	root := newGitCommandFixture(t)
	writeGitCommandFile(t, root, "one.txt", "one\n")
	gitCommandRun(t, root, "add", "one.txt")
	gitCommandRun(t, root, "commit", "-m", "initial")

	var out, stderr strings.Builder
	if code := runGitCommand([]string{"worktree", "create", "agent", "-w", root}, &out, &stderr); code != 2 {
		t.Fatalf("create without confirmation code = %d, want 2", code)
	}
	if code := runGitCommand([]string{"worktree", "create", "agent", "-w", root, "--yes"}, &out, &stderr); code != 0 {
		t.Fatalf("create code = %d, stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if code := runGitCommand([]string{"worktree", "list", "-w", root}, &out, &stderr); code != 0 || !strings.Contains(out.String(), filepath.Join(root, ".worktrees", "agent")) {
		t.Fatalf("list code = %d, stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if code := runGitCommand([]string{"worktree", "remove", "agent", "-w", root, "--yes"}, &out, &stderr); code != 0 {
		t.Fatalf("remove code = %d, stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestGitWorktreeMergeReportsConflictWithoutChangingMain(t *testing.T) {
	root := newGitCommandFixture(t)
	writeGitCommandFile(t, root, "same.txt", "base\n")
	gitCommandRun(t, root, "add", "same.txt")
	gitCommandRun(t, root, "commit", "-m", "initial")
	var out, stderr strings.Builder
	if code := runGitCommand([]string{"worktree", "create", "agent", "-w", root, "--yes"}, &out, &stderr); code != 0 {
		t.Fatalf("create code=%d stderr=%q", code, stderr.String())
	}
	writeGitCommandFile(t, filepath.Join(root, ".worktrees", "agent"), "same.txt", "branch\n")
	gitCommandRun(t, filepath.Join(root, ".worktrees", "agent"), "add", "same.txt")
	gitCommandRun(t, filepath.Join(root, ".worktrees", "agent"), "commit", "-m", "branch")
	writeGitCommandFile(t, root, "same.txt", "main\n")
	gitCommandRun(t, root, "commit", "-am", "main")
	before := strings.TrimSpace(gitCommandRun(t, root, "rev-parse", "HEAD"))
	if code := runGitCommand([]string{"worktree", "merge", "agent", "-w", root, "--yes"}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "冲突文件") {
		t.Fatalf("merge code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if after := strings.TrimSpace(gitCommandRun(t, root, "rev-parse", "HEAD")); after != before {
		t.Fatalf("main HEAD changed on conflict: before=%s after=%s", before, after)
	}
}

func newGitCommandFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitCommandRun(t, root, "init", "-b", "master")
	gitCommandRun(t, root, "config", "user.email", "drift-test@example.invalid")
	gitCommandRun(t, root, "config", "user.name", "Drift Test")
	return root
}

func writeGitCommandFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func gitCommandRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
