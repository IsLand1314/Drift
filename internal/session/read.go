package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxSessionEntryBytes = 2 << 20

// ReadEntries 读取 JSONL 审计记录；它只解析结构，不把正文打印到任何输出。
func ReadEntries(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxSessionEntryBytes)
	entries := make([]Entry, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, fmt.Errorf("session: decode line %d: %w", lineNumber, err)
		}
		entry.FinishReason = sanitizeFinishReason(entry.FinishReason)
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("session: read %s: %w", filepath.Base(path), err)
	}
	return entries, nil
}

// ListFiles 返回目录中按修改时间倒序排列的 run-*.jsonl 普通文件。
func ListFiles(root string) ([]string, error) {
	items, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("session: list directory: %w", err)
	}
	type item struct {
		path    string
		modTime int64
	}
	files := make([]item, 0, len(items))
	for _, entry := range items {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), "run-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, item{path: filepath.Join(root, entry.Name()), modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime != files[j].modTime {
			return files[i].modTime > files[j].modTime
		}
		return files[i].path > files[j].path
	})
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.path
	}
	return paths, nil
}
