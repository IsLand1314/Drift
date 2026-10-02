package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCommandExecutesInWorkspace(t *testing.T) {
	root := t.TempDir()
	preview, err := RunCommandPreview(root, `{"command":"go version"}`)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Operation != "run_command" || preview.Command != "go version" || preview.CWD != "." {
		t.Fatalf("preview=%+v", preview)
	}
	result, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "status=success") || !strings.Contains(result, "go version") {
		t.Fatalf("result=%q", result)
	}
}

func TestRunCommandRejectsUnsafeCWD(t *testing.T) {
	root := t.TempDir()
	for _, raw := range []string{`{"command":"echo ok","cwd":"../outside"}`, `{"command":"echo ok","cwd":".drift"}`} {
		if _, err := RunCommandPreview(root, raw); err == nil {
			t.Fatalf("RunCommandPreview(%s) succeeded", raw)
		}
	}
}

func TestRunCommandTimesOut(t *testing.T) {
	root := t.TempDir()
	preview, err := RunCommandPreview(root, `{"command":"go test ./...","timeout_ms":20}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := ExecuteCommand(ctx, root, preview)
	if err != nil || !strings.Contains(result, "status=timeout") {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestRunCommandTruncatesOutput(t *testing.T) {
	root := t.TempDir()
	preview, err := RunCommandPreview(root, `{"command":"go env","max_output_bytes":256}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "truncated=true") {
		t.Fatalf("result=%q", result)
	}
}
