//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireBwrap(t *testing.T) {
	t.Helper()
	if capabilities := DetectSandbox(); !capabilities.Reliable || capabilities.Backend != "bwrap" {
		t.Skip("reliable bwrap backend is unavailable")
	}
}

func runRequiredSandbox(t *testing.T, root, command string) string {
	t.Helper()
	output := runSandbox(t, root, command)
	if !strings.Contains(output, "status=success") {
		t.Fatalf("sandbox command failed: %s", output)
	}
	return output
}

func runSandbox(t *testing.T, root, command string) string {
	t.Helper()
	raw := `{"command":` + quoteJSON(command) + `}`
	preview, err := RunCommandPreviewWithSandbox(root, raw, SandboxRequired)
	if err != nil {
		t.Fatal(err)
	}
	output, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "sandboxed=true") {
		t.Fatalf("sandbox was not active: %s", output)
	}
	return output
}

func quoteJSON(value string) string {
	quoted, _ := json.Marshal(value)
	return string(quoted)
}

func TestRequiredBwrapWritesOnlyWorkspace(t *testing.T) {
	requireBwrap(t)
	root, err := os.MkdirTemp("/var/tmp", "drift-m313-root-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	outsideDir, err := os.MkdirTemp("/var/tmp", "drift-m313-outside-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.Mkdir(filepath.Join(root, ".drift"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	runRequiredSandbox(t, root, "printf inside > inside.txt")
	if _, err := os.Stat(filepath.Join(root, "inside.txt")); err != nil {
		t.Fatalf("workspace write missing: %v", err)
	}

	runSandbox(t, root, "printf outside > "+outside)
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("sandbox wrote outside workspace: err=%v", err)
	}

	runRequiredSandbox(t, root, "printf blocked > .drift/blocked")
	if _, err := os.Stat(filepath.Join(root, ".drift", "blocked")); !os.IsNotExist(err) {
		t.Fatalf("sandbox wrote protected .drift: err=%v", err)
	}
	runRequiredSandbox(t, root, "printf blocked > .git/blocked")
	if _, err := os.Stat(filepath.Join(root, ".git", "blocked")); !os.IsNotExist(err) {
		t.Fatalf("sandbox wrote protected .git: err=%v", err)
	}

	if output := runRequiredSandbox(t, root, "test ! -s /proc/net/route"); !strings.Contains(output, "status=success") {
		t.Fatalf("network namespace appears to have routes: %s", output)
	}
}

func TestRequiredBwrapCancellationStopsChildProcess(t *testing.T) {
	requireBwrap(t)
	root, err := os.MkdirTemp("/var/tmp", "drift-m314-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	marker := filepath.Join(root, "leaked.txt")
	preview, err := RunCommandPreviewWithSandbox(root, `{"command":"(sleep 1; printf leaked > `+marker+` ) & wait"}`, SandboxRequired)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		result, _ := ExecuteCommand(ctx, root, preview)
		done <- result
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	result := <-done
	if !strings.Contains(result, "status=cancelled") {
		t.Fatalf("result=%q", result)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sandbox child survived cancellation and created %s", marker)
	}
}

func TestRequiredBwrapTimeoutStopsChildProcess(t *testing.T) {
	requireBwrap(t)
	root, err := os.MkdirTemp("/var/tmp", "drift-m314-timeout-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	marker := filepath.Join(root, "leaked.txt")
	preview, err := RunCommandPreviewWithSandbox(root, `{"command":"(sleep 1; printf leaked > `+marker+` ) & wait","timeout_ms":100}`, SandboxRequired)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil || !strings.Contains(result, "status=timeout") {
		t.Fatalf("result=%q err=%v", result, err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("sandbox child survived timeout and created %s", marker)
	}
}
