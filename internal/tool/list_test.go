package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"z.txt", "a.txt", filepath.Join("nested", "b.txt"), filepath.Join("nested", "a.txt"), ".env", ".env.local"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("lists files sorted and limits relative directory", func(t *testing.T) {
		got, err := List(root, `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if got != "a.txt\nnested/a.txt\nnested/b.txt\nz.txt" {
			t.Fatalf("List() = %q", got)
		}
		got, err = List(root, `{"path":"nested"}`)
		if err != nil {
			t.Fatal(err)
		}
		if got != "nested/a.txt\nnested/b.txt" {
			t.Fatalf("relative List() = %q", got)
		}
	})
	t.Run("dotenv and symlink absent", func(t *testing.T) {
		dotenvDir := filepath.Join(root, ".env.private")
		if err := os.MkdirAll(dotenvDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dotenvDir, "secret.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dotenvDir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dotenvDir, "sub", "secret.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := List(root, `{"path":".env.private"}`); err == nil {
			t.Fatal("List(.env.private) error = nil")
		}
		if _, err := List(root, `{"path":".env.private/sub"}`); err == nil {
			t.Fatal("List(.env.private/sub) error = nil")
		}
		if err := os.Symlink("a.txt", filepath.Join(root, "link.txt")); err != nil {
			if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrInvalid) || runtime.GOOS == "windows" {
				t.Skip(err)
			}
			t.Fatal(err)
		}
		got, err := List(root, `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, ".env") || strings.Contains(got, "link.txt") {
			t.Fatalf("List() = %q", got)
		}
	})
	for _, path := range []string{"..", "../outside", filepath.Join(root, "a.txt"), ".env", "a.txt"} {
		t.Run("reject "+path, func(t *testing.T) {
			if _, err := List(root, `{"path":"`+filepath.ToSlash(path)+`"}`); err == nil {
				t.Fatalf("List(%q) error = nil", path)
			}
		})
	}
	t.Run("truncates", func(t *testing.T) {
		for i := 0; i < MaxListEntries+1; i++ {
			if err := os.WriteFile(filepath.Join(root, "file-"+string(rune('a'+i/26))+string(rune('a'+i%26))), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := List(root, `{}`)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(got, "\n")
		if len(lines) != MaxListEntries+1 || lines[len(lines)-1] != "list_files: results truncated at 200 entries" {
			t.Fatalf("line count/marker: %d %q", len(lines), lines[len(lines)-1])
		}
	})
	for _, raw := range []string{`{"path":`, `{"extra":true}`, `[]`, `null`, `{"path":null}`} {
		t.Run("reject JSON "+raw, func(t *testing.T) {
			if _, err := List(root, raw); err == nil {
				t.Fatalf("List(%q) error = nil", raw)
			}
		})
	}
	t.Run("tool delegates", func(t *testing.T) {
		var tool Tool = listFilesTool{}
		if tool.Name() != "list_files" {
			t.Fatal(tool.Name())
		}
		if _, err := tool.Execute(context.Background(), root, `{}`); err != nil {
			t.Fatal(err)
		}
	})
}

func TestListDefinition(t *testing.T) {
	definition := ListDefinition()
	if definition.Type != "function" {
		t.Fatalf("Type = %q", definition.Type)
	}
	var function struct {
		Name       string `json:"name"`
		Parameters struct {
			Type                 string                     `json:"type"`
			Properties           map[string]json.RawMessage `json:"properties"`
			AdditionalProperties bool                       `json:"additionalProperties"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(definition.Function, &function); err != nil {
		t.Fatal(err)
	}
	if function.Name != "list_files" || function.Parameters.Type != "object" || function.Parameters.Properties["path"] == nil || function.Parameters.AdditionalProperties {
		t.Fatalf("schema = %s", definition.Function)
	}
}
