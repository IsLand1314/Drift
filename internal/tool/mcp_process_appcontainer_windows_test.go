//go:build windows

package tool

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPProcessUsesAppContainerLifecycle(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".drift", ".git"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	process := newAppContainerMCPProcess(context.Background(), root, os.Getenv("ComSpec"), []string{"/d", "/s", "/c", "echo mcp-appcontainer"}, nil, false)
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "access is denied") || strings.Contains(strings.ToLower(err.Error()), "not supported") {
			t.Skipf("AppContainer unavailable: %v", err)
		}
		t.Fatal(err)
	}
	defer process.Close()
	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "mcp-appcontainer") {
		t.Fatalf("stdout=%q", data)
	}
}

func TestMCPProcessCannotWriteProtectedDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".drift", ".git"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	process := newAppContainerMCPProcess(context.Background(), root, os.Getenv("ComSpec"), []string{"/d", "/s", "/c", "echo blocked > .drift\\mcp-denied"}, nil, false)
	if _, err := process.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "access is denied") || strings.Contains(strings.ToLower(err.Error()), "not supported") {
			t.Skipf("AppContainer unavailable: %v", err)
		}
		t.Fatal(err)
	}
	_ = process.Wait()
	defer process.Close()
	if _, err := os.Stat(filepath.Join(root, ".drift", "mcp-denied")); err == nil {
		t.Fatal("MCP process wrote protected .drift directory")
	}
}
