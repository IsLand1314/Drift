//go:build !windows

package tool

import (
	"context"
	"os/exec"
)

func NewAppContainerMCPProcess(ctx context.Context, _ string, command string, args, env []string, _ bool) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append([]string(nil), env...)
	return cmd
}
