package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Worktree is one Git linked worktree reported by Git.
type Worktree struct {
	Path   string
	HEAD   string
	Branch string
}

type MergeResult struct {
	Status        string
	FailureReason string
	BeforeHEAD    string
	AfterHEAD     string
	Conflicts     []string
}

var worktreeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// CreateWorktree creates a branch-backed managed worktree under .worktrees.
func CreateWorktree(ctx context.Context, root, name, base string) (Worktree, error) {
	root, err := RepositoryRoot(ctx, root)
	if err != nil {
		return Worktree{}, err
	}
	if !worktreeNamePattern.MatchString(name) {
		return Worktree{}, fmt.Errorf("invalid_name: worktree name must match %s", worktreeNamePattern.String())
	}
	if strings.TrimSpace(base) == "" {
		base = "HEAD"
	}
	resolved, err := gitOutput(ctx, root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return Worktree{}, fmt.Errorf("invalid_base: %w", err)
	}
	base = strings.TrimSpace(string(resolved))
	managed := filepath.Join(root, ".worktrees")
	if info, err := os.Lstat(managed); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return Worktree{}, fmt.Errorf("invalid_path: .worktrees is a symlink")
	}
	if err := os.MkdirAll(managed, 0o700); err != nil {
		return Worktree{}, fmt.Errorf("failed: create worktree directory: %w", err)
	}
	target := filepath.Join(managed, name)
	if _, err := os.Lstat(target); err == nil {
		return Worktree{}, fmt.Errorf("worktree_exists: %s", target)
	} else if !os.IsNotExist(err) {
		return Worktree{}, fmt.Errorf("failed: inspect worktree path: %w", err)
	}
	branch := "drift/" + name
	if _, err := gitOutput(ctx, root, "worktree", "add", "-b", branch, target, base); err != nil {
		return Worktree{}, fmt.Errorf("create_failed: %w", err)
	}
	return Worktree{Path: target, HEAD: base, Branch: branch}, nil
}

// ListWorktrees returns Git's linked worktrees, including the main worktree.
func ListWorktrees(ctx context.Context, root string) ([]Worktree, error) {
	root, err := RepositoryRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	out, err := gitOutput(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list_failed: %w", err)
	}
	var result []Worktree
	var current *Worktree
	flush := func() {
		if current != nil {
			result = append(result, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case current != nil && strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		case current != nil && strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case strings.TrimSpace(line) == "":
			flush()
		}
	}
	flush()
	return result, nil
}

// RemoveWorktree removes one managed worktree. Git refuses removal when it is dirty.
func RemoveWorktree(ctx context.Context, root, name string) error {
	root, err := RepositoryRoot(ctx, root)
	if err != nil {
		return err
	}
	if !worktreeNamePattern.MatchString(name) {
		return fmt.Errorf("invalid_name: worktree name must match %s", worktreeNamePattern.String())
	}
	managed := filepath.Join(root, ".worktrees")
	if info, err := os.Lstat(managed); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid_path: .worktrees is a symlink")
	}
	target := filepath.Join(managed, name)
	if _, err := os.Lstat(target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree_missing: %s", name)
		}
		return fmt.Errorf("failed: inspect worktree path: %w", err)
	}
	if _, err := gitOutput(ctx, root, "worktree", "remove", target); err != nil {
		return fmt.Errorf("remove_failed: %w", err)
	}
	return nil
}

// MergeWorktree merges a clean managed worktree branch into a clean main worktree.
// It preflights conflicts before mutating the main worktree.
func MergeWorktree(ctx context.Context, root, name string) (MergeResult, error) {
	var result MergeResult
	root, err := RepositoryRoot(ctx, root)
	if err != nil {
		return result, err
	}
	if !worktreeNamePattern.MatchString(name) {
		return result, fmt.Errorf("invalid_name: worktree name must match %s", worktreeNamePattern.String())
	}
	if status, err := worktreeStatus(ctx, root); err != nil {
		return result, fmt.Errorf("not_git: check main worktree: %w", err)
	} else if strings.TrimSpace(string(status)) != "" {
		return result, fmt.Errorf("dirty_main_worktree: main worktree is not clean")
	}
	worktreePath := filepath.Join(root, ".worktrees", name)
	if info, err := os.Stat(worktreePath); err != nil || !info.IsDir() {
		return result, fmt.Errorf("worktree_missing: %s", name)
	}
	if status, err := worktreeStatus(ctx, worktreePath); err != nil {
		return result, fmt.Errorf("failed: check source worktree: %w", err)
	} else if strings.TrimSpace(string(status)) != "" {
		return result, fmt.Errorf("dirty_worktree: source worktree is not clean")
	}
	branch, err := gitOutput(ctx, worktreePath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(branch)), "drift/") {
		return result, fmt.Errorf("invalid_worktree: source worktree is not a managed branch")
	}
	branchName := strings.TrimSpace(string(branch))
	before, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return result, fmt.Errorf("failed: read main HEAD: %w", err)
	}
	result.BeforeHEAD = strings.TrimSpace(string(before))
	source, err := gitOutput(ctx, worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return result, fmt.Errorf("failed: read source HEAD: %w", err)
	}
	preflight, preflightErr := gitOutput(ctx, root, "merge-tree", "--write-tree", "HEAD", strings.TrimSpace(string(source)))
	if preflightErr != nil {
		result.Status = "conflict"
		result.FailureReason = "conflict"
		result.Conflicts = conflictPaths(string(preflight))
		return result, nil
	}
	if _, err := gitOutput(ctx, root, "merge", "--no-ff", "--no-edit", branchName); err != nil {
		result.Status = "failed"
		result.FailureReason = "merge_failed"
		return result, err
	}
	after, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		result.Status = "failed"
		result.FailureReason = "failed"
		return result, err
	}
	result.Status = "success"
	result.AfterHEAD = strings.TrimSpace(string(after))
	return result, nil
}

var conflictPathPattern = regexp.MustCompile(`(?m)^CONFLICT \([^)]*\): Merge conflict in (.+)$`)

func conflictPaths(output string) []string {
	matches := conflictPathPattern.FindAllStringSubmatch(output, -1)
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			paths = append(paths, strings.TrimSpace(match[1]))
		}
	}
	return paths
}
