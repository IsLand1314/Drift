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
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err := os.Mkdir(filepath.Join(root, "nested", "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "regular-dir"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("not a directory"), 0o644); err != nil {
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
	want := []string{"a.txt", "file.txt", "nested/b.txt", "nested/ignored-dir-file", "regular-dir"}
	if len(got) != len(want) {
		t.Fatalf("walked paths = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walked paths = %#v, want %#v", got, want)
		}
	}

	t.Run("symlinks are skipped and cannot be traversal roots", func(t *testing.T) {
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
		if err := walkRegularFiles(workspace, "link-dir/child", func(string, fs.DirEntry) error { return nil }); err == nil {
			t.Fatal("walkRegularFiles(parent symlink) = nil, want error")
		}
		var symlinkPaths []string
		if err := walkRegularFiles(workspace, "", func(path string, _ fs.DirEntry) error {
			symlinkPaths = append(symlinkPaths, path)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, path := range symlinkPaths {
			if path == "link.txt" || path == "link-dir" || path == "link-dir/b.txt" {
				t.Fatalf("walked symlink path %q", path)
			}
		}
	})
}

func TestWalkRegularFilesSkipsDiscoveryDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", ".foxcode", ".codex", ".claude", "node_modules", ".venv", "__pycache__", ".tox", ".mypy_cache"} {
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "hidden.txt"), []byte(dir), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("visible"), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace, err := openWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	var got []string
	if err := walkRegularFiles(workspace, "", func(path string, _ fs.DirEntry) error {
		got = append(got, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "visible.txt" {
		t.Fatalf("walked paths = %#v, want visible.txt only", got)
	}
}
