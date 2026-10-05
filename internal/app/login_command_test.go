package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IsLand1314/Drift/internal/config"
)

func TestRunLoginSavesAPIKeyWithoutEchoingIt(t *testing.T) {
	workspace := t.TempDir()
	writeLoginConfig(t, workspace, "api", "openai-compat")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	secret := "private-key"
	var out, stderr strings.Builder
	if code := RunWithInput(context.Background(), []string{"login", "--provider", "api"}, func(string) string { return "" }, strings.NewReader(secret+"\n"), &out, &stderr); code != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
	cfg, err := config.LoadUserConfig(filepath.Join(workspace, ".drift"))
	if err != nil || cfg.Auth.Providers["api"].Key != secret {
		t.Fatalf("config=%+v err=%v", cfg.Auth, err)
	}
	if strings.Contains(out.String()+stderr.String(), secret) {
		t.Fatalf("login output leaked key: out=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestRunLoginRejectsUnknownAndCodexProviders(t *testing.T) {
	workspace := t.TempDir()
	writeLoginConfig(t, workspace, "codex", "codex")
	old, _ := os.Getwd()
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	for _, provider := range []string{"missing", "codex"} {
		var out, stderr strings.Builder
		if code := RunWithInput(context.Background(), []string{"login", "--provider", provider}, func(string) string { return "" }, strings.NewReader("secret\n"), &out, &stderr); code != 2 || strings.Contains(out.String()+stderr.String(), "secret") {
			t.Fatalf("provider=%q code=%d out=%q stderr=%q", provider, code, out.String(), stderr.String())
		}
	}
}

func writeLoginConfig(t *testing.T, workspace, name, protocol string) {
	t.Helper()
	dir := filepath.Join(workspace, ".drift")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	text := "version = 1\n[[providers]]\nname = \"" + name + "\"\nprotocol = \"" + protocol + "\"\nmodel = \"test\"\n"
	if protocol != "codex" {
		text += "base_url = \"https://example.test/v1\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
