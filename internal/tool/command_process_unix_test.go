//go:build !windows

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteCommandUnixCancellationStopsChildProcess(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "leaked.txt")
	preview := Preview{Operation: "run_command", Command: "(sleep 0.5; printf leaked > '" + marker + "') & wait", CWD: ".", Timeout: 5 * time.Second, OutputLimit: MaxCommandOutputBytes}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		result, _ := ExecuteCommand(ctx, root, preview)
		done <- result
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	result := <-done
	if !strings.Contains(result, "status=cancelled") {
		t.Fatalf("result=%q", result)
	}
	time.Sleep(800 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("child process survived cancellation and created %s", marker)
	}
}
