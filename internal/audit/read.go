package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxAuditEntryBytes = 2 << 20

// ReadEntries 读取 JSONL 审计记录；它只解析结构，不把正文打印到任何输出。
func ReadEntries(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxAuditEntryBytes)
	entries := make([]Entry, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, fmt.Errorf("audit: decode line %d: %w", lineNumber, err)
		}
		entry.FinishReason = sanitizeFinishReason(entry.FinishReason)
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("audit: read %s: %w", filepath.Base(path), err)
	}
	return entries, nil
}

// ListFiles 返回日期目录中按修改时间倒序排列的 run-*.jsonl 普通文件。
func ListFiles(root string) ([]string, error) {
	return ListFilesInRoots([]string{root})
}

// ListFilesInRoots returns files from the supplied audit roots recursively.
func ListFilesInRoots(roots []string) ([]string, error) {
	all := make([]string, 0)
	for _, root := range roots {
		files, err := listFiles(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
	}
	sort.Slice(all, func(i, j int) bool {
		li, _ := os.Stat(all[i])
		lj, _ := os.Stat(all[j])
		if li != nil && lj != nil && !li.ModTime().Equal(lj.ModTime()) {
			return li.ModTime().After(lj.ModTime())
		}
		return all[i] > all[j]
	})
	return all, nil
}

func listFiles(root string) ([]string, error) {
	type item struct {
		path    string
		modTime int64
	}
	files := make([]item, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "run-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, item{path: path, modTime: info.ModTime().UnixNano()})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("audit: list directory: %w", err)
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
