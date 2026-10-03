package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsDuplicateAndNonStdioServers(t *testing.T) {
	root := t.TempDir()
	writeMCPConfig(t, root, `{"servers":[{"name":"demo","transport":"stdio","command":"tool"},{"name":"demo","transport":"http","command":"tool"}]}`)
	if _, err := Load(root); err == nil {
		t.Fatal("Load() error = nil")
	}
}

func TestLoadRejectsLiteralEnvironmentValues(t *testing.T) {
	root := t.TempDir()
	writeMCPConfig(t, root, `{"servers":[{"name":"demo","transport":"stdio","command":"tool","env":{"TOKEN":"secret"}}]}`)
	if _, err := Load(root); err == nil {
		t.Fatal("Load() error = nil")
	}
}

func TestLoadAcceptsStdioServerWithEnvironmentReferences(t *testing.T) {
	root := t.TempDir()
	writeMCPConfig(t, root, `{"servers":[{"name":"demo","transport":"stdio","command":"tool","args":["--quiet"],"env_refs":["DEMO_TOKEN"]}]}`)
	config, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := config.Server("demo")
	if !ok || server.Command != "tool" || len(server.EnvRefs) != 1 || server.EnvRefs[0] != "DEMO_TOKEN" {
		t.Fatalf("server=%+v ok=%v", server, ok)
	}
}

func writeMCPConfig(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, ".drift", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
