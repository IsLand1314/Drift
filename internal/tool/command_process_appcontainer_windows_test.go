//go:build windows

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteCommandUsesAppContainerLifecycle(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".drift", ".git"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	preview := Preview{
		Operation:   "run_command",
		Command:     "echo appcontainer-output",
		CWD:         ".",
		Timeout:     10 * time.Second,
		OutputLimit: 4096,
		SandboxMode: SandboxRequired,
		Sandbox: SandboxDecision{
			Mode:         SandboxRequired,
			Backend:      "appcontainer",
			Available:    true,
			Capabilities: []string{"workspace-write", "network-isolated", "process-tree"},
			Probe:        "passed",
		},
	}
	output, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "status=success") || !strings.Contains(output, "appcontainer-output") || !strings.Contains(output, "sandboxed=true") {
		t.Fatalf("output=%q", output)
	}
}

func TestExecuteCommandAppContainerCanWriteWorkspaceOnly(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".drift", ".git"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	preview := Preview{
		Operation:   "run_command",
		Command:     `echo sandbox-write > appcontainer-write.txt`,
		CWD:         ".",
		Timeout:     10 * time.Second,
		OutputLimit: 4096,
		SandboxMode: SandboxRequired,
		Sandbox:     SandboxDecision{Mode: SandboxRequired, Backend: "appcontainer", Available: true, Probe: "passed"},
	}
	output, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil || !strings.Contains(output, "status=success") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "appcontainer-write.txt"))
	if err != nil || string(data) != "sandbox-write \r\n" {
		t.Fatalf("workspace write=%q err=%v", data, err)
	}
	protected := preview
	protected.Command = `echo blocked > .drift\blocked.txt`
	protectedOutput, err := ExecuteCommand(context.Background(), root, protected)
	if err != nil || !strings.Contains(protectedOutput, "status=failed") {
		t.Fatalf("protected write output=%q err=%v", protectedOutput, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "blocked.txt")); err == nil {
		t.Fatal("protected directory unexpectedly changed")
	}
}

func TestRunCommandPreviewUsesVerifiedAppContainer(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".drift", ".git"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := RunCommandPreviewWithSandbox(root, `{"command":"echo preview-appcontainer"}`, SandboxRequired)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Sandbox.Available || preview.Sandbox.Backend != "appcontainer" {
		t.Fatalf("sandbox=%+v", preview.Sandbox)
	}
	output, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "status=success") || !strings.Contains(output, "preview-appcontainer") {
		t.Fatalf("output=%q", output)
	}
}

func TestExecuteCommandAppContainerSupportsNestedWorkspaceCWD(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested", "a", "b")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".drift", ".git"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	preview := Preview{
		Operation:   "run_command",
		Command:     `echo nested > nested-output.txt`,
		CWD:         "nested/a/b",
		Timeout:     10 * time.Second,
		OutputLimit: 4096,
		SandboxMode: SandboxRequired,
		Sandbox:     SandboxDecision{Mode: SandboxRequired, Backend: "appcontainer", Available: true, Probe: "passed"},
	}
	output, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil || !strings.Contains(output, "status=success") {
		t.Fatalf("nested output=%q err=%v", output, err)
	}
	data, err := os.ReadFile(filepath.Join(nested, "nested-output.txt"))
	if err != nil || string(data) != "nested \r\n" {
		t.Fatalf("nested file=%q err=%v", data, err)
	}
}
