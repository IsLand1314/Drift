//go:build windows

package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsSandboxProbe stays behind a seam so unsupported Windows hosts fail
// closed instead of claiming isolation.
var windowsSandboxProbe = func(root string) SandboxCapabilities {
	if err := verifyWindowsAppContainerAPIs(); err != nil {
		return windowsSandboxUnavailable("api:" + err.Error())
	}
	profileName, sid, err := createWindowsAppContainerProfile()
	if err != nil {
		return windowsSandboxUnavailable("profile:" + err.Error())
	}
	defer deleteWindowsAppContainerProfile(profileName)
	if err := probeWindowsAppContainerProcess(root, sid); err != nil {
		return windowsSandboxUnavailable("runtime:" + err.Error())
	}
	return SandboxCapabilities{Backend: "appcontainer", Reliable: true, Probe: "passed", Capabilities: []string{"workspace-write", "network-isolated", "process-tree"}}
}

func probeWindowsSandbox(root string) SandboxCapabilities {
	return windowsSandboxProbe(root)
}

func windowsSandboxUnavailable(reason string) SandboxCapabilities {
	return SandboxCapabilities{Probe: "unavailable:" + reason}
}

func verifyWindowsAppContainerAPIs() error {
	apis := []struct {
		dll  string
		name string
	}{
		{"userenv.dll", "CreateAppContainerProfile"},
		{"userenv.dll", "DeleteAppContainerProfile"},
		{"advapi32.dll", "CreateProcessAsUserW"},
		{"kernel32.dll", "InitializeProcThreadAttributeList"},
		{"kernel32.dll", "UpdateProcThreadAttribute"},
		{"kernel32.dll", "DeleteProcThreadAttributeList"},
	}
	for _, api := range apis {
		if err := windows.NewLazySystemDLL(api.dll).NewProc(api.name).Find(); err != nil {
			return fmt.Errorf("%s!%s: %w", api.dll, api.name, err)
		}
	}
	return nil
}

func createWindowsAppContainerProfile() (string, *windows.SID, error) {
	name := fmt.Sprintf("DriftProbe-%d", time.Now().UnixNano())
	profile := windows.NewLazySystemDLL("userenv.dll")
	create := profile.NewProc("CreateAppContainerProfile")
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", nil, err
	}
	var sid *windows.SID
	r1, _, callErr := create.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(namePtr)),
		0, 0, uintptr(unsafe.Pointer(&sid)),
	)
	if r1 != 0 {
		if callErr == nil {
			callErr = fmt.Errorf("HRESULT 0x%x", r1)
		}
		return "", nil, fmt.Errorf("CreateAppContainerProfile: %w", callErr)
	}
	if sid == nil {
		return "", nil, fmt.Errorf("CreateAppContainerProfile returned a nil SID")
	}
	return name, sid, nil
}

func deleteWindowsAppContainerProfile(name string) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err == nil {
		_, _, _ = windows.NewLazySystemDLL("userenv.dll").NewProc("DeleteAppContainerProfile").Call(uintptr(unsafe.Pointer(namePtr)))
	}
}

type windowsSecurityCapabilities struct {
	AppContainerSID *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

func probeWindowsAppContainerProcess(root string, sid *windows.SID) error {
	marker := filepath.Join(root, ".drift-appcontainer-probe-marker")
	networkMarker := filepath.Join(root, ".drift-appcontainer-network-marker")
	childMarker := filepath.Join(root, ".drift-appcontainer-child-marker")
	_ = os.Remove(marker)
	_ = os.Remove(networkMarker)
	_ = os.Remove(childMarker)
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		return fmt.Errorf("create workspace marker: %w", err)
	}
	if err := os.WriteFile(networkMarker, nil, 0600); err != nil {
		return fmt.Errorf("create network marker: %w", err)
	}
	if err := os.WriteFile(childMarker, nil, 0600); err != nil {
		return fmt.Errorf("create child marker: %w", err)
	}
	defer os.Remove(marker)
	defer os.Remove(networkMarker)
	defer os.Remove(childMarker)

	for _, path := range []string{marker, networkMarker, childMarker} {
		restore, err := grantWindowsProbeFileAccess(path, sid)
		if err != nil {
			return err
		}
		defer restore()
	}

	cmdPath := os.Getenv("ComSpec")
	if cmdPath == "" {
		cmdPath = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	markerArg := `/d /s /c "echo drift-appcontainer > .drift-appcontainer-probe-marker"`
	if err := runWindowsAppContainerProcess(cmdPath, markerArg, root, sid); err != nil {
		return fmt.Errorf("workspace marker: %w", err)
	}
	for _, protected := range []string{".drift", ".git"} {
		path := filepath.Join(root, protected)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("protected directory %s unavailable", protected)
		}
		attempt := fmt.Sprintf(`/d /s /c "echo blocked > %s\\drift-probe-denied"`, protected)
		if err := runWindowsAppContainerProcess(cmdPath, attempt, root, sid); err == nil {
			return fmt.Errorf("protected directory %s accepted a write", protected)
		}
	}
	networkArg := `/d /s /c "ping.exe -n 1 -w 250 127.0.0.1 > .drift-appcontainer-network-marker"`
	_ = runWindowsAppContainerProcess(cmdPath, networkArg, root, sid)
	if output, err := os.ReadFile(networkMarker); err == nil && strings.Contains(string(output), "TTL=") {
		return fmt.Errorf("network access was not isolated")
	}
	childArg := `/d /s /c "start /b \"\" cmd.exe /d /s /c \"ping.exe -n 4 127.0.0.1 >nul & echo escaped > .drift-appcontainer-child-marker\""`
	_ = runWindowsAppContainerProcess(cmdPath, childArg, root, sid)
	time.Sleep(500 * time.Millisecond)
	if output, err := os.ReadFile(childMarker); err == nil && strings.Contains(string(output), "escaped") {
		return fmt.Errorf("child process escaped job containment")
	}
	return nil
}

