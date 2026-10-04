package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

// TaskState is the durable state of one task in a chat session.
type TaskState struct {
	ID             string   `json:"id"`
	Subject        string   `json:"subject"`
	Description    string   `json:"description,omitempty"`
	Status         string   `json:"status"`
	Worktree       string   `json:"worktree,omitempty"`
	DependsOn      []string `json:"depends_on,omitempty"`
	Result         string   `json:"result,omitempty"`
	Error          string   `json:"error,omitempty"`
	MergeStatus    string   `json:"merge_status,omitempty"`
	MergeCommit    string   `json:"merge_commit,omitempty"`
	MergeConflicts []string `json:"merge_conflicts,omitempty"`
}

var taskWorktreePattern = regexp.MustCompile(`^\.worktrees/[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// TaskRegistry exposes the task state owned by a chat tool registry.
type TaskRegistry interface {
	Registry
	ExportTasks() []TaskState
	RestoreTasks([]TaskState) error
	ResetTasks()
	SetTaskRunner(TaskRunner)
	SetTaskMerger(TaskMerger)
	SetCoordinator(CoordinatorRunner)
}

// TaskSwitcher resolves the active task's worktree for the Agent runner.
type TaskSwitcher interface {
	ActiveWorktree(root string) (string, error)
}

type TaskExecutionResult struct {
	State  string
	Output string
	Err    error
}

type TaskMergeResult struct {
	Status    string
	AfterHEAD string
	Conflicts []string
}

type TaskMerger func(context.Context, string, TaskState) (TaskMergeResult, error)

type TaskHandle interface {
	Cancel()
	Wait(context.Context) (TaskExecutionResult, error)
}

type TaskRunner func(context.Context, TaskState, string, time.Duration) (TaskHandle, error)

// CoordinatorRunner executes a plan's task graph and owns scheduling policy.
// The interface keeps the tool package independent from the coordinator package.
type CoordinatorRunner interface {
	Run(context.Context, string, []TaskState) ([]TaskState, error)
	Cancel()
	Status() []TaskState
}

type taskStore struct {
	mu          sync.Mutex
	next        int
	tasks       map[string]TaskState
	active      string
	runner      TaskRunner
	merger      TaskMerger
	coordinator CoordinatorRunner
	running     map[string]TaskHandle
}

func newTaskStore() *taskStore {
	return &taskStore{tasks: make(map[string]TaskState), running: make(map[string]TaskHandle)}
}

type taskCreateTool struct{ store *taskStore }
type taskListTool struct{ store *taskStore }
type taskGetTool struct{ store *taskStore }
type taskUpdateTool struct{ store *taskStore }
type taskSwitchTool struct{ store *taskStore }
type taskRunTool struct{ store *taskStore }
type taskStatusTool struct{ store *taskStore }
type taskCancelTool struct{ store *taskStore }
type taskMergeTool struct{ store *taskStore }

func (taskCreateTool) Name() string { return "TaskCreate" }
func (taskListTool) Name() string   { return "TaskList" }
func (taskGetTool) Name() string    { return "TaskGet" }
func (taskUpdateTool) Name() string { return "TaskUpdate" }
func (taskSwitchTool) Name() string { return "TaskSwitch" }
func (taskRunTool) Name() string    { return "TaskRun" }
func (taskStatusTool) Name() string { return "TaskStatus" }
func (taskCancelTool) Name() string { return "TaskCancel" }
func (taskMergeTool) Name() string  { return "TaskMerge" }

func (t taskCreateTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Create a session task.", map[string]any{
		"subject":     map[string]any{"type": "string", "description": "Short task title."},
		"description": map[string]any{"type": "string", "description": "Optional task details."},
		"worktree":    map[string]any{"type": "string", "description": "Optional managed worktree path, for example .worktrees/agent-1."},
		"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 32},
	}, []string{"subject"})
}

func (t taskListTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "List tasks in the current chat session.", map[string]any{}, nil)
}

func (t taskGetTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Get one task from the current chat session.", map[string]any{
		"task_id": map[string]any{"type": "string"},
	}, []string{"task_id"})
}

func (t taskUpdateTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Update a task status or description.", map[string]any{
		"task_id":     map[string]any{"type": "string"},
		"status":      map[string]any{"type": "string", "enum": []string{"pending", "blocked", "running", "completed", "failed", "cancelled", "timeout"}},
		"description": map[string]any{"type": "string"},
		"worktree":    map[string]any{"type": "string", "description": "Optional managed worktree path."},
		"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 32},
	}, []string{"task_id"})
}

func (t taskSwitchTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Switch the Agent to a task's existing managed worktree.", map[string]any{
		"task_id": map[string]any{"type": "string"},
	}, []string{"task_id"})
}

func (t taskRunTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Run one child Agent in the task's existing worktree.", map[string]any{
		"task_id":    map[string]any{"type": "string"},
		"prompt":     map[string]any{"type": "string"},
		"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 600000},
	}, []string{"task_id", "prompt"})
}

func (t taskStatusTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Read one task and its child Agent status.", map[string]any{"task_id": map[string]any{"type": "string"}}, []string{"task_id"})
}

func (t taskCancelTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Cancel one running child Agent.", map[string]any{"task_id": map[string]any{"type": "string"}}, []string{"task_id"})
}

func (t taskMergeTool) Definition() llm.ToolDefinition {
	return controlDefinition(t.Name(), "Retry a queued task worktree merge after conflicts have been resolved.", map[string]any{
		"task_id": map[string]any{"type": "string"},
	}, []string{"task_id"})
}

func (t taskCreateTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		Subject     string   `json:"subject"`
		Description string   `json:"description"`
		Worktree    string   `json:"worktree"`
		DependsOn   []string `json:"depends_on"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskCreate arguments: %w", err)
	}
	args.Subject = strings.TrimSpace(args.Subject)
	args.Description = strings.TrimSpace(args.Description)
	args.Worktree = normalizeTaskWorktree(args.Worktree)
	args.DependsOn = normalizeTaskDependencies(args.DependsOn)
	if args.Subject == "" || len([]rune(args.Subject)) > 200 || len([]rune(args.Description)) > 2000 || len(args.DependsOn) > 32 || !validTaskDependencies(args.DependsOn) || (args.Worktree != "" && !validTaskWorktree(args.Worktree)) {
		return "", fmt.Errorf("TaskCreate arguments are invalid")
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	for _, dependency := range args.DependsOn {
		if _, exists := t.store.tasks[dependency]; !exists {
			return "", fmt.Errorf("TaskCreate dependency %q not found", dependency)
		}
	}
	t.store.next++
	item := TaskState{ID: fmt.Sprintf("task-%d", t.store.next), Subject: args.Subject, Description: args.Description, Status: "pending", Worktree: args.Worktree, DependsOn: args.DependsOn}
	t.store.tasks[item.ID] = item
	return formatTask(item), nil
}

