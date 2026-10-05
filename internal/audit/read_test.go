package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestJSONLWriterStoresAuditMetadataOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "请读取 README.md"
	const arguments = `{"path":"README.md","query":"secret"}`
	const result = "private file contents"
	if err := writer.Append(agent.Event{Type: agent.EventRunStarted, Text: prompt}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventToolCall, ToolName: "ReadFile", Arguments: arguments}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventToolResult, ToolName: "ReadFile", Result: result}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventRunFinished, FinishReason: "stop"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventRunFinished, FinishReason: "length\nforged"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, value := range []string{prompt, arguments, result} {
		if strings.Contains(text, value) {
			t.Fatalf("audit leaked %q: %s", value, text)
		}
	}
	entries, err := ReadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].TextBytes != len(prompt) || entries[1].ArgumentBytes != len(arguments) || entries[2].ResultBytes != len(result) || entries[3].FinishReason != "stop" || entries[4].FinishReason != "" {
		t.Fatalf("audit metadata = %#v", entries)
	}
}

func TestJSONLWriterStoresCommandMetadataWithoutCommandBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	command := "echo private-command-output"
	if err := writer.Append(agent.Event{Type: agent.EventPermissionRequest, ToolName: "Bash", Command: command, CWD: "."}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventToolResult, ToolName: "Bash", Result: "private-command-output"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), command) || strings.Contains(string(content), "private-command-output") {
		t.Fatalf("command data leaked: %s", content)
	}
	entries, err := ReadEntries(path)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if entries[0].CommandBytes != len(command) || entries[0].CWD != "." || entries[1].Result != "<redacted>" {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestJSONLWriterRecordsOnlySafeRelativeToolPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"path":"src/README.md"}`,
		`{"path":"C:\\secret.txt"}`,
		`{"path":"../secret.txt"}`,
		`{"path":".env"}`,
	} {
		if err := writer.Append(agent.Event{Type: agent.EventToolCall, ToolName: "ReadFile", Arguments: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, `"path":"src/README.md"`) {
		t.Fatalf("safe relative path missing: %s", text)
	}
	for _, value := range []string{`C:\\secret.txt`, `../secret.txt`, `.env`} {
		if strings.Contains(text, value) {
			t.Fatalf("unsafe path leaked %q: %s", value, text)
		}
	}
}

func TestReadEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte(`{"version":1,"type":"run_started"}`+"\n"+`{"version":1,"type":"run_finished"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Type != "run_started" || entries[1].Type != "run_finished" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestListFiles(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "run-old.jsonl")
	newPath := filepath.Join(root, "run-new.jsonl")
	for _, path := range []string{oldPath, newPath, filepath.Join(root, "notes.txt")} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(oldPath, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, time.Unix(2, 0), time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	files, err := ListFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != newPath || files[1] != oldPath {
		t.Fatalf("files = %#v", files)
	}
}
