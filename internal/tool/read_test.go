package tool

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "valid", args: `{"path":"README.md"}`, want: "hello\n"},
		{name: "malformed json", args: `{"path":`, want: ""},
		{name: "missing path", args: `{}`, want: ""},
		{name: "unknown field", args: `{"path":"README.md","extra":true}`, want: ""},
		{name: "blank path", args: `{"path":"  "}`, want: ""},
		{name: "absolute path", args: `{"path":"` + filepath.ToSlash(filepath.Join(root, "README.md")) + `"}`, want: ""},
		{name: "parent path", args: `{"path":"../outside.txt"}`, want: ""},
		{name: "parent path within root", args: `{"path":"unused/../README.md"}`, want: ""},
		{name: "directory", args: `{"path":"."}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Read(root, tt.args)
			if tt.want != "" {
				if err != nil {
					t.Fatalf("Read() error = %v", err)
				}
				if got != tt.want {
					t.Fatalf("Read() = %q, want %q", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatal("Read() error = nil, want non-nil")
			}
		})
	}

	t.Run("too large", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(root, "large"), make([]byte, MaxReadBytes+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(root, `{"path":"large"}`); err == nil {
			t.Fatal("Read() error = nil, want non-nil")
		}
	})

	t.Run("dotenv credential files", func(t *testing.T) {
		for _, name := range []string{".env", ".env.local", ".env.example"} {
			t.Run(name, func(t *testing.T) {
				if err := os.WriteFile(filepath.Join(root, name), []byte("OPENAI_API_KEY=synthetic-secret"), 0o600); err != nil {
					t.Fatal(err)
				}
				if got, err := Read(root, `{"path":"`+name+`"}`); err == nil || got != "" {
					t.Fatalf("Read() = %q, %v; want denied dotenv credential file", got, err)
				}
			})
		}
	})

	t.Run("dotenv symlink alias", func(t *testing.T) {
		secretFile := filepath.Join(root, ".env")
		alias := filepath.Join(root, "env-link")
		if err := os.WriteFile(secretFile, []byte("OPENAI_API_KEY=synthetic-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(".env", alias); err != nil {
			if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrInvalid) || runtime.GOOS == "windows" {
				t.Skipf("symlink unsupported: %v", err)
			}
			t.Fatal(err)
		}
		if got, err := Read(root, `{"path":"env-link"}`); err == nil || got != "" {
			t.Fatalf("Read() = %q, %v; want denied dotenv symlink alias", got, err)
		}
	})

	t.Run("symlink outside", func(t *testing.T) {
		link := filepath.Join(root, "link.txt")
		if err := os.Symlink(outside, link); err != nil {
			if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrInvalid) || runtime.GOOS == "windows" {
				t.Skipf("symlink unsupported: %v", err)
			}
			t.Fatal(err)
		}
		if _, err := Read(root, `{"path":"link.txt"}`); err == nil {
			t.Fatal("Read() error = nil, want non-nil")
		}
	})
}

func TestReadDefinition(t *testing.T) {
	definition := ReadDefinition()
	if definition.Type != "function" {
		t.Fatalf("Type = %q, want function", definition.Type)
	}
	var function struct {
		Name       string `json:"name"`
		Parameters struct {
			Type                 string                     `json:"type"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties bool                       `json:"additionalProperties"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(definition.Function, &function); err != nil {
		t.Fatal(err)
	}
	if function.Name != "read_file" {
		t.Fatalf("name = %q, want read_file", function.Name)
	}
	if function.Parameters.Type != "object" || len(function.Parameters.Properties) != 1 || function.Parameters.Properties["path"] == nil || len(function.Parameters.Required) != 1 || function.Parameters.Required[0] != "path" || function.Parameters.AdditionalProperties {
		t.Fatalf("unexpected parameters schema: %s", strings.TrimSpace(string(definition.Function)))
	}
}
