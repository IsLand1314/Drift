package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/tool"
)

type Options struct {
	MaxConcurrency int
	MaxRetries     int
	RetryBackoff   time.Duration
}

type Coordinator struct {
	mu          sync.Mutex
	tasks       map[string]tool.TaskState
	order       []string
	runner      tool.TaskRunner
	merger      tool.TaskMerger
	max         int
	maxRetries  int
	root        string
	taskTimeout time.Duration
	cancel      context.CancelFunc
	active      map[string]tool.TaskHandle
	retries     map[string]int
	stateSink   func([]tool.TaskState)
}

type completedTask struct {
	id     string
	result tool.TaskExecutionResult
	err    error
}

func New(tasks []tool.TaskState, runner tool.TaskRunner, merger tool.TaskMerger, options Options) *Coordinator {
	max := options.MaxConcurrency
	if max < 1 {
		max = 1
	}
	if max > 4 {
		max = 4
	}
	if options.MaxRetries < 0 {
		options.MaxRetries = 0
	}
	c := &Coordinator{tasks: make(map[string]tool.TaskState, len(tasks)), runner: runner, merger: merger, max: max, maxRetries: options.MaxRetries, active: make(map[string]tool.TaskHandle), retries: make(map[string]int)}
	for _, task := range tasks {
		c.tasks[task.ID] = task
		c.order = append(c.order, task.ID)
	}
	return c
}

// Restore replaces a scheduler snapshot. Interrupted active tasks are safe to
// retry; completed and failed terminal states remain unchanged.
func (c *Coordinator) Restore(tasks []tool.TaskState) error {
	c.mu.Lock()
	if len(c.active) != 0 {
		c.mu.Unlock()
		return fmt.Errorf("cannot restore while coordinator is running")
	}
	next := make(map[string]tool.TaskState, len(tasks))
	order := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == "" {
			c.mu.Unlock()
			return fmt.Errorf("task id is required")
		}
		if _, exists := next[task.ID]; exists {
			c.mu.Unlock()
			return fmt.Errorf("duplicate task %q", task.ID)
		}
		if task.Status == "running" || task.Status == "in_progress" {
			task.Status = "pending"
		}
		next[task.ID] = task
		order = append(order, task.ID)
	}
	c.tasks, c.order, c.retries = next, order, make(map[string]int)
	sink := c.stateSink
	snapshot := c.snapshotLocked()
	c.mu.Unlock()
	if sink != nil {
		sink(snapshot)
	}
	return nil
}

func (c *Coordinator) setStateSink(sink func([]tool.TaskState)) {
	c.mu.Lock()
	c.stateSink = sink
	c.mu.Unlock()
}

func (c *Coordinator) emit() {
	c.mu.Lock()
	sink := c.stateSink
	snapshot := c.snapshotLocked()
	c.mu.Unlock()
	if sink != nil {
		sink(snapshot)
	}
}

func (c *Coordinator) Status() []tool.TaskState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Coordinator) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	handles := make([]tool.TaskHandle, 0, len(c.active))
	for _, handle := range c.active {
		handles = append(handles, handle)
	}
	for id, task := range c.tasks {
		if task.Status == "pending" || task.Status == "running" || task.Status == "in_progress" {
			task.Status = "cancelled"
			task.Error = "coordinator cancelled"
			c.tasks[id] = task
		}
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, handle := range handles {
		handle.Cancel()
	}
	c.emit()
}

func (c *Coordinator) Run(parent context.Context, root string) ([]tool.TaskState, error) {
	return c.run(parent, root, 0)
}

// RunWithTimeout applies one timeout budget to every child task in this plan.
func (c *Coordinator) RunWithTimeout(parent context.Context, root string, timeout time.Duration) ([]tool.TaskState, error) {
	return c.run(parent, root, timeout)
}

