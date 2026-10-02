package tool

import (
	"bytes"
	"fmt"
	"os"
)

// checkPreviewState is the optimistic file-state cache carried by Preview.
// The ceiling is an in-memory byte comparison; replace with a digest/cache only
// if large-file approval latency becomes measurable.
func checkPreviewState(workspace *os.Root, preview Preview) error {
	info, err := workspace.Lstat(preview.Path)
	if !preview.BeforeExists {
		if os.IsNotExist(err) {
			return nil
		}
		if err == nil {
			return fmt.Errorf("target changed since preview")
		}
		return fmt.Errorf("check target state: %w", err)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("target changed since preview")
	}
	current, err := workspace.ReadFile(preview.Path)
	if err != nil || !bytes.Equal(current, preview.Before) {
		return fmt.Errorf("target changed since preview")
	}
	return nil
}
