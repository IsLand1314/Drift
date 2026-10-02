package changes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Restore reverts one completed change set after verifying every target still
// contains the post-change snapshot. It refuses partial, stale, or unsafe sets.
func Restore(root, changeDir string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(changeDir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(filepath.Join(rootAbs, ".drift", "changes"), dirAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return fmt.Errorf("changes: change set is outside workspace changes directory")
	}
	b, err := os.ReadFile(filepath.Join(dirAbs, "manifest.json"))
	if err != nil {
		return fmt.Errorf("changes: read manifest: %w", err)
	}
	var set ChangeSetManifest
	if err := json.Unmarshal(b, &set); err != nil {
		return fmt.Errorf("changes: decode manifest: %w", err)
	}
	// Older single-operation records used Manifest directly. Treat them as a
	// completed one-file set so existing change records remain recoverable.
	if set.Status == "" && set.ID == "" && len(set.Files) == 0 {
		var file Manifest
		if err := json.Unmarshal(b, &file); err != nil || file.Operation == "" || file.Path == "" {
			return fmt.Errorf("changes: invalid manifest")
		}
		set.Status, set.Files = "complete", []Manifest{file}
	}
	if set.Status != "complete" || len(set.Files) == 0 {
		return fmt.Errorf("changes: only completed non-empty change sets can be restored")
	}
	type action struct {
		path, before string
		remove       bool
		data         []byte
	}
	actions := make([]action, 0, len(set.Files))
	seen := map[string]bool{}
	for _, file := range set.Files {
		path := filepath.FromSlash(file.Path)
		slashPath := filepath.ToSlash(path)
		if path == "." || filepath.IsAbs(path) || strings.HasPrefix(slashPath, "../") || slashPath == ".drift" || strings.HasPrefix(slashPath, ".drift/") || strings.Contains(slashPath, "/.drift/") {
			return fmt.Errorf("changes: unsafe path %q", file.Path)
		}
		if seen[file.Path] {
			return fmt.Errorf("changes: duplicate path %q", file.Path)
		}
		seen[file.Path] = true
		target := filepath.Join(rootAbs, path)
		snapshot := filepath.Join(dirAbs, "work", path)
		var after []byte
		if file.Operation != "delete_file" {
			after, err = os.ReadFile(snapshot)
			if err != nil {
				return fmt.Errorf("changes: read after snapshot %s: %w", file.Path, err)
			}
		}
		current, readErr := os.ReadFile(target)
		switch file.Operation {
		case "create_file":
			if readErr == nil && string(current) != string(after) {
				return fmt.Errorf("changes: target changed %q", file.Path)
			}
			if readErr != nil && !os.IsNotExist(readErr) {
				return fmt.Errorf("changes: inspect target %q: %w", file.Path, readErr)
			}
			actions = append(actions, action{path: target, remove: readErr == nil})
		case "edit_file", "overwrite_file":
			if readErr != nil || string(current) != string(after) {
				return fmt.Errorf("changes: target changed %q", file.Path)
			}
			before, err := os.ReadFile(snapshot + ".before")
			if err != nil {
				return fmt.Errorf("changes: read before snapshot %s: %w", file.Path, err)
			}
			actions = append(actions, action{path: target, data: before})
		case "delete_file":
			if readErr == nil {
				return fmt.Errorf("changes: deleted target was recreated %q", file.Path)
			}
			if !os.IsNotExist(readErr) {
				return fmt.Errorf("changes: inspect target %q: %w", file.Path, readErr)
			}
			before, err := os.ReadFile(snapshot + ".before")
			if err != nil {
				return fmt.Errorf("changes: read deleted snapshot %s: %w", file.Path, err)
			}
			actions = append(actions, action{path: target, data: before})
		default:
			return fmt.Errorf("changes: unsupported operation %q", file.Operation)
		}
	}
	for _, a := range actions {
		if a.remove {
			if err := os.Remove(a.path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(a.path), ".drift-restore-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		if _, err = tmp.Write(a.data); err == nil {
			err = tmp.Chmod(0o600)
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmpName, a.path)
		}
		_ = os.Remove(tmpName)
		if err != nil {
			return err
		}
	}
	return nil
}
