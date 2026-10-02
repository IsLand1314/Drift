//go:build windows

package tool

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsSandboxProbe is deliberately kept behind a seam until the native
// AppContainer launch and ACL cleanup path is implemented and verified on a
// supported Windows host. A false result is safer than claiming isolation.
var windowsSandboxProbe = func(string) SandboxCapabilities {
	if err := verifyWindowsAppContainerAPIs(); err != nil {
		return windowsSandboxUnavailable("api:" + err.Error())
	}
	if err := probeWindowsAppContainerProfile(); err != nil {
		return windowsSandboxUnavailable("profile:" + err.Error())
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

func probeWindowsAppContainerProfile() error {
	name := fmt.Sprintf("DriftProbe-%d", time.Now().UnixNano())
	profile := windows.NewLazySystemDLL("userenv.dll")
	create := profile.NewProc("CreateAppContainerProfile")
	delete := profile.NewProc("DeleteAppContainerProfile")
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var sid *windows.SID
	r1, _, callErr := create.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(namePtr)),
		0, 0, uintptr(unsafe.Pointer(&sid)),
	)
	if r1 != 0 {
		return fmt.Errorf("CreateAppContainerProfile: %w", callErr)
	}
	if sid != nil {
		_ = windows.FreeSid(sid)
	}
	defer delete.Call(uintptr(unsafe.Pointer(namePtr)))
	return nil
}
