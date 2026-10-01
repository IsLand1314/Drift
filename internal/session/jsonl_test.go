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
	if entries[0].Version != 1 || entries[0].Type != string(agent.EventRunStarted) || entries[0].Text != "<redacted>" || entries[0].TextBytes != len("解释 README.md") {
		t.Fatalf("start entry = %#v", entries[0])
	}
	if entries[1].Type != string(agent.EventTextDelta) || entries[1].Text != "<redacted>" {
		t.Fatalf("text entry = %#v, want redacted placeholder", entries[1])
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
	if !strings.Contains(text, "<redacted>") || !strings.Contains(text, "argument_bytes") {
		t.Fatalf("session missing audit redaction metadata: %s", text)
	}
}

func TestJSONLWriterRedactsQuotedAndJSONSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	secret := "quoted-secret"
	if err := writer.Append(agent.Event{
		Type: agent.EventRunStarted,
		Text: `OPENAI_API_KEY="` + secret + `"`,
	}); err != nil {
		t.Fatalf("Append(start) error = %v", err)
	}
	if err := writer.Append(agent.Event{
		Type:      agent.EventToolCall,
		ToolName:  "read_file",
		Arguments: `{"OPENAI_API_KEY":"` + secret + `","password":"demo-password","path":"README.md"}`,
	}); err != nil {
		t.Fatalf("Append(tool) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), secret) {
		t.Fatalf("session leaked quoted or JSON secret: %s", content)
	}
	if strings.Contains(string(content), "demo-password") {
		t.Fatalf("session leaked JSON password: %s", content)
	}
}

func TestJSONLWriterRedactsPathsInAllTextFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	absolutePath := `F:\private\report.txt`
	serializedPath := strings.ReplaceAll(absolutePath, `\`, `\\`)
	for _, event := range []agent.Event{
		{Type: agent.EventRunStarted, Text: "解释 " + absolutePath},
		{Type: agent.EventToolResult, Result: "发现 " + absolutePath},
		{Type: agent.EventError, Error: "无法读取 " + absolutePath},
	} {
		if err := writer.Append(event); err != nil {
			t.Fatalf("Append(%s) error = %v", event.Type, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), serializedPath) {
		t.Fatalf("session leaked path in text/result/error: %s", content)
	}
}

func TestJSONLWriterRedactsSecretAcrossTextDeltas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventTextDelta, Text: "OPENAI_API_KEY="}); err != nil {
		t.Fatalf("Append(prefix) error = %v", err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventTextDelta, Text: "split-secret"}); err != nil {
		t.Fatalf("Append(value) error = %v", err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventRunFinished}); err != nil {
		t.Fatalf("Append(finished) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "split-secret") {
		t.Fatalf("session leaked cross-event secret: %s", content)
	}
}

func TestJSONLWriterRedactsConfiguredSecretAcrossTextDeltas(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		parts  []string
	}{
		{name: "long secret", secret: "split-secret", parts: []string{"prefix split-", "secret suffix"}},
		{name: "two-character prefix", secret: "sk-test-secret", parts: []string{"prefix s", "k-test-secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.jsonl")
			writer, err := NewJSONLWriterWithSecrets(path, "", tc.secret)
			if err != nil {
				t.Fatalf("NewJSONLWriterWithSecrets() error = %v", err)
			}
			for _, text := range tc.parts {
				if err := writer.Append(agent.Event{Type: agent.EventTextDelta, Text: text}); err != nil {
					t.Fatalf("Append(%q) error = %v", text, err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(content), tc.secret) {
				t.Fatalf("session leaked configured cross-event secret: %s", content)
			}
		})
	}
}

func TestJSONLWriterRedactsSplitCredentialAndPathAcrossTextDeltas(t *testing.T) {
	cases := []struct {
		name      string
		parts     []string
		forbidden []string
	}{
		{name: "openai key", parts: []string{"OPENAI_API_", "KEY=split-secret"}, forbidden: []string{"OPENAI_API_", "KEY=split-secret"}},
		{name: "api key", parts: []string{"prefix api", "_key=demo-secret"}, forbidden: []string{"prefix api", "_key=demo-secret"}},
		{name: "api key short", parts: []string{"ap", "i_key=short-secret"}, forbidden: []string{"ap", "i_key=short-secret"}},
		{name: "bearer", parts: []string{"prefix Bearer ", "demo-credential"}, forbidden: []string{"prefix Bearer ", "demo-credential"}},
		{name: "bearer short", parts: []string{"B", "earer short-credential"}, forbidden: []string{"B", "earer short-credential"}},
		{name: "windows path", parts: []string{"路径：C:", `\private\report.txt`}, forbidden: []string{"路径：C:", `C:\private\report.txt`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.jsonl")
			writer, err := NewJSONLWriter(path)
			if err != nil {
				t.Fatalf("NewJSONLWriter() error = %v", err)
			}
			for _, text := range tc.parts {
				if err := writer.Append(agent.Event{Type: agent.EventTextDelta, Text: text}); err != nil {
					t.Fatalf("Append(%q) error = %v", text, err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range tc.forbidden {
				if strings.Contains(string(content), value) {
					t.Fatalf("session leaked split credential or path %q: %s", value, content)
				}
			}
			scanner := bufio.NewScanner(strings.NewReader(string(content)))
			var combined strings.Builder
			for scanner.Scan() {
				var entry Entry
				if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
					t.Fatalf("invalid session line: %v", err)
				}
				combined.WriteString(entry.Text)
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			for _, value := range tc.forbidden {
				if strings.Contains(combined.String(), value) {
					t.Fatalf("session reconstructed split secret or path %q: %s", value, combined.String())
				}
			}
		})
	}
}

func TestJSONLWriterRedactsNormalTextWithConfiguredSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriterWithSecrets(path, "", "sk-test-secret")
	if err != nil {
		t.Fatalf("NewJSONLWriterWithSecrets() error = %v", err)
	}
	for _, text := range []string{"This is a section about README.", "Open", " source text", "It remains readable."} {
		if err := writer.Append(agent.Event{Type: agent.EventTextDelta, Text: text}); err != nil {
			t.Fatalf("Append(%q) error = %v", text, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"text":"<redacted>"`) {
		t.Fatalf("text delta was not stored as a redacted placeholder: %s", content)
	}
	for _, text := range []string{"This is a section about README.", "Open", " source text", "It remains readable."} {
		if strings.Contains(string(content), text) {
			t.Fatalf("text delta leaked raw content %q: %s", text, content)
		}
	}
}