func grantWindowsProbeFileAccess(path string, sid *windows.SID) (func(), error) {
	return updateWindowsACL(path, sid, windows.GENERIC_ALL, windows.GRANT_ACCESS, windows.NO_INHERITANCE)
}

func grantWindowsWorkspaceAccess(root, cwd string, sid *windows.SID) (func(), error) {
	type aclRestore struct {
		path string
		dacl *windows.ACL
	}
	var restores []aclRestore
	apply := func(path string, permissions windows.ACCESS_MASK, mode windows.ACCESS_MODE, inheritance uint32) error {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return fmt.Errorf("read workspace ACL %s: %w", path, err)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			return fmt.Errorf("read workspace DACL %s: %w", path, err)
		}
		entry := windows.EXPLICIT_ACCESS{AccessPermissions: permissions, AccessMode: mode, Inheritance: inheritance, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(sid)}}
		acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
		if err != nil {
			return fmt.Errorf("build workspace ACL %s: %w", path, err)
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			return fmt.Errorf("grant workspace ACL %s: %w", path, err)
		}
		restores = append(restores, aclRestore{path: path, dacl: dacl})
		return nil
	}
	if err := apply(root, windows.GENERIC_ALL, windows.GRANT_ACCESS, windows.NO_INHERITANCE); err != nil {
		for j := len(restores) - 1; j >= 0; j-- {
			_ = windows.SetNamedSecurityInfo(restores[j].path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, restores[j].dacl, nil)
		}
		return nil, err
	}
	if cwd != "." && cwd != "" {
		current := root
		parts := strings.Split(filepath.ToSlash(cwd), "/")
		for i, part := range parts {
			current = filepath.Join(current, filepath.FromSlash(part))
			inheritance := uint32(windows.NO_INHERITANCE)
			if i == len(parts)-1 {
				inheritance = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
			}
			if err := apply(current, windows.GENERIC_ALL, windows.GRANT_ACCESS, inheritance); err != nil {
				for j := len(restores) - 1; j >= 0; j-- {
					_ = windows.SetNamedSecurityInfo(restores[j].path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, restores[j].dacl, nil)
				}
				return nil, err
			}
		}
	}
	return func() {
		for i := len(restores) - 1; i >= 0; i-- {
			_ = windows.SetNamedSecurityInfo(restores[i].path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, restores[i].dacl, nil)
		}
	}, nil
}

func updateWindowsACL(path string, sid *windows.SID, permissions uint32, mode uint32, inheritance uint32) (func(), error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, fmt.Errorf("read probe ACL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("read probe DACL: %w", err)
	}
	entry := windows.EXPLICIT_ACCESS{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(sid)}}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		return nil, fmt.Errorf("build probe ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return nil, fmt.Errorf("grant probe ACL: %w", err)
	}
	return func() {
		_ = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	}, nil
}

func runWindowsAppContainerProcess(appPath, args, cwd string, sid *windows.SID) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fmt.Errorf("create process attributes: %w", err)
	}
	defer attrs.Delete()
	caps := windowsSecurityCapabilities{AppContainerSID: sid}
	const procThreadAttributeSecurityCapabilities = 0x00020009
	if err := attrs.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&caps), unsafe.Sizeof(caps)); err != nil {
		return fmt.Errorf("set AppContainer security capabilities: %w", err)
	}
	cmdLine, err := windows.UTF16FromString(strings.TrimSpace(args))
	if err != nil {
		return err
	}
	appName, err := windows.UTF16PtrFromString(appPath)
	if err != nil {
		return err
	}
	cwdPtr, err := windows.UTF16PtrFromString(cwd)
	if err != nil {
		return err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.ProcThreadAttributeList = attrs.List()
	var info windows.ProcessInformation
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create AppContainer job: %w", err)
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("configure AppContainer job: %w", err)
	}
	if err := windows.CreateProcess(appName, &cmdLine[0], nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_NO_WINDOW|windows.CREATE_SUSPENDED, nil, cwdPtr, &startup.StartupInfo, &info); err != nil {
		return fmt.Errorf("CreateProcess AppContainer: %w", err)
	}
	defer windows.CloseHandle(info.Thread)
	defer windows.CloseHandle(info.Process)
	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		return fmt.Errorf("assign AppContainer process to job: %w", err)
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		return fmt.Errorf("resume AppContainer process: %w", err)
	}
	waitResult, err := windows.WaitForSingleObject(info.Process, 5000)
	if err != nil {
		return fmt.Errorf("wait AppContainer process: %w", err)
	}
	if waitResult == uint32(windows.WAIT_TIMEOUT) {
		_ = windows.TerminateJobObject(job, 1)
		return fmt.Errorf("AppContainer process timed out")
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(info.Process, &exitCode); err != nil {
		return fmt.Errorf("read AppContainer exit code: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("AppContainer process exited with code %d", exitCode)
	}
	return nil
}