func (c *Coordinator) run(parent context.Context, root string, timeout time.Duration) ([]tool.TaskState, error) {
	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	c.cancel = cancel
	c.root = root
	c.taskTimeout = timeout
	c.mu.Unlock()
	c.emit()
	defer func() {
		cancel()
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
	}()
	completed := make(chan completedTask, len(c.tasks))
	for {
		c.blockFailedDependents()
		c.mu.Lock()
		if c.runner == nil {
			for id, task := range c.tasks {
				if task.Status == "pending" || task.Status == "ready" {
					task.Status, task.Error = "failed", "coordinator runner unavailable"
					c.tasks[id] = task
				}
			}
			snapshot := c.snapshotLocked()
			c.mu.Unlock()
			c.emit()
			return snapshot, fmt.Errorf("coordinator runner unavailable")
		}
		retryScheduled := false
		for _, id := range c.order {
			if len(c.active) >= c.max {
				break
			}
			task := c.tasks[id]
			if task.Status != "pending" || !c.dependenciesCompletedLocked(task) {
				continue
			}
			task.Status = "running"
			c.tasks[id] = task
			handle, err := c.runner(ctx, task, task.Description, c.taskTimeout)
			if err != nil {
				if c.shouldRetryLocked(id, "failed", err) {
					task.Status, task.Error = "pending", err.Error()
					c.tasks[id] = task
					retryScheduled = true
					continue
				}
				task.Status, task.Error = "failed", err.Error()
				c.tasks[id] = task
				continue
			}
			c.active[id] = handle
			go func(id string, handle tool.TaskHandle) {
				result, err := handle.Wait(ctx)
				completed <- completedTask{id: id, result: result, err: err}
			}(id, handle)
		}
		if len(c.active) == 0 {
			if retryScheduled {
				c.mu.Unlock()
				c.emit()
				continue
			}
			failed := false
			pending := false
			for _, task := range c.tasks {
				switch task.Status {
				case "failed", "blocked", "cancelled", "timeout":
					failed = true
				case "pending", "ready", "running", "in_progress":
					pending = true
				}
				if task.MergeStatus == "conflict" || task.MergeStatus == "failed" {
					failed = true
				}
			}
			snapshot := c.snapshotLocked()
			c.mu.Unlock()
			c.emit()
			if failed || pending {
				return snapshot, fmt.Errorf("coordinator stopped before all tasks completed")
			}
			return snapshot, nil
		}
		c.mu.Unlock()
		c.emit()
		select {
		case result := <-completed:
			c.finish(result)
		case <-ctx.Done():
			c.Cancel()
			return c.Status(), ctx.Err()
		}
	}
}

func (c *Coordinator) finish(result completedTask) {
	c.mu.Lock()
	task, ok := c.tasks[result.id]
	if !ok {
		c.mu.Unlock()
		return
	}
	delete(c.active, result.id)
	if task.Status == "cancelled" {
		c.tasks[result.id] = task
		c.mu.Unlock()
		c.emit()
		return
	}
	if result.err != nil {
		if c.shouldRetryLocked(result.id, "failed", result.err) {
			task.Status, task.Error = "pending", result.err.Error()
			c.tasks[result.id] = task
			c.mu.Unlock()
			c.emit()
			return
		}
		task.Status, task.Error = "failed", result.err.Error()
	} else if result.result.State != "" {
		if c.shouldRetryLocked(result.id, result.result.State, result.result.Err) {
			task.Status, task.Error = "pending", result.result.Err.Error()
			c.tasks[result.id] = task
			c.mu.Unlock()
			c.emit()
			return
		}
		task.Status, task.Result = result.result.State, result.result.Output
		if result.result.Err != nil {
			task.Error = result.result.Err.Error()
		}
	} else {
		task.Status, task.Result = "completed", result.result.Output
	}
	merger := c.merger
	c.tasks[result.id] = task
	c.mu.Unlock()
	if task.Status == "completed" && merger != nil && task.Worktree != "" {
		merge, err := merger(context.Background(), c.root, task)
		c.mu.Lock()
		task = c.tasks[result.id]
		if err != nil {
			task.MergeStatus, task.Error = "failed", err.Error()
		} else {
			switch merge.Status {
			case "success", "merged":
				task.MergeStatus = "merged"
			case "conflict":
				task.MergeStatus = "conflict"
			default:
				task.MergeStatus = "failed"
			}
			task.MergeCommit, task.MergeConflicts = merge.AfterHEAD, append([]string(nil), merge.Conflicts...)
		}
		c.tasks[result.id] = task
		c.mu.Unlock()
		c.emit()
		return
	}
	c.emit()
}

