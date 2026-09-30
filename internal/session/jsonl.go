package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

var sensitivePattern = regexp.MustCompile("(?i)(authorization\\s*[:=]\\s*(?:[a-z]+\\s+)?|bearer\\s+)[^\\s\"',}]+")
var credentialPattern = regexp.MustCompile("(?i)([\\\"']?(?:openai[_-]?api[_-]?key|api[_-]?key|authorization|access[_-]?token|refresh[_-]?token|password|secret)[\\\"']?\\s*[:=]\\s*)(?:\"(?:\\\\.|[^\"\\\\])*\"|'[^']*'|[^,\\s}\\]]*)")
var windowsPathPattern = regexp.MustCompile("(?i)(?:[a-z]:[\\\\/]|\\\\\\\\)[^\\s\"'`<>\\]}]+")
var unixPathPattern = regexp.MustCompile("(^|[^A-Za-z0-9_])\\/[^\\s\"'`<>\\]}]+")

type sanitizer struct {
	root    string
	secrets []string
}

// JSONLWriter 将事件以追加模式写入一个本地 JSONL 文件。
type JSONLWriter struct {
	mu        sync.Mutex
	file      *os.File
	writer    *bufio.Writer
	closed    bool
	sanitizer sanitizer
}

// NewJSONLWriter 创建父目录并打开指定的 JSONL 文件。
func NewJSONLWriter(path string) (*JSONLWriter, error) {
	return newJSONLWriter(path, "", nil)
}

// NewJSONLWriterWithSecrets 创建带 workspace 和配置密钥脱敏规则的 Writer。
func NewJSONLWriterWithSecrets(path, root string, secrets ...string) (*JSONLWriter, error) {
	return newJSONLWriter(path, root, secrets)
}

func newJSONLWriter(path, root string, secrets []string) (*JSONLWriter, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session: path is blank")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("session: create directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: open JSONL file: %w", err)
	}
	return &JSONLWriter{
		file:      file,
		writer:    bufio.NewWriter(file),
		sanitizer: sanitizer{root: root, secrets: append([]string(nil), secrets...)},
	}, nil
}

// Append 编码一条完整记录并立即 Flush，保证每次调用对应一行。
func (w *JSONLWriter) Append(event agent.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("session: writer is closed")
	}
	return w.appendEntryLocked(entryFromEvent(event, w.sanitizer))
}

// Close Flush 并关闭文件；重复关闭是幂等的。
func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	finalFlushErr := w.writer.Flush()
	closeErr := w.file.Close()
	if finalFlushErr != nil {
		return fmt.Errorf("session: flush on close: %w", finalFlushErr)
	}
	if closeErr != nil {
		return fmt.Errorf("session: close file: %w", closeErr)
	}
	return nil
}

func (w *JSONLWriter) appendEntryLocked(entry Entry) error {
	encoder := json.NewEncoder(w.writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entry); err != nil {
		return fmt.Errorf("session: encode entry: %w", err)
	}
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("session: flush entry: %w", err)
	}
	return nil
}

func entryFromEvent(event agent.Event, clean sanitizer) Entry {
	text := clean.text(event.Text)
	if event.Type == agent.EventTextDelta {
		text = "<redacted>"
	}
	return Entry{
		Version:    1,
		Type:       string(event.Type),
		Time:       time.Now().UTC(),
		Text:       text,
		ToolCallID: clean.text(event.ToolCallID),
		Tool:       clean.text(event.ToolName),
		Arguments:  sanitizeArguments(event.Arguments, clean),
		Result:     clean.text(event.Result),
		Error:      clean.text(event.Error),
		Stage:      clean.text(event.Stage),
	}
}

func sanitizeArguments(raw string, clean sanitizer) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "<invalid-json>"
	}
	value = sanitizeJSONValue(value, clean)
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "<invalid-json>"
	}
	return strings.TrimSpace(buffer.String())
}

func sanitizeJSONValue(value any, clean sanitizer) any {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if sensitiveJSONKey(key) {
				current[key] = "<redacted>"
				continue
			}
			if key == "path" {
				if path, ok := child.(string); ok && restrictedPath(path) {
					current[key] = "<restricted>"
					continue
				}
			}
			current[key] = sanitizeJSONValue(child, clean)
		}
		return current
	case []any:
		for index, child := range current {
			current[index] = sanitizeJSONValue(child, clean)
		}
		return current
	case string:
		return clean.text(current)
	default:
		return value
	}
}

func sensitiveJSONKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	return strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "secret")
}

func restrictedPath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	normalized := strings.ReplaceAll(path, "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		return true
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func (clean sanitizer) text(value string) string {
	for _, secret := range clean.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	value = sensitivePattern.ReplaceAllString(value, "$1<redacted>")
	value = credentialPattern.ReplaceAllString(value, "$1<redacted>")
	if clean.root != "" {
		value = strings.ReplaceAll(value, clean.root, "<workspace>")
		value = strings.ReplaceAll(value, filepath.ToSlash(clean.root), "<workspace>")
	}
	value = windowsPathPattern.ReplaceAllString(value, "<path>")
	return unixPathPattern.ReplaceAllString(value, "$1<path>")
}
