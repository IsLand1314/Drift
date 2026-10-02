package tool

import (
	"context"
	"os/exec"
	"runtime"
	"time"
)

type commandProcessHandle struct {
	cmd        *exec.Cmd
	afterStart func() error
	cancel     func() error
	close      func() error
}

func newCommandProcess(ctx context.Context, command string) *commandProcessHandle {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}
	handle := newPlatformCommandProcess(cmd)
	cmd.Cancel = handle.cancel
	cmd.WaitDelay = 2 * time.Second
	return handle
}
