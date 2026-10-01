// Package layout centralizes the workspace-local Drift storage directories.
package layout

import (
	"os"
	"path/filepath"
	"time"
)

type Layout struct {
	Root     string
	Sessions string
	Audits   string
	Skills   string
}

func ForWorkspace(workspace string) Layout {
	drift := filepath.Join(workspace, ".drift")
	return Layout{
		Root:     drift,
		Sessions: filepath.Join(drift, "sessions"),
		Audits:   filepath.Join(drift, "audits"),
		Skills:   filepath.Join(drift, "skills"),
	}
}

func (l Layout) Prepare() error {
	for _, path := range []string{l.Sessions, l.Audits, l.Skills} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func DateDir(root string, t time.Time) string {
	t = t.UTC()
	return filepath.Join(root, t.Format("2006"), t.Format("01"), t.Format("02"))
}

func FileTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15-04-05Z")
}