func (t taskListTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct{}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", err
	}
	t.store.mu.Lock()
	items := make([]TaskState, 0, len(t.store.tasks))
	for _, item := range t.store.tasks {
		items = append(items, item)
	}
	t.store.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if len(items) == 0 {
		return "TaskList: no tasks", nil
	}
	var out strings.Builder
	for index, item := range items {
		if index > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(formatTask(item))
	}
	return out.String(), nil
}

func (t taskGetTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskGet arguments: %w", err)
	}
	return t.store.get(strings.TrimSpace(args.TaskID))
}

func (t taskUpdateTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID      string   `json:"task_id"`
		Status      string   `json:"status"`
		Description string   `json:"description"`
		Worktree    string   `json:"worktree"`
		DependsOn   []string `json:"depends_on"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskUpdate arguments: %w", err)
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	args.Status = strings.TrimSpace(args.Status)
	args.Description = strings.TrimSpace(args.Description)
	args.Worktree = normalizeTaskWorktree(args.Worktree)
	args.DependsOn = normalizeTaskDependencies(args.DependsOn)
	if args.Status == "in_progress" {
		args.Status = "running"
	}
	if args.Status != "" && !validTaskStatus(args.Status) || len([]rune(args.Description)) > 2000 || len(args.DependsOn) > 32 || !validTaskDependencies(args.DependsOn) || (args.Worktree != "" && !validTaskWorktree(args.Worktree)) {
		return "", fmt.Errorf("TaskUpdate arguments are invalid")
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	item, ok := t.store.tasks[args.TaskID]
	if !ok {
		return "", fmt.Errorf("TaskUpdate task %q not found", args.TaskID)
	}
	if args.DependsOn != nil {
		for _, dependency := range args.DependsOn {
			if _, exists := t.store.tasks[dependency]; !exists {
				return "", fmt.Errorf("TaskUpdate dependency %q not found", dependency)
			}
		}
		candidate := item
		candidate.DependsOn = args.DependsOn
		t.store.tasks[item.ID] = candidate
		if hasDependencyCycleLocked(t.store.tasks, item.ID) {
			t.store.tasks[item.ID] = item
			return "", fmt.Errorf("TaskUpdate dependency cycle detected")
		}
		item = candidate
	}
	if args.Status != "" {
		item.Status = args.Status
	}
	if args.Description != "" {
		item.Description = args.Description
	}
	if args.Worktree != "" {
		item.Worktree = args.Worktree
	}
	t.store.tasks[item.ID] = item
	return formatTask(item), nil
}

func (t taskSwitchTool) Execute(_ context.Context, root string, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskSwitch arguments: %w", err)
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	t.store.mu.Lock()
	item, ok := t.store.tasks[args.TaskID]
	if ok {
		t.store.active = item.ID
	}
	t.store.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("TaskSwitch task %q not found", args.TaskID)
	}
	if item.Worktree == "" {
		return "", fmt.Errorf("TaskSwitch task %q has no worktree", args.TaskID)
	}
	path := filepath.Join(root, filepath.FromSlash(item.Worktree))
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("TaskSwitch worktree unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("TaskSwitch worktree is not a directory")
	}
	return formatTask(item), nil
}

func (t taskRunTool) Execute(ctx context.Context, root, raw string) (string, error) {
	var args struct {
		TaskID    string `json:"task_id"`
		Prompt    string `json:"prompt"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskRun arguments: %w", err)
	}
	args.TaskID, args.Prompt = strings.TrimSpace(args.TaskID), strings.TrimSpace(args.Prompt)
	if args.TaskID == "" || args.Prompt == "" || len([]rune(args.Prompt)) > 4000 || args.TimeoutMS < 0 || args.TimeoutMS > 600000 {
		return "", fmt.Errorf("TaskRun arguments are invalid")
	}
	t.store.mu.Lock()
	item, ok := t.store.tasks[args.TaskID]
	runner := t.store.runner
	if ok && (item.Status == "running" || item.Status == "in_progress") {
		t.store.mu.Unlock()
		return "", fmt.Errorf("TaskRun task %q is already running", args.TaskID)
	}
	if ok {
		if !dependenciesCompletedLocked(t.store.tasks, item) {
			item.Status = "blocked"
			t.store.tasks[item.ID] = item
			t.store.mu.Unlock()
			return "", fmt.Errorf("TaskRun task %q is blocked by dependencies", args.TaskID)
		}
		item.Status = "running"
		t.store.tasks[item.ID] = item
	}
	t.store.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("TaskRun task %q not found", args.TaskID)
	}
	if item.Worktree == "" {
		t.store.fail(args.TaskID, "task has no worktree")
		return "", fmt.Errorf("TaskRun task %q has no worktree", args.TaskID)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(item.Worktree))); err != nil {
		t.store.fail(args.TaskID, err.Error())
		return "", fmt.Errorf("TaskRun worktree unavailable: %w", err)
	}
	if runner == nil {
		t.store.fail(args.TaskID, "TaskRun is unavailable in this host")
		return "", fmt.Errorf("TaskRun is unavailable in this host")
	}
	timeout := time.Duration(args.TimeoutMS) * time.Millisecond
	// TaskRun is asynchronous: the model tool-call context ends when this tool
	// returns, so it must not cancel the child. Child cancellation is owned by
	// TaskCancel or the runner's explicit timeout.
	handle, err := runner(context.WithoutCancel(ctx), item, args.Prompt, timeout)
	if err != nil {
		t.store.mu.Lock()
		item.Status = "failed"
		t.store.tasks[item.ID] = item
		t.store.mu.Unlock()
		return "", err
	}
	t.store.mu.Lock()
	t.store.running[item.ID] = handle
	t.store.mu.Unlock()
	go t.store.finishTask(context.Background(), root, item.ID, handle)
	return formatTask(item), nil
}

