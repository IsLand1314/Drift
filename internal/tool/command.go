package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

const (
	DefaultCommandTimeout = 30 * time.Second
	MaxCommandTimeout     = 60 * time.Second
	MaxCommandBytes       = 8 << 10
	MaxCommandOutputBytes = 32 << 10
)

type runCommandTool struct{ sandboxMode SandboxMode }

func (runCommandTool) Name() string                   { return "Bash" }
func (runCommandTool) Definition() llm.ToolDefinition { return RunCommandDefinition() }
func (runCommandTool) Execute(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("Bash requires permission confirmation")
}
func (t runCommandTool) Preview(ctx context.Context, root, raw string) (Preview, error) {
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	mode := t.sandboxMode
	if mode == "" {
		mode = SandboxOff
	}
	return RunCommandPreviewWithSandbox(root, raw, mode)
}
func (runCommandTool) ExecutePreview(ctx context.Context, root string, preview Preview) (string, error) {
	return ExecuteCommand(ctx, root, preview)
}

type commandArguments struct {
	Command        string `json:"command"`
	CWD            string `json:"cwd"`
	TimeoutMS      int    `json:"timeout_ms"`
	MaxOutputBytes int    `json:"max_output_bytes"`
}

func RunCommandDefinition() llm.ToolDefinition {
	function := map[string]any{
		"name":        "Bash",
		"description": "Run one user-approved command in the workspace. Output is limited and the command may not use a cwd outside the workspace.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":          map[string]any{"type": "string", "description": "Shell command to run."},
				"cwd":              map[string]any{"type": "string", "description": "Optional workspace-relative working directory."},
				"timeout_ms":       map[string]any{"type": "integer", "description": "Optional timeout from 1 to 60000 milliseconds."},
				"max_output_bytes": map[string]any{"type": "integer", "description": "Optional combined stdout/stderr limit from 256 to 32768 bytes."},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}
	raw, err := json.Marshal(function)
	if err != nil {
		panic("tool: marshal Bash definition: " + err.Error())
	}
	return llm.ToolDefinition{Type: "function", Function: raw}
}

func RunCommandPreview(root, raw string) (Preview, error) {
	return RunCommandPreviewWithSandbox(root, raw, SandboxOff)
}

func RunCommandPreviewWithSandbox(root, raw string, sandboxMode SandboxMode) (Preview, error) {
	var args commandArguments
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return Preview{}, fmt.Errorf("decode Bash arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Preview{}, fmt.Errorf("decode Bash arguments: multiple JSON values")
	}
	args.Command = strings.TrimSpace(args.Command)
	if args.Command == "" {
		return Preview{}, fmt.Errorf("Bash command is blank")
	}
	if len(args.Command) > MaxCommandBytes {
		return Preview{}, fmt.Errorf("Bash command exceeds %d bytes", MaxCommandBytes)
	}
	args.CWD = filepath.ToSlash(strings.TrimSpace(args.CWD))
	if args.CWD == "" {
		args.CWD = "."
	}
	if err := validateRelativePath(args.CWD, false); err != nil {
		return Preview{}, fmt.Errorf("Bash cwd is invalid: %w", err)
	}
	for _, part := range strings.Split(args.CWD, "/") {
		switch strings.ToLower(part) {
		case ".git", ".drift", ".codex-temp", ".worktrees":
			return Preview{}, fmt.Errorf("Bash cwd is protected")
		}
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(args.CWD)))
	if err != nil {
		return Preview{}, fmt.Errorf("Bash cwd: %w", err)
	}
	if !info.IsDir() {
		return Preview{}, fmt.Errorf("Bash cwd is not a directory")
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return Preview{}, fmt.Errorf("open Bash root: %w", err)
	}
	defer workspace.Close()
	if hasSymlink, symlinkErr := hasSymlinkComponent(workspace, args.CWD); symlinkErr != nil || hasSymlink {
		return Preview{}, fmt.Errorf("Bash cwd contains a symlink")
	}
	timeout := DefaultCommandTimeout
	if args.TimeoutMS != 0 {
		if args.TimeoutMS < 1 || time.Duration(args.TimeoutMS)*time.Millisecond > MaxCommandTimeout {
			return Preview{}, fmt.Errorf("Bash timeout_ms must be between 1 and 60000")
		}
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	maxOutput := MaxCommandOutputBytes
	if args.MaxOutputBytes != 0 {
		if args.MaxOutputBytes < 256 || args.MaxOutputBytes > MaxCommandOutputBytes {
			return Preview{}, fmt.Errorf("Bash max_output_bytes must be between 256 and %d", MaxCommandOutputBytes)
		}
		maxOutput = args.MaxOutputBytes
	}
	sandbox, err := SelectSandbox(sandboxMode, DetectSandboxForWorkspace(root))
	if err != nil {
		return Preview{}, fmt.Errorf("Bash: %w", err)
	}
	return Preview{Operation: "run_command", Path: args.CWD, Command: args.Command, CWD: args.CWD, Timeout: timeout, OutputLimit: maxOutput, SandboxMode: sandboxMode, Sandbox: sandbox}, nil
}

func ExecuteCommand(parent context.Context, root string, preview Preview) (string, error) {
	if preview.SandboxMode == SandboxRequired && !preview.Sandbox.Available {
		return "", fmt.Errorf("sandbox required but unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, preview.Timeout)
	defer cancel()
	process := newCommandProcess(ctx, preview.Command, root, preview.CWD, preview.Sandbox)
	cmd := process.cmd
	cmd.Dir = filepath.Join(root, filepath.FromSlash(preview.CWD))
	limit := preview.OutputLimit
	if limit <= 0 || limit > MaxCommandOutputBytes {
		limit = MaxCommandOutputBytes
	}
	stdout, stderr := &limitedBuffer{limit: limit / 2}, &limitedBuffer{limit: limit - limit/2}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	if err := cmd.Start(); err != nil {
		_ = process.close()
		return "", err
	}
	if err := process.afterStart(); err != nil {
		_ = process.cancel()
		_ = process.close()
		return "", err
	}
	err := cmd.Wait()
	_ = process.close()
	duration := time.Since(started).Round(time.Millisecond)
	status, exitCode := "success", 0
	if err != nil {
		status = "failed"
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
		}
		if ctx.Err() == context.Canceled {
			status = "cancelled"
		}
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ProcessState != nil {
			exitCode = exitErr.ExitCode()
		} else if status != "success" {
			exitCode = -1
		}
	}
	output := strings.TrimSpace(strings.TrimRight(stdout.String()+stderr.String(), "\r\n"))
	if output == "" {
		output = "<no output>"
	}
	probe := preview.Sandbox.Probe
	if probe == "" {
		probe = "unavailable"
	}
	return fmt.Sprintf("run_command status=%s exit_code=%d duration=%s sandbox_mode=%s sandbox_backend=%s sandboxed=%t sandbox_probe=%s stdout_bytes=%d stderr_bytes=%d truncated=%t\n%s", status, exitCode, duration, preview.SandboxMode, preview.Sandbox.Backend, preview.Sandbox.Available, probe, stdout.n, stderr.n, stdout.truncated || stderr.truncated, output), nil
}

type limitedBuffer struct {
	mu        sync.Mutex
	data      []byte
	n         int
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n += len(p)
	if len(b.data)+len(p) > b.limit {
		b.truncated = true
	}
	if len(b.data) < b.limit {
		take := len(p)
		if remaining := b.limit - len(b.data); take > remaining {
			take = remaining
		}
		b.data = append(b.data, p[:take]...)
	}
	return len(p), nil
}
func (b *limitedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }
