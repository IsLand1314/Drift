package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"z.txt": "no\nneedle here\n", "a.txt": "needle first\nneedle second\n", "nested/b.txt": "needle nested\n", ".env": "needle secret\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Search(root, `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "a.txt:1: needle first\na.txt:2: needle second\nnested/b.txt:1: needle nested\nz.txt:2: needle here"
	if got != want {
		t.Fatalf("Search() = %q, want %q", got, want)
	}
	got, err = Search(root, `{"query":"needle","path":"nested"}`)
	if err != nil || got != "nested/b.txt:1: needle nested" {
		t.Fatalf("relative Search() = %q, %v", got, err)
	}
	for _, raw := range []string{`{"query":""}`, `{"path":"x"}`, `{"query":null}`, `{"query":1}`, `{"query":"x","path":null}`, `{"query":"x","path":1}`, `{"query":"x","extra":true}`, `null`, `[]`, `{"query":"x"} {}`} {
		if _, err := Search(root, raw); err == nil {
			t.Errorf("Search(%s) error = nil", raw)
		}
	}
	for _, path := range []string{"..", "../x", filepath.ToSlash(root), ".env", ".env.local", "nested/.env.private"} {
		if _, err := Search(root, `{"query":"x","path":"`+path+`"}`); err == nil {
			t.Errorf("Search path %q error = nil", path)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "binary"), []byte("needle\x00needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Search(root, `{"query":"needle"}`)
	if err != nil || strings.Contains(got, "binary") {
		t.Fatalf("binary result = %q, %v", got, err)
	}
	t.Run("long line bounded", func(t *testing.T) {
		line := strings.Repeat("x", 250) + "needle"
		if err := os.Mkdir(filepath.Join(root, "longdir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "longdir", "long.txt"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := Search(root, `{"query":"needle","path":"longdir"}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len("longdir/long.txt:1: ")+240 || !strings.HasPrefix(got, "longdir/long.txt:1: ") {
			t.Fatalf("long result length = %d, %q", len(got), got)
		}
		tooLong := filepath.Join(root, "longdir", "too-long.txt")
		if err := os.WriteFile(tooLong, []byte(strings.Repeat("x", 65<<10)+"needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Search(root, `{"query":"needle","path":"longdir"}`); err == nil {
			t.Fatal("Search() error = nil for scanner token-too-long")
		}
		if err := os.Remove(tooLong); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("regular file root rejected", func(t *testing.T) {
		if _, err := Search(root, `{"query":"x","path":"z.txt"}`); err == nil {
			t.Fatal("regular root accepted")
		}
	})
	t.Run("symlink skipped", func(t *testing.T) {
		if err := os.Symlink("z.txt", filepath.Join(root, "link.txt")); err != nil {
			if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrInvalid) || os.IsPermission(err) || errors.Is(err, syscall.Errno(1314)) {
				t.Skip(err)
			}
			t.Fatal(err)
		}
		got, err := Search(root, `{"query":"needle"}`)
		if err != nil || strings.Contains(got, "link.txt") {
			t.Fatalf("symlink result = %q, %v", got, err)
		}
	})
	t.Run("tool delegates", func(t *testing.T) {
		var tool Tool = searchTextTool{}
		if tool.Name() != "search_text" {
			t.Fatal(tool.Name())
		}
		if _, err := tool.Execute(context.Background(), root, `{"query":"needle"}`); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancellation is returned", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := (searchTextTool{}).Execute(ctx, root, `{"query":"needle"}`); !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute() error = %v", err)
		}
	})
}

func TestSearchLimitsAndDefinition(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxSearchFiles+1; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d.txt", i)), []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Search(root, `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("limit result lacks marker: %q", got[len(got)-80:])
	}
	t.Run("file limit counts files without matches", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < MaxSearchFiles+1; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d", i)), []byte("other\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := Search(dir, `{"query":"needle"}`)
		if err != nil || !strings.Contains(got, "truncated") {
			t.Fatalf("result = %q, %v", got, err)
		}
	})
	t.Run("match limit", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < MaxSearchMatches+1; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d", i)), []byte("needle\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := Search(dir, `{"query":"needle"}`)
		if err != nil || !strings.Contains(got, "truncated") {
			t.Fatalf("result = %q, %v", got, err)
		}
	})
	t.Run("output limit preserves results", func(t *testing.T) {
		dir := t.TempDir()
		name := strings.Repeat("x", 200)
		for i := 0; i < MaxSearchMatches; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s%03d", name, i)), []byte(strings.Repeat("needle", 50)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := Search(dir, `{"query":"needle"}`)
		if err != nil || !strings.Contains(got, "truncated") || len(got) > MaxSearchBytes {
			t.Fatalf("len/result = %d/%q, %v", len(got), got, err)
		}
	})
	definition := SearchDefinition()
	var fn struct {
		Name       string `json:"name"`
		Parameters struct {
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
			AdditionalProperties bool                       `json:"additionalProperties"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(definition.Function, &fn); err != nil {
		t.Fatal(err)
	}
	if definition.Type != "function" || fn.Name != "search_text" || len(fn.Parameters.Required) != 1 || fn.Parameters.Required[0] != "query" || fn.Parameters.Properties["path"] == nil || fn.Parameters.Properties["query"] == nil || fn.Parameters.AdditionalProperties {
		t.Fatalf("schema = %s", definition.Function)
	}
}