func (t taskStatusTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskStatus arguments: %w", err)
	}
	return t.store.get(strings.TrimSpace(args.TaskID))
}

func (t taskCancelTool) Execute(_ context.Context, _ string, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskCancel arguments: %w", err)
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	t.store.mu.Lock()
	item, ok := t.store.tasks[args.TaskID]
	handle := t.store.running[args.TaskID]
	t.store.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("TaskCancel task %q not found", args.TaskID)
	}
	if handle == nil {
		return "", fmt.Errorf("TaskCancel task %q is not running", args.TaskID)
	}
	handle.Cancel()
	return "cancel requested: " + formatTask(item), nil
}

func (t taskMergeTool) Execute(ctx context.Context, root, raw string) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeTaskArgs(raw, &args); err != nil {
		return "", fmt.Errorf("decode TaskMerge arguments: %w", err)
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	t.store.mu.Lock()
	item, ok := t.store.tasks[args.TaskID]
	merger := t.store.merger
	t.store.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("TaskMerge task %q not found", args.TaskID)
	}
	if item.MergeStatus != "conflict" {
		return "", fmt.Errorf("TaskMerge task %q has no queued conflict", args.TaskID)
	}
	if merger == nil {
		return "", fmt.Errorf("TaskMerge is unavailable in this host")
	}
	result, err := merger(ctx, root, item)
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	item, ok = t.store.tasks[args.TaskID]
	if !ok {
		return "", fmt.Errorf("TaskMerge task %q disappeared", args.TaskID)
	}
	if err != nil {
		item.MergeStatus = "failed"
		item.Error = err.Error()
	} else {
		switch result.Status {
		case "success":
			item.MergeStatus = "merged"
			item.MergeCommit = result.AfterHEAD
			item.MergeConflicts = nil
		case "conflict":
			item.MergeStatus = "conflict"
			item.MergeConflicts = append([]string(nil), result.Conflicts...)
		default:
			item.MergeStatus = "failed"
		}
	}
	t.store.tasks[args.TaskID] = item
	return formatTask(item), err
}

