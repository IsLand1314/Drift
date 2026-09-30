package tool

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestValidateRelativePath(t *testing.T) {
	for _, path := range []string{"../outside", "/tmp", `.env`} {
		if err := validateRelativePath(path, false); err == nil {
			t.Fatalf("validateRelativePath(%q) = nil, want error", path)
		}
	}
	if err := validateRelativePath("", true); err != nil {
		t.Fatalf("validateRelativePath(empty, true) = %v, want nil", err)
	}
	if err := validateRelativePath("", false); err == nil {
		t.Fatal("validateRelativePath(empty, false) = nil, want error")
	}
	if err := validateRelativePath(`nested\\file.txt`, false); err != nil {
		t.Fatalf("validateRelativePath(backslash path) = %v, want nil", err)
	}
}

func TestWalkRegularFilesRejectsUnsafeDirectory(t *testing.T) {
	root := t.TempDir()
	workspace, err := openWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()

	for _, path := range []string{"../outside", "/tmp", "file.txt", `.env`} {
		err := walkRegularFiles(workspace, path, func(string, fs.DirEntry) error { return nil })
		if err == nil {
			t.Fatalf("walkRegularFiles(%q) = nil, want error", path)
		}
	}
}

func TestWalkRegularFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", filepath.Join("nested", "b.txt"), ".env", ".env.local"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "ignored-dir-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "regular-dir"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(root, "link.txt")); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrInvalid) || runtime.GOOS == "windows" {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink("nested", filepath.Join(root, "link-dir")); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrInvalid) || runtime.GOOS == "windows" {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Fatal(err)
	}

	workspace, err := openWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	var got []string
	err = walkRegularFiles(workspace, "", func(path string, _ fs.DirEntry) error {
		got = append(got, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"a.txt", "nested/b.txt", "nested/ignored-dir-file", "regular-dir"}
	if len(got) != len(want) {
		t.Fatalf("walked paths = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walked paths = %#v, want %#v", got, want)
		}
	}
}
