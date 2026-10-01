package changes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/layout"
)

type Manifest struct {
	Operation string    `json:"operation"`
	Path      string    `json:"path"`
	OldBytes  int       `json:"old_bytes"`
	NewBytes  int       `json:"new_bytes"`
	Decision  string    `json:"decision"`
	Time      time.Time `json:"time"`
	Error     string    `json:"error,omitempty"`
}

type changeSetContextKey struct{}

type ChangeSet struct {
	mu              sync.Mutex
	root, dir, work string
	manifest        ChangeSetManifest
	diff            string
}

type ChangeSetManifest struct {
	ID     string     `json:"id"`
	Time   time.Time  `json:"time"`
	Status string     `json:"status"`
	Files  []Manifest `json:"files"`
}

func WithChangeSet(ctx context.Context, set *ChangeSet) context.Context {
	return context.WithValue(ctx, changeSetContextKey{}, set)
}
func FromContext(ctx context.Context) *ChangeSet {
	set, _ := ctx.Value(changeSetContextKey{}).(*ChangeSet)
	return set
}

func Begin(root string, started time.Time) (*ChangeSet, error) {
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(layout.DateDir(filepath.Join(root, ".drift", "changes"), started), "change-"+layout.FileTimestamp(started)+"-"+id)
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return nil, err
	}
	return &ChangeSet{root: root, dir: dir, work: work, manifest: ChangeSetManifest{ID: id, Time: started.UTC(), Status: "in_progress"}}, nil
}

func (c *ChangeSet) Record(manifest Manifest, diff string, content []byte, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest.Time = c.manifest.Time
	c.manifest.Files = append(c.manifest.Files, manifest)
	c.diff += diff + "\n"
	path := filepath.Join(c.work, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	return c.flushLocked()
}

func (c *ChangeSet) Finalize(status string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.manifest.Files) == 0 {
		return os.RemoveAll(c.dir)
	}
	c.manifest.Status = status
	return c.flushLocked()
}

func (c *ChangeSet) flushLocked() error {
	b, err := json.MarshalIndent(c.manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(c.dir, "manifest.json"), append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.dir, "diff.patch"), []byte(c.diff), 0o600)
}

func NewID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func Record(root string, started time.Time, id string, manifest Manifest, diff string, content []byte, name string) (string, error) {
	changeDir := filepath.Join(layout.DateDir(filepath.Join(root, ".drift", "changes"), started), "change-"+layout.FileTimestamp(started)+"-"+id)
	workDir := filepath.Join(changeDir, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return "", fmt.Errorf("changes: create directory: %w", err)
	}
	manifest.Time = started.UTC()
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("changes: encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(changeDir, "manifest.json"), append(manifestBytes, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("changes: write manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(changeDir, "diff.patch"), []byte(diff), 0o600); err != nil {
		return "", fmt.Errorf("changes: write diff: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, filepath.Base(name)), content, 0o600); err != nil {
		return "", fmt.Errorf("changes: write work content: %w", err)
	}
	return changeDir, nil
}
