package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndLoadValidSkill(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".drift", "skills", "project-overview", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const content = "# Project overview\nRead-only workflow.\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Name != "project-overview" {
		t.Fatalf("List() = %#v", items)
	}
	loaded, err := Load(root, "project-overview")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Content != content || loaded.Size != int64(len(content)) {
		t.Fatalf("Load() = %#v", loaded)
	}
}

func TestListSkipsInvalidEntries(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".drift", "skills")
	for _, name := range []string{"valid", "Upper", "bad_name", "missing"} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "valid", "SKILL.md"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Name != "valid" {
		t.Fatalf("List() = %#v", items)
	}
}

func TestLoadRejectsInvalidNameAndLimits(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".drift", "skills", "valid")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "Upper", "bad_name", "../valid", `C:\\secret`} {
		if _, err := Load(root, name); err == nil {
			t.Errorf("Load(%q) succeeded", name)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte(strings.Repeat("x", 64<<10+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "valid"); err == nil {
		t.Fatal("Load() accepted oversized Skill")
	}
}

func TestLoadRejectsInvalidUTF8AndSymlink(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".drift", "skills")
	valid := filepath.Join(base, "valid")
	if err := os.MkdirAll(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(valid, "SKILL.md")
	if err := os.WriteFile(entry, []byte{0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "valid"); err == nil {
		t.Fatal("Load() accepted invalid UTF-8")
	}

	target := filepath.Join(root, "outside.md")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkDir := filepath.Join(base, "linked")
	if err := os.Symlink(valid, symlinkDir); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	items, err := List(root)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, item := range items {
		if item.Name == "linked" {
			t.Fatalf("List() included symlink Skill: %#v", item)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"project-overview", true},
		{"a", true},
		{"a1-b2", true},
		{"", false},
		{"A", false},
		{"a_b", false},
		{"-a", false},
		{strings.Repeat("a", 65), false},
	} {
		if got := ValidateName(tc.name); got != tc.want {
			t.Errorf("ValidateName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
