//go:build windows

package tool

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type appContainerCommandProcess struct {
	root       string
	command    string
	cwd        string
	ctx        context.Context
	profile    string
	job        windows.Handle
	process    windows.Handle
	thread     windows.Handle
	restoreACL func()
	stdoutR    *os.File
	stderrR    *os.File
	waitOnce   sync.Once
	waitErr    error
	copyWG     sync.WaitGroup
}

func newAppContainerCommandProcess(ctx context.Context, command, root, cwd string, _ SandboxDecision) *commandProcessHandle {
	p := &appContainerCommandProcess{ctx: ctx, command: command, root: root, cwd: cwd}
	return &commandProcessHandle{
		start:      p.start,
		wait:       p.wait,
		afterStart: func() error { return nil },
		cancel:     p.cancel,
		close:      p.close,
	}
}

func (p *appContainerCommandProcess) start(stdout, stderr io.Writer) error {
	profile, sid, err := createWindowsAppContainerProfile()
	if err != nil {
		return err
	}
	p.profile = profile
	restore, err := grantWindowsProbeFileAccess(p.root, sid)
	if err != nil {
		deleteWindowsAppContainerProfile(profile)
		return fmt.Errorf("grant workspace ACL: %w", err)
	}
	p.restoreACL = restore

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return err
	}
	p.stdoutR, p.stderrR = stdoutR, stderrR
	started := false
	defer func() {
		if !started {
			_ = stdoutW.Close()
			_ = stderrW.Close()
		}
	}()
	for _, f := range []*os.File{stdoutW, stderrW} {
		if err := windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			stdoutR.Close()
			stdoutW.Close()
			stderrR.Close()
			stderrW.Close()
			return err
		}
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	caps := windowsSecurityCapabilities{AppContainerSID: sid}
	if err := attrs.Update(0x00020009, unsafe.Pointer(&caps), unsafe.Sizeof(caps)); err != nil {
		return err
	}
	p.job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}

	cmdPath := os.Getenv("ComSpec")
	if cmdPath == "" {
		cmdPath = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmdLine, err := windows.UTF16FromString(fmt.Sprintf(`/d /s /c "%s"`, p.command))
	if err != nil {
		return err
	}
	appName, err := windows.UTF16PtrFromString(cmdPath)
	if err != nil {
		return err
	}
	workdir := filepath.Join(p.root, filepath.FromSlash(p.cwd))
	workdirPtr, err := windows.UTF16PtrFromString(workdir)
	if err != nil {
		return err
	}
	envBlock, err := sanitizedEnvironmentBlock()
	if err != nil {
		return err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.ProcThreadAttributeList = attrs.List()
	startup.StdOutput = windows.Handle(stdoutW.Fd())
	startup.StdErr = windows.Handle(stderrW.Fd())
	startup.Flags = windows.STARTF_USESTDHANDLES
	var info windows.ProcessInformation
	if err := windows.CreateProcess(appName, &cmdLine[0], nil, nil, true, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_NO_WINDOW|windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT, &envBlock[0], workdirPtr, &startup.StartupInfo, &info); err != nil {
		return err
	}
	stdoutW.Close()
	stderrW.Close()
	p.process, p.thread = info.Process, info.Thread
	started = true
	if err := windows.AssignProcessToJobObject(p.job, p.process); err != nil {
		return err
	}
	if _, err := windows.ResumeThread(p.thread); err != nil {
		return err
	}
	p.copyWG.Add(2)
	go func() { defer p.copyWG.Done(); _, _ = io.Copy(stdout, stdoutR) }()
	go func() { defer p.copyWG.Done(); _, _ = io.Copy(stderr, stderrR) }()
	return nil
}

func sanitizedEnvironmentBlock() ([]uint16, error) {
	block := make([]uint16, 0)
	for _, entry := range sanitizedCommandEnv() {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded[:len(encoded)-1]...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func (p *appContainerCommandProcess) wait() error {
	p.waitOnce.Do(func() {
		if p.process == 0 {
			p.waitErr = fmt.Errorf("AppContainer process was not started")
			return
		}
		for {
			result, err := windows.WaitForSingleObject(p.process, 100)
			if err != nil {
				p.waitErr = err
				return
			}
			if result == uint32(windows.WAIT_OBJECT_0) {
				break
			}
			select {
			case <-p.ctx.Done():
				_ = p.cancel()
				p.waitErr = p.ctx.Err()
				return
			default:
			}
		}
		var code uint32
		if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
			p.waitErr = err
		} else if code != 0 {
			p.waitErr = &windowsExitError{code: int(code)}
		}
	})
	return p.waitErr
}

type windowsExitError struct{ code int }

func (e *windowsExitError) Error() string {
	return fmt.Sprintf("AppContainer process exited with code %d", e.code)
}
func (e *windowsExitError) ExitCode() int { return e.code }

func (p *appContainerCommandProcess) cancel() error {
	if p.job != 0 {
		return windows.TerminateJobObject(p.job, 1)
	}
	return nil
}

func (p *appContainerCommandProcess) close() error {
	if p.stdoutR != nil {
		_ = p.stdoutR.Close()
	}
	if p.stderrR != nil {
		_ = p.stderrR.Close()
	}
	p.copyWG.Wait()
	if p.thread != 0 {
		_ = windows.CloseHandle(p.thread)
	}
	if p.process != 0 {
		_ = windows.CloseHandle(p.process)
	}
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
	}
	if p.restoreACL != nil {
		p.restoreACL()
	}
	if p.profile != "" {
		deleteWindowsAppContainerProfile(p.profile)
	}
	return nil
}
