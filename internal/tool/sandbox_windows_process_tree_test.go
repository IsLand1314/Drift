//go:build windows

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsAppContainerKillsChildProcessWithJob(t *testing.T) {
	if os.Getenv("DRIFT_SANDBOX_CHILD") == "1" {
		time.Sleep(3 * time.Second)
		_ = os.WriteFile("child-process-marker", []byte("escaped"), 0600)
		return
	}
	if os.Getenv("DRIFT_SANDBOX_CHILD") != "1" && os.Getenv("DRIFT_SANDBOX_PARENT") == "1" {
		child := exec.Command(os.Args[0], "-test.run=TestWindowsAppContainerKillsChildProcessWithJob", "-test.v")
		child.Env = append(os.Environ(), "DRIFT_SANDBOX_CHILD=1")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	profile, sid, err := createWindowsAppContainerProfile()
	if err != nil {
		t.Skipf("AppContainer unavailable: %v", err)
	}
	defer deleteWindowsAppContainerProfile(profile)
	restoreRoot, err := grantWindowsTestPathAccess(root, sid, windows.GENERIC_EXECUTE)
	if err != nil {
		t.Fatal(err)
	}
	defer restoreRoot()

	marker := filepath.Join(root, "child-process-marker")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(marker)
	restore, err := grantWindowsTestFileAccess(marker, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()

	childBinary := filepath.Join(root, "sandbox-test.exe")
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childBinary, data, 0700); err != nil {
		t.Fatal(err)
	}
	restoreBinary, err := grantWindowsTestFileAccess(childBinary, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer restoreBinary()

	if err := os.Setenv("DRIFT_SANDBOX_PARENT", "1"); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("DRIFT_SANDBOX_PARENT")
	args := fmt.Sprintf(`-test.run=TestWindowsAppContainerKillsChildProcessWithJob -test.v`)
	err = runWindowsAppContainerProcess(childBinary, args, root, sid)
	t.Logf("child-parent exit=%v", err)
	time.Sleep(4 * time.Second)
	if content, err := os.ReadFile(marker); err != nil {
		t.Fatal(err)
	} else if len(content) != 0 {
		t.Fatalf("child survived job cleanup: %q", content)
	}
}

func grantWindowsTestFileAccess(path string, sid *windows.SID) (func(), error) {
	return grantWindowsTestPathAccess(path, sid, windows.GENERIC_ALL)
}

func grantWindowsTestPathAccess(path string, sid *windows.SID, permissions windows.ACCESS_MASK) (func(), error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, err
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
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
		return nil, err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return nil, err
	}
	return func() {
		_ = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	}, nil
}