func (s *taskStore) get(id string) (string, error) {
	s.mu.Lock()
	item, ok := s.tasks[id]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("TaskGet task %q not found", id)
	}
	return formatTask(item), nil
}

func (s *taskStore) syncPlanTasks(tasks []PlanTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	known := make(map[string]struct{}, len(s.tasks)+len(tasks))
	for id := range s.tasks {
		known[id] = struct{}{}
	}
	for _, planTask := range tasks {
		known[planTask.ID] = struct{}{}
	}
	for _, planTask := range tasks {
		if !strings.HasPrefix(planTask.ID, "task-") || !validPlanStatus(planTask.Status) {
			return fmt.Errorf("plan task %q is invalid", planTask.ID)
		}
		for _, dependency := range planTask.Dependencies {
			if _, ok := known[dependency]; !ok {
				return fmt.Errorf("plan task %q depends on unknown task %q", planTask.ID, dependency)
			}
		}
	}
	candidate := make(map[string]TaskState, len(s.tasks))
	for id, item := range s.tasks {
		candidate[id] = item
	}
	for _, planTask := range tasks {
		description := planTask.Description
		if description == "" {
			description = planTask.Title
		}
		candidate[planTask.ID] = TaskState{ID: planTask.ID, Subject: planTask.Title, Description: description, Status: planTask.Status, Worktree: planTask.Worktree, DependsOn: append([]string(nil), planTask.Dependencies...)}
	}
	for _, planTask := range tasks {
		if hasDependencyCycleLocked(candidate, planTask.ID) {
			return fmt.Errorf("plan task %q has dependency cycle", planTask.ID)
		}
		if existing, ok := s.tasks[planTask.ID]; ok {
			existing.Subject, existing.Description, existing.Worktree = planTask.Title, planTask.Description, planTask.Worktree
			if existing.Description == "" {
				existing.Description = planTask.Title
			}
			existing.DependsOn, existing.Status = append([]string(nil), planTask.Dependencies...), planTask.Status
			s.tasks[planTask.ID] = existing
			continue
		}
		description := planTask.Description
		if description == "" {
			description = planTask.Title
		}
		s.tasks[planTask.ID] = TaskState{ID: planTask.ID, Subject: planTask.Title, Description: description, Status: planTask.Status, Worktree: planTask.Worktree, DependsOn: append([]string(nil), planTask.Dependencies...)}
		if number, err := strconv.Atoi(strings.TrimPrefix(planTask.ID, "task-")); err == nil && number > s.next {
			s.next = number
		}
	}
	return nil
}

