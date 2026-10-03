package git

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/IsLand1314/Drift/internal/tool"
)

type Preflight struct {
	Root    string
	Commit  string
	Branch  string
	Preview string
}

type Result struct {
	Status        string
	FailureReason string
	BeforeHEAD    string
	AfterHEAD     string
	Output        string
	ExitCode      int
}

var commandStatusPattern = regexp.MustCompile(`(?:^|\s)status=([a-z_]+)(?:\s|$)`)

func Revert(ctx context.Context, preflight Preflight, sandbox tool.SandboxMode) (Result, error) {
	result := Result{}
	status, err := worktreeStatus(ctx, preflight.Root)
	if err != nil {
		return result, fmt.Errorf("failed: check worktree: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return result, fmt.Errorf("dirty_worktree: worktree changed after preflight")
	}
	before, err := gitOutput(ctx, preflight.Root, "rev-parse", "HEAD")
	if err != nil {
		return result, fmt.Errorf("failed: read HEAD: %w", err)
	}
	result.BeforeHEAD = strings.TrimSpace(string(before))
	decision, err := tool.SelectSandbox(sandbox, tool.DetectSandboxForWorkspace(preflight.Root))
	if err != nil {
		if denied, ok := err.(*tool.SandboxDeniedError); ok {
			result.Status = "denied"
			result.FailureReason = "sandbox_denied"
			return result, denied
		}
		return result, err
	}
	preview := tool.Preview{
		Operation:   "git_revert",
		Path:        ".git",
		Command:     "git revert --no-edit " + preflight.Commit,
		CWD:         ".",
		Timeout:     tool.DefaultCommandTimeout,
		OutputLimit: tool.MaxCommandOutputBytes,
		SandboxMode: sandbox,
		Sandbox:     decision,
	}
	output, err := tool.ExecuteCommand(ctx, preflight.Root, preview)
	result.Output = output
	if err != nil {
		result.Status = "failed"
		if ctx.Err() != nil {
			result.Status = "cancelled"
			result.FailureReason = "cancelled"
		} else if _, ok := err.(*tool.SandboxDeniedError); ok {
			result.Status = "denied"
			result.FailureReason = "sandbox_denied"
		} else {
			result.FailureReason = "failed"
		}
		return result, err
	}
	if match := commandStatusPattern.FindStringSubmatch(output); len(match) == 2 {
		result.Status = match[1]
	} else {
		result.Status = "failed"
	}
	if result.Status != "success" {
		result.FailureReason = classifyFailure(output)
		return result, nil
	}
	after, err := gitOutput(ctx, preflight.Root, "rev-parse", "HEAD")
	if err != nil {
		result.Status = "failed"
		result.FailureReason = "failed"
		return result, fmt.Errorf("failed: read new HEAD: %w", err)
	}
	result.AfterHEAD = strings.TrimSpace(string(after))
	return result, nil
}

func classifyFailure(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "conflict") || strings.Contains(lower, "automatic revert failed"):
		return "conflict"
	case strings.Contains(lower, "hook"):
		return "hook_failed"
	case strings.Contains(lower, "cancel"):
		return "cancelled"
	default:
		return "failed"
	}
}

func PreflightRevert(ctx context.Context, root, target string) (Preflight, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || strings.TrimSpace(root) == "" {
		return Preflight{}, fmt.Errorf("not_git: workspace path is invalid")
	}
	if strings.TrimSpace(target) == "" {
		return Preflight{}, fmt.Errorf("invalid_target: commit is blank")
	}
	root, err = RepositoryRoot(ctx, root)
	if err != nil {
		return Preflight{}, err
	}
	status, err := worktreeStatus(ctx, root)
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

func RepositoryRoot(ctx context.Context, root string) (string, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || root == "" {
		return "", fmt.Errorf("not_git: workspace path is invalid")
	}
	resolvedBytes, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not_git: %w", err)
	}
	resolved, err := filepath.Abs(strings.TrimSpace(string(resolvedBytes)))
	if err != nil || resolved == "" {
		return "", fmt.Errorf("not_git: repository root is invalid")
	}
	return resolved, nil
}

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	return cmd.CombinedOutput()
}

func worktreeStatus(ctx context.Context, root string) ([]byte, error) {
	return gitOutput(ctx, root, "status", "--porcelain", "--untracked-files=all", "--", ":!.drift")
}
