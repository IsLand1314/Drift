//go:build windows

package tool

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// appContainerMCPProcess is the stdio equivalent of the Bash AppContainer
// process. It deliberately exposes only the process surface MCP needs.
type appContainerMCPProcess struct {
	ctx            context.Context
	root           string
	command        string
	args           []string
	env            []string
	networkEnabled bool
	profile        string
	job            windows.Handle
	process        windows.Handle
	thread         windows.Handle
	stdinR         *os.File
	stdinW         *os.File
	stdoutR        *os.File
	stdoutW        *os.File
	stderrR        *os.File
	stderrW        *os.File
	restoreACL     func()
	waitOnce       sync.Once
	waitErr        error
}

func newAppContainerMCPProcess(ctx context.Context, root, command string, args, env []string, networkEnabled bool) *appContainerMCPProcess {
	return &appContainerMCPProcess{ctx: ctx, root: root, command: command, args: append([]string(nil), args...), env: append([]string(nil), env...), networkEnabled: networkEnabled}
}

func NewAppContainerMCPProcess(ctx context.Context, root, command string, args, env []string, networkEnabled bool) *appContainerMCPProcess {
	return newAppContainerMCPProcess(ctx, root, command, args, env, networkEnabled)
}

func (p *appContainerMCPProcess) StdinPipe() (io.WriteCloser, error) {
	if p.stdinW != nil {
		return p.stdinW, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	p.stdinR, p.stdinW = r, w
	return w, nil
}

func (p *appContainerMCPProcess) StdoutPipe() (io.ReadCloser, error) {
	if p.stdoutR != nil {
		return p.stdoutR, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	p.stdoutR, p.stdoutW = r, w
	return r, nil
}

func (p *appContainerMCPProcess) Start() error {
	if p.command == "" {
		return fmt.Errorf("empty MCP command")
	}
	profile, sid, err := createWindowsAppContainerProfile()
	if err != nil {
		return err
	}
	p.profile = profile
	started := false
	defer func() {
		if !started {
			_ = p.Close()
		}
	}()
	restore, err := grantWindowsWorkspaceAccess(p.root, ".", sid)
	if err != nil {
		deleteWindowsAppContainerProfile(profile)
		return fmt.Errorf("grant workspace ACL: %w", err)
	}
	p.restoreACL = restore
	if p.stdinR == nil {
		if _, err := p.StdinPipe(); err != nil {
			return err
		}
	}
	if p.stdoutR == nil {
		if _, err := p.StdoutPipe(); err != nil {
			return err
		}
	}
	p.stderrR, p.stderrW, err = os.Pipe()
	if err != nil {
		return err
	}
	for _, f := range []*os.File{p.stdinR, p.stdoutW, p.stderrW} {
		if err := windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return err
		}
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	caps := windowsSecurityCapabilities{AppContainerSID: sid}
	var capability windows.SIDAndAttributes
	if p.networkEnabled {
		internetSID, sidErr := windows.StringToSid("S-1-15-3-1") // internetClient capability
		if sidErr != nil {
			return sidErr
		}
		capability = windows.SIDAndAttributes{Sid: internetSID, Attributes: windows.SE_GROUP_ENABLED}
		caps.Capabilities = &capability
		caps.CapabilityCount = 1
	}
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
	command, commandArgs := resolveWindowsMCPCommand(p.command, p.args, os.Getenv("ComSpec"))
	if runtimeRestore, err := grantExternalMCPRuntimeAccess(command, p.root, sid); err != nil {
		_ = p.Close()
		return err
	} else if runtimeRestore != nil {
		workspaceRestore := p.restoreACL
		p.restoreACL = func() {
			runtimeRestore()
			workspaceRestore()
		}
	}
	appName, err := windows.UTF16PtrFromString(command)
	if err != nil {
		return err
	}
	cmdLine, err := windows.UTF16FromString(quoteWindowsArgs(append([]string{command}, commandArgs...)))
	if err != nil {
		return err
	}
	workdir, err := filepath.Abs(p.root)
	if err != nil {
		return err
	}
	workdirPtr, err := windows.UTF16PtrFromString(workdir)
	if err != nil {
		return err
	}
	envBlock, err := sanitizedEnvironmentBlockWithOverrides(p.env)
	if err != nil {
		return err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.ProcThreadAttributeList = attrs.List()
	startup.StdInput = windows.Handle(p.stdinR.Fd())
	startup.StdOutput = windows.Handle(p.stdoutW.Fd())
	startup.StdErr = windows.Handle(p.stderrW.Fd())
	startup.Flags = windows.STARTF_USESTDHANDLES
	var info windows.ProcessInformation
	if err := windows.CreateProcess(appName, &cmdLine[0], nil, nil, true, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_NO_WINDOW|windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT, &envBlock[0], workdirPtr, &startup.StartupInfo, &info); err != nil {
		return err
	}
	p.stdinR.Close()
	p.stdoutW.Close()
	p.stderrW.Close()
	p.thread, p.process = info.Thread, info.Process
	if err := windows.AssignProcessToJobObject(p.job, p.process); err != nil {
		return err
	}
	if _, err := windows.ResumeThread(p.thread); err != nil {
		return err
	}
	started = true
	go func() { _, _ = io.Copy(io.Discard, p.stderrR) }()
	return nil
}

func grantExternalMCPRuntimeAccess(command, root string, sid *windows.SID) (func(), error) {
	absoluteCommand, err := filepath.Abs(command)
	if err != nil || !filepath.IsAbs(command) {
		return nil, nil
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(absoluteRoot, absoluteCommand)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return nil, nil
	}
	return grantWindowsWorkspaceAccess(filepath.Dir(absoluteCommand), ".", sid)
}

func resolveWindowsMCPCommand(command string, args []string, comspec string) (string, []string) {
	resolved := command
	if filepath.Ext(command) == "" {
		for _, extension := range []string{".exe", ".cmd", ".bat"} {
			if candidate, err := exec.LookPath(command + extension); err == nil {
				resolved = candidate
				break
			}
		}
	}
	extension := strings.ToLower(filepath.Ext(resolved))
	if (extension == ".cmd" || extension == ".bat") && comspec != "" {
		return comspec, append([]string{"/d", "/s", "/c", "call", resolved}, args...)
	}
	return resolved, args
}

func quoteWindowsArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\"") {
			quoted[i] = `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}

func (p *appContainerMCPProcess) Wait() error {
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
				_ = p.Kill()
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

func (p *appContainerMCPProcess) Kill() error {
	if p.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(p.job, 1)
}

func (p *appContainerMCPProcess) Close() error {
	_ = p.Kill()
	for _, f := range []*os.File{p.stdinW, p.stdoutR, p.stderrR} {
		_ = closeWindowsMCPFile(f)
	}
	for _, h := range []windows.Handle{p.thread, p.process, p.job} {
		_ = closeWindowsMCPHandle(h)
	}
	if p.restoreACL != nil {
		p.restoreACL()
	}
	if p.profile != "" {
		deleteWindowsAppContainerProfile(p.profile)
	}
	return nil
}

func closeWindowsMCPFile(item *os.File) error {
	if item != nil {
		return item.Close()
	}
	return nil
}

func closeWindowsMCPHandle(item windows.Handle) error {
	if item != 0 {
		return windows.CloseHandle(item)
	}
	return nil
}
