//go:build !windows

package tool

import (
	"os/exec"
	"syscall"
	"time"
)

func newPlatformCommandProcess(cmd *exec.Cmd) *commandProcessHandle {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &commandProcessHandle{
		cmd:        cmd,
		afterStart: func() error { return nil },
		cancel: func() error {
			if cmd.Process == nil {
				return nil
			}
			pid := -cmd.Process.Pid
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
				return err
			}
			time.Sleep(50 * time.Millisecond)
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				return err
			}
			return nil
		},
		close: func() error { return nil },
	}
}
