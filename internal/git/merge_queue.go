package git

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// MergeRequest is a conflict waiting for an explicit retry after resolution.
type MergeRequest struct {
	TaskID    string
	Worktree  string
	Status    string
	Conflicts []string
	Error     string
}

// MergeQueue serializes merge decisions while reusing MergeWorktree's safety checks.
// It never stashes, resets, cleans, or deletes a worktree.
type MergeQueue struct {
	mu      sync.Mutex
	pending map[string]MergeRequest
}

func NewMergeQueue() *MergeQueue { return &MergeQueue{pending: make(map[string]MergeRequest)} }

// Submit attempts one automatic merge. A conflict is retained for retry; other
// safety failures are returned without changing the queue or main worktree.
func (q *MergeQueue) Submit(ctx context.Context, root, taskID, worktree string) (MergeResult, error) {
	q.mu.Lock()
	if q.pending == nil {
		q.pending = make(map[string]MergeRequest)
	}
	if _, exists := q.pending[taskID]; exists {
		q.mu.Unlock()
		return MergeResult{}, fmt.Errorf("merge task %q already queued", taskID)
	}
	q.mu.Unlock()
	name, err := managedWorktreeName(root, worktree)
	if err != nil {
		return MergeResult{}, err
	}
	result, err := MergeWorktree(ctx, root, name)
	if err != nil {
		return result, err
	}
	if result.Status == "conflict" {
		q.mu.Lock()
		q.pending[taskID] = MergeRequest{TaskID: taskID, Worktree: worktree, Status: "conflict", Conflicts: append([]string(nil), result.Conflicts...)}
		q.mu.Unlock()
	}
	return result, nil
}

// Retry re-runs a queued conflict after the source worktree has been resolved.
func (q *MergeQueue) Retry(ctx context.Context, root, taskID string) (MergeResult, error) {
	q.mu.Lock()
	request, ok := q.pending[taskID]
	q.mu.Unlock()
	if !ok {
		return MergeResult{}, fmt.Errorf("merge task %q is not queued", taskID)
	}
	name, err := managedWorktreeName(root, request.Worktree)
	if err != nil {
		return MergeResult{}, err
	}
	result, err := MergeWorktree(ctx, root, name)
	if err != nil {
		return result, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if result.Status == "success" {
		delete(q.pending, taskID)
	} else if result.Status == "conflict" {
		request.Conflicts = append([]string(nil), result.Conflicts...)
		q.pending[taskID] = request
	}
	return result, nil
}

func (q *MergeQueue) Pending() []MergeRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]MergeRequest, 0, len(q.pending))
	for _, request := range q.pending {
		request.Conflicts = append([]string(nil), request.Conflicts...)
		result = append(result, request)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TaskID < result[j].TaskID })
	return result
}

func managedWorktreeName(root, worktree string) (string, error) {
	root, err := RepositoryRoot(context.Background(), root)
	if err != nil {
		return "", err
	}
	managed := filepath.Join(root, ".worktrees")
	absolute, err := filepath.Abs(worktree)
	if err != nil {
		return "", fmt.Errorf("invalid_worktree: %w", err)
	}
	rel, err := filepath.Rel(managed, absolute)
	if err != nil || rel == "." || strings.Contains(rel, string(filepath.Separator)) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid_worktree: path is outside managed worktrees")
	}
	return rel, nil
}
