package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".drift", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".drift", "sessions", "run-1.jsonl"), []byte(`{"version":1,"type":"run_started"}`+"\n"+`{"version":1,"type":"run_finished"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "list"}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, out=%q, stderr=%q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "run-1.jsonl") || !strings.Contains(out.String(), "events=2") {
		t.Fatalf("out = %q", out.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSessionShowDoesNotPrintLegacyContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-1.jsonl")
	content := strings.Join([]string{
		`{"version":1,"type":"run_started","text":"secret prompt","text_bytes":13}`,
		`{"version":1,"type":"tool_call","tool":"read_file","path":"README.md","arguments":"secret args","argument_bytes":11}`,
		`{"version":1,"type":"tool_result","tool":"read_file","result":"private file contents","result_bytes":20}`,
		`{"version":1,"type":"text_delta","text":"<redacted>","text_bytes":4}`,
		`{"version":1,"type":"text_delta","text":"<redacted>","text_bytes":5}`,
		`{"version":1,"type":"error","error":"模型连接失败","stage":"provider_transport","finish_reason":"length"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "show", path}, func(string) string { return "" }, &out, &stderr); code != 0 {
		t.Fatalf("code = %d, out=%q, stderr=%q", code, out.String(), stderr.String())
	}
	text := out.String()
	for _, value := range []string{"secret prompt", "secret args", "private file contents"} {
		if strings.Contains(text, value) {
			t.Fatalf("show leaked %q: %q", value, text)
		}
	}
	if !strings.Contains(text, "tool_call tool=read_file path=README.md argument_bytes=11") || !strings.Contains(text, "tool_result tool=read_file result_bytes=20") || !strings.Contains(text, "error stage=provider_transport finish_reason=length") {
		t.Fatalf("show = %q", text)
	}
	if !strings.Contains(text, "text_delta events=2 text_bytes=9") {
		t.Fatalf("show did not aggregate text deltas: %q", text)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSessionShowMissingFileReturnsUsageError(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"session", "show", filepath.Join(t.TempDir(), "missing.jsonl")}, func(string) string { return "" }, &out, &stderr); code != 2 {
		t.Fatalf("code = %d, out=%q, stderr=%q", code, out.String(), stderr.String())
	}
}