func (s *taskStore) state(id string) (TaskState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.tasks[id]
	return item, ok
}

func (s *taskStore) activeWorktree(root string) (string, error) {
	s.mu.Lock()
	active := s.active
	item, ok := s.tasks[active]
	s.mu.Unlock()
	if !ok || item.Worktree == "" {
		return "", nil
	}
	path := filepath.Join(root, filepath.FromSlash(item.Worktree))
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("active task worktree unavailable")
	}
	return filepath.Abs(path)
}

func (r *registry) SetTaskRunner(runner TaskRunner) {
	r.tasks.mu.Lock()
	r.tasks.runner = runner
	r.tasks.mu.Unlock()
}

func (r *registry) SetTaskMerger(merger TaskMerger) {
	r.tasks.mu.Lock()
	r.tasks.merger = merger
	r.tasks.mu.Unlock()
}

func (r *registry) SetCoordinator(coordinator CoordinatorRunner) {
	r.tasks.mu.Lock()
	r.tasks.coordinator = coordinator
	r.tasks.mu.Unlock()
}

func (s *taskStore) applyCoordinatorStates(states []TaskState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range states {
		if _, ok := s.tasks[state.ID]; ok {
			s.tasks[state.ID] = state
		}
	}
}

func (s *taskStore) finishTask(ctx context.Context, root, id string, handle TaskHandle) {
	result, waitErr := handle.Wait(ctx)
	s.mu.Lock()
	item, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.running, id)
	if waitErr != nil {
		item.Status = "failed"
		item.Error = waitErr.Error()
	} else if result.State != "" {
		item.Status = result.State
		item.Result = result.Output
		if result.Err != nil {
			item.Error = result.Err.Error()
		}
	} else {
		item.Status, item.Result = "completed", result.Output
		if result.Err != nil {
			item.Error = result.Err.Error()
		}
	}
	s.tasks[id] = item
	merger := s.merger
	shouldMerge := waitErr == nil && item.Status == "completed" && merger != nil && item.Worktree != ""
	s.mu.Unlock()
	if !shouldMerge {
		return
	}
	merge, mergeErr := merger(ctx, root, item)
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok = s.tasks[id]
	if !ok {
		return
	}
	if mergeErr != nil {
		item.MergeStatus = "failed"
		item.Error = mergeErr.Error()
	} else {
		switch merge.Status {
		case "success":
			item.MergeStatus = "merged"
			item.MergeCommit = merge.AfterHEAD
		case "conflict":
			item.MergeStatus = "conflict"
			item.MergeConflicts = append([]string(nil), merge.Conflicts...)
		default:
			item.MergeStatus = "failed"
		}
	}
	s.tasks[id] = item
}

func (s *taskStore) fail(id, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.tasks[id]
	if !ok {
		return
	}
	item.Status = "failed"
	item.Error = message
	s.tasks[id] = item
}

func validTaskStatus(status string) bool {
	switch status {
	case "pending", "blocked", "running", "in_progress", "completed", "failed", "cancelled", "timeout":
		return true
	default:
		return false
	}
}

func formatTask(item TaskState) string {
	result := item.ID + " · " + item.Status + " · " + item.Subject
	if item.Description != "" {
		result += "\n" + item.Description
	}
	if item.Worktree != "" {
		result += "\nworktree: " + item.Worktree
	}
	if len(item.DependsOn) > 0 {
		result += "\ndepends_on: " + strings.Join(item.DependsOn, ", ")
	}
	if item.Result != "" {
		result += "\nresult: " + item.Result
	}
	if item.Error != "" {
		result += "\nerror: " + item.Error
	}
	if item.MergeStatus != "" {
		result += "\nmerge: " + item.MergeStatus
	}
	if item.MergeCommit != "" {
		result += "\nmerge commit: " + item.MergeCommit
	}
	if len(item.MergeConflicts) > 0 {
		result += "\nmerge conflicts: " + strings.Join(item.MergeConflicts, ", ")
	}
	return result
}

// ValidTaskStatus reports whether a persisted task status is supported.
func ValidTaskStatus(status string) bool { return validTaskStatus(status) }

