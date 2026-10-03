//go:build !windows

package tool

import "context"

func newAppContainerCommandProcess(context.Context, string, string, string, SandboxDecision) *commandProcessHandle {
	return nil
}
