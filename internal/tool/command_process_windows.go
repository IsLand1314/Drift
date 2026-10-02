//go:build windows

package tool

import (
	"errors"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func newPlatformCommandProcess(cmd *exec.Cmd) *commandProcessHandle {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fallbackWindowsCommandProcess(cmd)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return fallbackWindowsCommandProcess(cmd)
	}
	var closed bool
	closeJob := func() error {
		if closed {
			return nil
		}
		closed = true
		return windows.CloseHandle(job)
	}
	return &commandProcessHandle{
		cmd: cmd,
		afterStart: func() error {
			if cmd.Process == nil {
				return errors.New("command process did not start")
			}
			process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION, false, uint32(cmd.Process.Pid))
			if err != nil {
				return nil
			}
			defer windows.CloseHandle(process)
			_ = windows.AssignProcessToJobObject(job, process)
			return nil
		},
		cancel: func() error {
			if cmd.Process == nil {
				return closeJob()
			}
			err := exec.Command("taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
			if err != nil {
				err = windows.TerminateJobObject(job, 1)
			}
			_ = closeJob()
			return err
		},
		close: closeJob,
	}
}

func fallbackWindowsCommandProcess(cmd *exec.Cmd) *commandProcessHandle {
	return &commandProcessHandle{
		cmd:        cmd,
		afterStart: func() error { return nil },
		cancel: func() error {
			if cmd.Process == nil {
				return nil
			}
			return exec.Command("taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		},
		close: func() error { return nil },
	}
}