// ValidateTaskState validates state crossing the session persistence boundary.
func ValidateTaskState(item TaskState) bool {
	if item.Subject == "" || len([]rune(item.Subject)) > 200 || len([]rune(item.Description)) > 2000 || len(item.DependsOn) > 32 || !validTaskDependencies(item.DependsOn) || len([]rune(item.Result)) > 8000 || len([]rune(item.Error)) > 2000 || len([]rune(item.MergeCommit)) > 200 || len(item.MergeConflicts) > 32 || !validTaskStatus(item.Status) || !validMergeStatus(item.MergeStatus) || !strings.HasPrefix(item.ID, "task-") || (item.Worktree != "" && !validTaskWorktree(item.Worktree)) {
		return false
	}
	number, err := strconv.Atoi(strings.TrimPrefix(item.ID, "task-"))
	return err == nil && number > 0
}

func validMergeStatus(status string) bool {
	switch status {
	case "", "queued", "merged", "conflict", "failed":
		return true
	default:
		return false
	}
}

func normalizeTaskWorktree(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func normalizeTaskDependencies(dependencies []string) []string {
	if dependencies == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(dependencies))
	result := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		dependency = strings.TrimSpace(dependency)
		if dependency == "" {
			continue
		}
		if _, exists := seen[dependency]; exists {
			continue
		}
		seen[dependency] = struct{}{}
		result = append(result, dependency)
	}
	sort.Strings(result)
	return result
}

func validTaskDependencies(dependencies []string) bool {
	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if !validTaskID(dependency) {
			return false
		}
		if _, exists := seen[dependency]; exists {
			return false
		}
		seen[dependency] = struct{}{}
	}
	return true
}

func validTaskID(id string) bool {
	if !strings.HasPrefix(id, "task-") {
		return false
	}
	number, err := strconv.Atoi(strings.TrimPrefix(id, "task-"))
	return err == nil && number > 0
}

func dependenciesCompletedLocked(tasks map[string]TaskState, item TaskState) bool {
	for _, dependency := range item.DependsOn {
		state, ok := tasks[dependency]
		if !ok || state.Status != "completed" {
			return false
		}
	}
	return true
}

func hasDependencyCycleLocked(tasks map[string]TaskState, start string) bool {
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, dependency := range tasks[id].DependsOn {
			if visit(dependency) {
				return true
			}
		}
		delete(visiting, id)
		visited[id] = true
		return false
	}
	return visit(start)
}

func validTaskWorktree(path string) bool { return taskWorktreePattern.MatchString(path) }

func (s *taskStore) export() []TaskState {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]TaskState, 0, len(s.tasks))
	for _, item := range s.tasks {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (s *taskStore) restore(items []TaskState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := 0
	restored := make(map[string]TaskState, len(items))
	for _, item := range items {
		if !ValidateTaskState(item) {
			return fmt.Errorf("invalid task state")
		}
		item.DependsOn = normalizeTaskDependencies(item.DependsOn)
		if item.Status == "in_progress" {
			item.Status = "running"
		}
		var number int
		number, _ = strconv.Atoi(strings.TrimPrefix(item.ID, "task-"))
		if _, exists := restored[item.ID]; exists {
			return fmt.Errorf("duplicate task id %q", item.ID)
		}
		if number > next {
			next = number
		}
		restored[item.ID] = item
	}
	for id, item := range restored {
		for _, dependency := range item.DependsOn {
			if _, exists := restored[dependency]; !exists {
				return fmt.Errorf("task %q dependency %q not found", id, dependency)
			}
		}
		if hasDependencyCycleLocked(restored, id) {
			return fmt.Errorf("task dependency cycle detected")
		}
	}
	for id, item := range restored {
		if item.Status == "running" {
			item.Status = "pending"
		}
		if item.Status == "pending" || item.Status == "blocked" {
			if dependenciesCompletedLocked(restored, item) {
				item.Status = "pending"
			} else {
				item.Status = "blocked"
			}
		}
		restored[id] = item
	}
	s.tasks = restored
	s.next = next
	s.active = ""
	return nil
}

func (s *taskStore) reset() {
	s.mu.Lock()
	s.tasks = make(map[string]TaskState)
	s.next = 0
	s.active = ""
	s.mu.Unlock()
}

func decodeTaskArgs(raw string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}
