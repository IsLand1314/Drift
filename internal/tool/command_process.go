package tool

import (
	"context"
	"io"
	"os/exec"
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
	cmd := sandboxCommand(ctx, command, root, cwd, sandbox)
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