func TestJSONLWriterRedactsQuotedJSONPathsAndCredentialsInTextFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	values := []string{
		`path=/home/alice/private.txt`,
		`路径：/home/alice/private.txt`,
		`路径（/home/alice/private.txt）`,
		`{"path":"/home/alice/private.txt"}`,
		"`C:\\private\\report.txt`",
		`OPENAI_API_KEY='another-secret'`,
		`OPENAI_API_KEY="a\"b tail-leak"`,
		`{"OPENAI_API_KEY":"json-secret"}`,
		`{"password":"a'b c"}`,
		`{"password":"a\"b tail-leak"}`,
		`Authorization: Basic dXNlcjpwYXNz`,
		`Authorization:Basic dXNlcjpwYXNz`,
		`Authorization = Basic dXNlcjpwYXNz`,
		`请读取/home/alice/private.txt`,
	}
	for _, value := range values {
		if err := writer.Append(agent.Event{Type: agent.EventToolResult, Result: value}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if strings.Contains(string(content), value) {
			t.Fatalf("session leaked free-text secret or path %q: %s", value, content)
		}
	}
	if strings.Contains(string(content), "a'b c") {
		t.Fatalf("session leaked mismatched-quote credential value: %s", content)
	}
	if strings.Contains(string(content), "b tail-leak") {
		t.Fatalf("session leaked escaped-quote credential tail: %s", content)
	}
	if strings.Contains(string(content), "dXNlcjpwYXNz") {
		t.Fatalf("session leaked Basic authorization payload: %s", content)
	}
}

func TestJSONLWriterFlushesTextDeltaImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatalf("NewJSONLWriter() error = %v", err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventTextDelta, Text: "第一段"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"text":"<redacted>"`) {
		t.Fatalf("text delta was not flushed immediately: %s", content)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestJSONLWriterStoresCompactionCountersWithoutSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(agent.Event{Type: agent.EventCompactionFinished, Text: "secret summary", BeforeBytes: 200, AfterBytes: 80, MessageCount: 9, KeptMessages: 4}); err != nil {
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
	if strings.Contains(text, "secret summary") || !strings.Contains(text, `"before_bytes":200`) || !strings.Contains(text, `"after_bytes":80`) || !strings.Contains(text, `"kept_messages":4`) {
		t.Fatalf("audit=%s", text)
	}
}

func TestJSONLWriterStoresNumericUsageOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := NewJSONLWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Append(agent.Event{Type: agent.EventModelUsage, InputTokens: 17, OutputTokens: 9, TotalTokens: 26, UsageAvailable: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadEntries(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if entries[0].InputTokens != 17 || entries[0].OutputTokens != 9 || entries[0].TotalTokens != 26 {
		t.Fatalf("usage=%+v", entries[0])
	}
}
