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

// windowsSandboxProbe is deliberately kept behind a seam until the native
// AppContainer launch and ACL cleanup path is implemented and verified on a
// supported Windows host. A false result is safer than claiming isolation.
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
	return windowsSandboxUnavailable("runtime-isolation-not-verified")
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
	_ = os.Remove(marker)
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		return fmt.Errorf("create workspace marker: %w", err)
	}
	defer os.Remove(marker)

	sd, err := windows.GetNamedSecurityInfo(marker, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read workspace ACL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read workspace DACL: %w", err)
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		return fmt.Errorf("build workspace ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(marker, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("grant workspace marker ACL: %w", err)
	}
	defer func() {
		_, _, _ = sd.DACL()
		_ = windows.SetNamedSecurityInfo(marker, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	}()

	cmdPath := os.Getenv("ComSpec")
	if cmdPath == "" {
		cmdPath = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	markerArg := fmt.Sprintf(`/d /s /c "echo drift-appcontainer > \"%s\""`, marker)
	return runWindowsAppContainerProcess(cmdPath, markerArg, root, sid)
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
	if _, err := windows.WaitForSingleObject(info.Process, 5000); err != nil {
		return fmt.Errorf("wait AppContainer process: %w", err)
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
