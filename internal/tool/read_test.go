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

	"github.com/IsLand1314/Drift/internal/llm"
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

	t.Run("symlink directory component", func(t *testing.T) {
		targetDir := filepath.Join(root, "secret-dir")
		aliasDir := filepath.Join(root, "dir-link")
		if err := os.Mkdir(targetDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(targetDir, "secret.txt"), []byte("synthetic-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("secret-dir", aliasDir); err != nil {
			if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrInvalid) || runtime.GOOS == "windows" {
				t.Skipf("symlink unsupported: %v", err)
			}
			t.Fatal(err)
		}
		if got, err := Read(root, `{"path":"dir-link/secret.txt"}`); err == nil || got != "" {
			t.Fatalf("Read() = %q, %v; want denied symlink directory component", got, err)
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

func TestReadSupportsLinePaging(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Read(root, `{"path":"large.txt","offset":1,"limit":2}`)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got != "read_file: lines 2-3\ntwo\nthree\nread_file: more lines available; next offset: 3" {
		t.Fatalf("Read() = %q, want paged result", got)
	}

	got, err = Read(root, `{"path":"large.txt","offset":3,"limit":2}`)
	if err != nil {
		t.Fatalf("Read() final page error = %v", err)
	}
	if got != "read_file: lines 4-4\nfour" {
		t.Fatalf("Read() final page = %q, want four", got)
	}
}

func TestReadRejectsBinaryFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte("text\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root, `{"path":"image.bin"}`); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("Read() error = %v, want binary rejection", err)
	}
}

func TestReadLargeFileRequiresPaging(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), make([]byte, MaxReadBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root, `{"path":"large.txt"}`); err == nil || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("Read() error = %v, want paging guidance", err)
	}
}

func TestReadPagingRejectsInvalidRanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"path":"file.txt","offset":-1}`, `{"path":"file.txt","limit":0}`} {
		if _, err := Read(root, args); err == nil {
			t.Fatalf("Read(%s) error = nil, want invalid range error", args)
		}
	}
}

func TestReadExplicitDiscoveryDirectoryFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".git", "config.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("explicit content"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(root, `{"path":".git/config.txt"}`)
	if err != nil || got != "explicit content" {
		t.Fatalf("Read() = %q, %v; want explicit discovery file content", got, err)
	}
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
	if function.Parameters.Type != "object" || len(function.Parameters.Properties) != 3 || function.Parameters.Properties["path"] == nil || len(function.Parameters.Required) != 1 || function.Parameters.Required[0] != "path" || function.Parameters.AdditionalProperties {
		t.Fatalf("unexpected parameters schema: %s", strings.TrimSpace(string(definition.Function)))
	}
	for _, name := range []string{"offset", "limit"} {
		raw, ok := function.Parameters.Properties[name]
		if !ok {
			t.Fatalf("read_file schema missing %s", name)
		}
		var property struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &property); err != nil || property.Type != "integer" {
			t.Fatalf("read_file %s schema = %s, %v", name, raw, err)
		}
	}
}

func TestDefaultRegistryExposesReadOnlyTools(t *testing.T) {
	registry := NewDefaultRegistry()
	definitions := registry.Definitions()
	wantNames := []string{"list_files", "search_text", "read_file"}
	if len(definitions) != len(wantNames) {
		t.Fatalf("Definitions() length = %d, want %d", len(definitions), len(wantNames))
	}
	for index, wantName := range wantNames {
		var function struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(definitions[index].Function, &function); err != nil {
			t.Fatal(err)
		}
		if function.Name != wantName {
			t.Fatalf("default tool name at index %d = %q, want %q", index, function.Name, wantName)
		}
		if _, ok := registry.Lookup(wantName); !ok {
			t.Fatalf("Lookup(%q) = false, want true", wantName)
		}
	}
}

func TestNewRegistryRejectsDuplicateToolNames(t *testing.T) {
	first := testTool{name: "same"}
	second := testTool{name: "same"}
	if _, err := NewRegistry(first, second); err == nil || !strings.Contains(err.Error(), "duplicate tool") {
		t.Fatalf("NewRegistry() error = %v, want duplicate tool error", err)
	}
}

type testTool struct {
	name string
}

func (t testTool) Name() string {
	return t.name
}

func (t testTool) Definition() llm.ToolDefinition {
	raw := json.RawMessage(`{"name":"` + t.name + `"}`)
	return llm.ToolDefinition{Type: "function", Function: raw}
}

func (t testTool) Execute(context.Context, string, string) (string, error) {
	return "fake result", nil
}
