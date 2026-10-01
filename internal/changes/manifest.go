package changes

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
