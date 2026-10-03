package tool

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
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
	if preview.Operation != "run_command" || preview.Command != "go version" || preview.CWD != "." || preview.Profile != SandboxProfileBash {
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

func TestBashCommandEnvironmentDoesNotExposeProviderSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-secret")
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-secret")
	env := sanitizedCommandEnv()
	for _, value := range env {
		if strings.Contains(value, "OPENAI_API_KEY=") || strings.Contains(value, "ANTHROPIC_API_KEY=") {
			t.Fatalf("provider secret remained in child environment: %q", value)
		}
	}
}

func TestBashExecutionDoesNotExposeProviderSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "test-openai-secret")
	command := `printf '%s' "$OPENAI_API_KEY"`
	if runtime.GOOS == "windows" {
		command = `echo %OPENAI_API_KEY%`
	}
	preview, err := RunCommandPreview(root, `{"command":`+strconv.Quote(command)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil || strings.Contains(result, "test-openai-secret") {
		t.Fatalf("provider secret leaked: result=%q err=%v", result, err)
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
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	preview, err := RunCommandPreview(root, `{"command":"go test ./...","timeout_ms":500}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
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

func TestLimitedBufferPreservesHeadAndTailOnTruncation(t *testing.T) {
	var buffer limitedBuffer
	buffer.limit = 16
	input := "HEAD-1234567890-TAIL"
	if _, err := buffer.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	got := buffer.String()
	if !buffer.truncated || !strings.Contains(got, "output truncated") || !strings.Contains(got, "HEAD") || !strings.Contains(got, "TAIL") {
		t.Fatalf("buffer=%q truncated=%v", got, buffer.truncated)
	}
}

func TestLimitedBufferPreservesAllDataBeforeLimit(t *testing.T) {
	var buffer limitedBuffer
	buffer.limit = 16
	_, _ = buffer.Write([]byte("first-"))
	_, _ = buffer.Write([]byte("second"))
	if got := buffer.String(); got != "first-second" {
		t.Fatalf("buffer=%q, want complete pre-limit output", got)
	}
}

func TestRunCommandReportsNonZeroExit(t *testing.T) {
	root := t.TempDir()
	preview, err := RunCommandPreview(root, `{"command":"exit 7"}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "status=failed") || !strings.Contains(result, "exit_code=7") || strings.Contains(result, "status=success") {
		t.Fatalf("result=%q", result)
	}
}

func TestRunCommandSandboxModeIsRecorded(t *testing.T) {
	root := t.TempDir()
	preview, err := RunCommandPreviewWithSandbox(root, `{"command":"echo ok"}`, SandboxAuto)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SandboxMode != SandboxAuto || preview.Sandbox.Available {
		t.Fatalf("preview sandbox=%+v", preview)
	}
	result, err := ExecuteCommand(context.Background(), root, preview)
	if err != nil || !strings.Contains(result, "sandbox_mode=auto") || !strings.Contains(result, "sandboxed=false") || !strings.Contains(result, "sandbox_probe=") {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestRunCommandRequiredSandboxFailsClosedWithoutBackend(t *testing.T) {
	root := t.TempDir()
	if _, err := RunCommandPreviewWithSandbox(root, `{"command":"echo blocked"}`, SandboxRequired); err == nil {
		t.Fatal("required sandbox unexpectedly accepted without reliable backend")
	} else {
		var denied *SandboxDeniedError
		if !errors.As(err, &denied) {
			t.Fatalf("error=%T %v, want SandboxDeniedError", err, err)
		}
	}
}

func TestExecuteCommandRequiredSandboxFailsClosedWithDecision(t *testing.T) {
	root := t.TempDir()
	_, err := ExecuteCommand(context.Background(), root, Preview{
		Command:     "echo blocked",
		CWD:         ".",
		Timeout:     time.Second,
		SandboxMode: SandboxRequired,
		Sandbox:     SandboxDecision{Mode: SandboxRequired, Probe: "unavailable:missing"},
	})
	var denied *SandboxDeniedError
	if !errors.As(err, &denied) || denied.Mode != SandboxRequired {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestRunCommandRejectsModelControlledSandboxMode(t *testing.T) {
	root := t.TempDir()
	if _, err := (runCommandTool{sandboxMode: SandboxRequired}).Preview(context.Background(), root, `{"command":"echo blocked","sandbox_mode":"off"}`); err == nil {
		t.Fatal("model-controlled sandbox_mode was accepted")
	}
}

func TestRunCommandUsesConfiguredSandboxMode(t *testing.T) {
	root := t.TempDir()
	preview, err := (runCommandTool{sandboxMode: SandboxAuto}).Preview(context.Background(), root, `{"command":"echo ok"}`)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SandboxMode != SandboxAuto {
		t.Fatalf("sandbox mode = %q, want %q", preview.SandboxMode, SandboxAuto)
	}
}

func TestRunCommandSchemaDoesNotExposeSandboxModeToModel(t *testing.T) {
	definition := string(RunCommandDefinition().Function)
	if strings.Contains(definition, "sandbox_mode") {
		t.Fatalf("sandbox policy leaked into model schema: %s", definition)
	}
}
