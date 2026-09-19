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

var sensitivePattern = regexp.MustCompile("(?i)(openai_api_key\\s*=\\s*|authorization:\\s*bearer\\s+|bearer\\s+)[^\\s\"']+")

// JSONLWriter 将事件以追加模式写入一个本地 JSONL 文件。
type JSONLWriter struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
	closed bool
}

// NewJSONLWriter 创建父目录并打开指定的 JSONL 文件。
func NewJSONLWriter(path string) (*JSONLWriter, error) {
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
		file:   file,
		writer: bufio.NewWriter(file),
	}, nil
}

// Append 编码一条完整记录并立即 Flush，保证每次调用对应一行。
func (w *JSONLWriter) Append(event agent.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("session: writer is closed")
	}
	entry := entryFromEvent(event)
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

// Close Flush 并关闭文件；重复关闭是幂等的。
func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	flushErr := w.writer.Flush()
	closeErr := w.file.Close()
	if flushErr != nil {
		return fmt.Errorf("session: flush on close: %w", flushErr)
	}
	if closeErr != nil {
		return fmt.Errorf("session: close file: %w", closeErr)
	}
	return nil
}

func entryFromEvent(event agent.Event) Entry {
	return Entry{
		Version:    1,
		Type:       string(event.Type),
		Time:       time.Now().UTC(),
		Text:       redactText(event.Text),
		ToolCallID: redactText(event.ToolCallID),
		Tool:       redactText(event.ToolName),
		Arguments:  sanitizeArguments(event.Arguments),
		Result:     redactText(event.Result),
		Error:      redactText(event.Error),
	}
}

func sanitizeArguments(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return redactText(raw)
	}
	if path, ok := value["path"].(string); ok && restrictedPath(path) {
		value["path"] = "<restricted>"
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return redactText(raw)
	}
	return redactText(strings.TrimSpace(buffer.String()))
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

func redactText(value string) string {
	return sensitivePattern.ReplaceAllString(value, "$1<redacted>")
}
