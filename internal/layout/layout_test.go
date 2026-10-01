package layout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareCreatesCurrentStorageRootsWithoutLegacyMigration(t *testing.T) {
	root := t.TempDir()
	l := ForWorkspace(root)
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{l.Sessions, l.Audits, l.Skills} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("storage root %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "conversations")); !os.IsNotExist(err) {
		t.Fatalf("legacy conversations unexpectedly created: %v", err)
	}
}

func TestDateDirUsesUTCYearMonthDay(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	got := DateDir(root, time.Date(2026, 10, 1, 1, 2, 3, 0, time.FixedZone("UTC+8", 8*60*60)))
	want := filepath.Join(root, "2026", "09", "30")
	if got != want {
		t.Fatalf("DateDir=%q, want %q", got, want)
	}
}

func TestFileTimestampIsWindowsSafe(t *testing.T) {
	got := FileTimestamp(time.Date(2026, 10, 1, 15, 36, 20, 0, time.UTC))
	if got != "2026-10-01T15-36-20Z" {
		t.Fatalf("FileTimestamp=%q", got)
	}
}
