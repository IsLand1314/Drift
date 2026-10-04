package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// fileStateCache is process-local. It is deliberately not persisted: a resumed
// session must reread files before editing them.
type fileStateCache struct {
	mu    sync.Mutex
	items map[string]fileState
}

type fileState struct {
	Size    int64
	ModTime time.Time
	Digest  string
}

func newFileStateCache() *fileStateCache { return &fileStateCache{items: make(map[string]fileState)} }

func cacheKey(root, path string) string {
	abs, _ := filepath.Abs(root)
	return filepath.Clean(abs) + "\x00" + filepath.ToSlash(filepath.Clean(path))
}

func (c *fileStateCache) Record(root, path string) error {
	state, err := fingerprint(root, path)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.items[cacheKey(root, path)] = state
	c.mu.Unlock()
	return nil
}

func (c *fileStateCache) Check(root, path string) error {
	key := cacheKey(root, path)
	c.mu.Lock()
	expected, ok := c.items[key]
	c.mu.Unlock()
	if !ok {
		return nil
	}
	actual, err := fingerprint(root, path)
	if err != nil || actual != expected {
		return fmt.Errorf("target changed since ReadFile")
	}
	return nil
}

func (c *fileStateCache) Invalidate(root, path string) {
	c.mu.Lock()
	delete(c.items, cacheKey(root, path))
	c.mu.Unlock()
}

func fingerprint(root, path string) (fileState, error) {
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return fileState{}, err
	}
	defer workspace.Close()
	info, err := workspace.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fileState{}, fmt.Errorf("file is not a regular file")
	}
	file, err := workspace.Open(path)
	if err != nil {
		return fileState{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fileState{}, err
	}
	return fileState{Size: info.Size(), ModTime: info.ModTime(), Digest: hex.EncodeToString(hash.Sum(nil))}, nil
}

func readPath(raw string) string {
	var args readArguments
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Path)
}
