//go:build windows

package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteCommandWindowsCancellationStopsChildProcess(t *testing.T) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe is unavailable")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "leaked.txt")
	child := filepath.Join(root, "child2.cmd")
	if err := os.WriteFile(child, []byte("@echo off\r\necho started>leaked.txt\r\nping 127.0.0.1 -n 5 >nul\r\necho leaked>leaked.txt\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "child.cmd")
	if err := os.WriteFile(script, []byte("@echo off\r\nstart \"\" /b child2.cmd\r\nping 127.0.0.1 -n 10 >nul\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := "child.cmd"
	raw, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := RunCommandPreview(root, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type commandResult struct {
		result string
		err    error
	}
	done := make(chan commandResult, 1)
	go func() {
		result, err := ExecuteCommand(ctx, root, preview)
		done <- commandResult{result: result, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		data, readErr := os.ReadFile(marker)
		if readErr == nil && strings.TrimSpace(string(data)) == "started" {
			break
		}
		if time.Now().After(deadline) {
			select {
			case completed := <-done:
				t.Fatalf("child process did not start; command finished result=%q err=%v", completed.result, completed.err)
			default:
			}
			t.Fatalf("child process did not start: %v", readErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	completed := <-done
	if completed.err != nil {
		t.Fatalf("ExecuteCommand error: %v", completed.err)
	}
	result := completed.result
	if !strings.Contains(result, "status=cancelled") {
		t.Fatalf("result=%q", result)
	}
	time.Sleep(800 * time.Millisecond)
	data, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(data)) != "started" {
		t.Fatalf("child process survived cancellation and wrote %q, err=%v", data, err)
	}
}
