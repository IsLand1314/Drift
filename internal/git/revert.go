package git

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Preflight struct {
	Root    string
	Commit  string
	Branch  string
	Preview string
}

func PreflightRevert(ctx context.Context, root, target string) (Preflight, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || strings.TrimSpace(root) == "" {
		return Preflight{}, fmt.Errorf("not_git: workspace path is invalid")
	}
	if strings.TrimSpace(target) == "" {
		return Preflight{}, fmt.Errorf("invalid_target: commit is blank")
	}
	if _, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel"); err != nil {
		return Preflight{}, fmt.Errorf("not_git: %w", err)
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain")
	if err != nil {
		return Preflight{}, fmt.Errorf("not_git: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return Preflight{}, fmt.Errorf("dirty_worktree: worktree is not clean")
	}
	commit, err := gitOutput(ctx, root, "rev-parse", "--verify", "--end-of-options", target+"^{commit}")
	if err != nil {
		return Preflight{}, fmt.Errorf("invalid_target: %w", err)
	}
	commit = []byte(strings.TrimSpace(string(commit)))
	if len(commit) != 40 {
		return Preflight{}, fmt.Errorf("invalid_target: resolved commit is not a full SHA")
	}
	if _, err := gitOutput(ctx, root, "rev-parse", "--verify", "--end-of-options", string(commit)+"^2"); err == nil {
		return Preflight{}, fmt.Errorf("merge_commit: merge commits are not supported")
	}
	branch, err := gitOutput(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return Preflight{}, fmt.Errorf("invalid_target: detached HEAD is not supported")
	}
	preview, err := gitOutput(ctx, root, "show", "--stat", "--oneline", "--no-renames", string(commit))
	if err != nil {
		return Preflight{}, fmt.Errorf("invalid_target: cannot create preview: %w", err)
	}
	return Preflight{Root: root, Commit: string(commit), Branch: strings.TrimSpace(string(branch)), Preview: strings.TrimSpace(string(preview))}, nil
}

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	return cmd.CombinedOutput()
}
