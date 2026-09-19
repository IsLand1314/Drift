package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

func TestJSONLWriterAppendsVersionedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".drift", "sessions", "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventRunStarted, Text: "解释 README.md"}); err != nil {
		t.Fatalf("Append(start) error = %v", err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventTextDelta, Text: "项目说明"}); err != nil {
		t.Fatalf("Append(text) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var entries []Entry
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("unmarshal JSONL line %q: %v", scanner.Text(), err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Version != 1 || entries[0].Type != string(agent.EventRunStarted) || entries[0].Text != "解释 README.md" {
		t.Fatalf("start entry = %#v", entries[0])
	}
	if entries[1].Type != string(agent.EventTextDelta) || entries[1].Text != "项目说明" {
		t.Fatalf("text entry = %#v", entries[1])
	}
	for _, entry := range entries {
		if entry.Time.IsZero() || entry.Time.Location() != time.UTC {
			t.Fatalf("entry time = %v, want non-zero UTC", entry.Time)
		}
	}
}

func TestJSONLWriterCreatesParentAndRejectsAppendAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventRunFinished}); err == nil {
		t.Fatal("Append() after Close() error = nil, want error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file was not created: %v", err)
	}
}

func TestJSONLWriterRedactsSecretsAndAbsolutePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	secret := "synthetic-openai-key"
	absolutePath := "C:\\workspace\\secret.txt"
	if err := writer.Append(agent.Event{
		Type: agent.EventRunStarted,
		Text: "OPENAI_API_KEY=" + secret,
	}); err != nil {
		t.Fatalf("Append(start) error = %v", err)
	}
	if err := writer.Append(agent.Event{
		Type:       agent.EventToolCall,
		ToolCallID: "call-1",
		ToolName:   "read_file",
		Arguments:  `{"path":"C:\\\\workspace\\\\secret.txt"}`,
	}); err != nil {
		t.Fatalf("Append(tool) error = %v", err)
	}
	if err := writer.Append(agent.Event{
		Type:  agent.EventError,
		Error: "Authorization: Bearer " + secret,
	}); err != nil {
		t.Fatalf("Append(error) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, secret) {
		t.Fatalf("session leaked secret: %s", text)
	}
	if strings.Contains(text, absolutePath) {
		t.Fatalf("session leaked absolute path: %s", text)
	}
	if !strings.Contains(text, "<redacted>") || !strings.Contains(text, "<restricted>") {
		t.Fatalf("session missing redaction markers: %s", text)
	}
}
