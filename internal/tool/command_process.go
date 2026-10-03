package tool

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type commandProcessHandle struct {
	cmd        *exec.Cmd
	start      func(io.Writer, io.Writer) error
	wait       func() error
	afterStart func() error
	cancel     func() error
	close      func() error
}

func newCommandProcess(ctx context.Context, command, root, cwd string, sandbox SandboxDecision) *commandProcessHandle {
	if sandbox.Backend == "appcontainer" {
		return newAppContainerCommandProcess(ctx, command, root, cwd, sandbox)
	}
	cmd := sandboxCommand(ctx, command, root, cwd, sandbox)
	cmd.Env = sanitizedCommandEnv()
	handle := newPlatformCommandProcess(cmd)
	handle.start = func(stdout, stderr io.Writer) error {
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return cmd.Start()
	}
	handle.wait = cmd.Wait
	cmd.Cancel = handle.cancel
	cmd.WaitDelay = 2 * time.Second
	return handle
}

func sanitizedCommandEnv() []string {
	blocked := map[string]struct{}{
		"OPENAI_API_KEY": {}, "ANTHROPIC_API_KEY": {}, "AZURE_OPENAI_API_KEY": {},
		"AWS_ACCESS_KEY_ID": {}, "AWS_SECRET_ACCESS_KEY": {}, "GITHUB_TOKEN": {},
	}
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if _, ok := blocked[upper]; ok || strings.HasSuffix(upper, "_API_KEY") || strings.HasSuffix(upper, "_TOKEN") || strings.HasSuffix(upper, "_PASSWORD") || strings.HasSuffix(upper, "_SECRET") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
