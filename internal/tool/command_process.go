package tool

import (
	"context"
	"os/exec"
	"time"
)

type commandProcessHandle struct {
	cmd        *exec.Cmd
	afterStart func() error
	cancel     func() error
	close      func() error
}

func newCommandProcess(ctx context.Context, command, root, cwd string, sandbox SandboxDecision) *commandProcessHandle {
	cmd := sandboxCommand(ctx, command, root, cwd, sandbox)
	handle := newPlatformCommandProcess(cmd)
	cmd.Cancel = handle.cancel
	cmd.WaitDelay = 2 * time.Second
	return handle
}
