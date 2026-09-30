package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveWorkspace(t *testing.T) {
	launchDir := t.TempDir()
	targetDir := filepath.Join(launchDir, "workspace")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "README.md"), []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(targetDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, ".env.local"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		rawTarget string
		want      workspaceSelection
	}{
		{name: "default", want: workspaceSelection{Root: launchDir}},
		{name: "absolute directory", rawTarget: targetDir, want: workspaceSelection{Root: targetDir}},
		{name: "relative directory", rawTarget: "workspace", want: workspaceSelection{Root: targetDir}},
		{name: "absolute file", rawTarget: filepath.Join(targetDir, "README.md"), want: workspaceSelection{Root: targetDir, Focus: "README.md"}},
		{name: "relative file", rawTarget: filepath.Join("workspace", "README.md"), want: workspaceSelection{Root: targetDir, Focus: "README.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWorkspace(launchDir, tt.rawTarget)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("selection = %#v, want %#v", got, tt.want)
			}
		})
	}

	for _, name := range []string{"missing", ".env.local"} {
		t.Run("reject "+name, func(t *testing.T) {
			_, err := resolveWorkspace(launchDir, filepath.Join(targetDir, name))
			if err == nil {
				t.Fatal("resolveWorkspace() = nil error, want rejection")
			}
		})
	}

	t.Run("reject symlink", func(t *testing.T) {
		link := filepath.Join(launchDir, "workspace-link")
		if err := os.Symlink(targetDir, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink unsupported: %v", err)
			}
			t.Fatal(err)
		}
		if _, err := resolveWorkspace(launchDir, link); err == nil {
			t.Fatal("resolveWorkspace() = nil error, want symlink rejection")
		}
	})
}