func (c *Coordinator) shouldRetryLocked(id, state string, err error) bool {
	if c.retries[id] >= c.maxRetries || !retryable(state, err) {
		return false
	}
	c.retries[id]++
	return true
}

func retryable(state string, err error) bool {
	if state == "timeout" {
		return true
	}
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") || strings.Contains(message, "temporary") || strings.Contains(message, "transient")
}

// Runner adapts a coordinator instance to the tool registry. A fresh scheduler
// is created per PlanExecute call, while Cancel/Status address the active run.
type Runner struct {
	mu        sync.Mutex
	current   *Coordinator
	runner    tool.TaskRunner
	merger    tool.TaskMerger
	options   Options
	stateSink func([]tool.TaskState)
}

func NewRunner(runner tool.TaskRunner, merger tool.TaskMerger, options Options) *Runner {
	return &Runner{runner: runner, merger: merger, options: options}
}

func (r *Runner) SetStateSink(sink func([]tool.TaskState)) {
	r.mu.Lock()
	r.stateSink = sink
	r.mu.Unlock()
}

func (r *Runner) Run(ctx context.Context, root string, tasks []tool.TaskState) ([]tool.TaskState, error) {
	return r.run(ctx, root, tasks, 0)
}

func (r *Runner) RunWithTimeout(ctx context.Context, root string, tasks []tool.TaskState, timeout time.Duration) ([]tool.TaskState, error) {
	return r.run(ctx, root, tasks, timeout)
}

func (r *Runner) run(ctx context.Context, root string, tasks []tool.TaskState, timeout time.Duration) ([]tool.TaskState, error) {
	c := New(tasks, r.runner, r.merger, r.options)
	r.mu.Lock()
	r.current = c
	sink := r.stateSink
	r.mu.Unlock()
	c.setStateSink(sink)
	defer func() {
		r.mu.Lock()
		if r.current == c {
			r.current = nil
		}
		r.mu.Unlock()
	}()
	return c.run(ctx, root, timeout)
}

func (r *Runner) Cancel() {
	r.mu.Lock()
	c := r.current
	r.mu.Unlock()
	if c != nil {
		c.Cancel()
	}
}

func (r *Runner) Status() []tool.TaskState {
	r.mu.Lock()
	c := r.current
	r.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Status()
}

func (c *Coordinator) blockFailedDependents() {
	c.mu.Lock()
	changedAny := false
	changed := true
	for changed {
		changed = false
		for id, task := range c.tasks {
			if task.Status != "pending" {
				continue
			}
			for _, dependency := range task.DependsOn {
				parent, ok := c.tasks[dependency]
				if ok && (parent.Status == "failed" || parent.Status == "blocked" || parent.Status == "cancelled" || parent.Status == "timeout") {
					task.Status, task.Error = "blocked", "dependency "+dependency+" failed"
					c.tasks[id] = task
					changed = true
					changedAny = true
					break
				}
			}
		}
	}
	c.mu.Unlock()
	if changedAny {
		c.emit()
	}
}

func (c *Coordinator) dependenciesCompletedLocked(task tool.TaskState) bool {
	for _, dependency := range task.DependsOn {
		parent, ok := c.tasks[dependency]
		if !ok || parent.Status != "completed" {
			return false
		}
	}
	return true
}

func (c *Coordinator) snapshotLocked() []tool.TaskState {
	result := make([]tool.TaskState, 0, len(c.tasks))
	for _, task := range c.tasks {
		result = append(result, task)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
