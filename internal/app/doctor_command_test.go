package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsConfigurationWithoutLeakingCredentials(t *testing.T) {
	root := t.TempDir()
	driftDir := filepath.Join(root, ".drift")
	if err := os.MkdirAll(driftDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driftDir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"deepseek\"\nprotocol = \"openai-compat\"\nbase_url = \"https://api.deepseek.com\"\nmodel = \"deepseek-flash\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := "sk-do-not-print"
	if err := os.WriteFile(filepath.Join(driftDir, "auth.json"), []byte(`{"version":1,"providers":{"deepseek":{"type":"api_key","key":"`+secret+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driftDir, "mcp.json"), []byte(`{"servers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}

	var out, stderr strings.Builder
	code := runDoctorCommand([]string{"-w", root}, os.Getenv, &out, &stderr)
	if code != 0 {
		t.Fatalf("doctor code=%d stderr=%q", code, stderr.String())
	}
	got := out.String()
	for _, want := range []string{"workspace:", "provider deepseek: configured", "mcp: 0 server", "boundary: workspace-only", "sandbox:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output=%q missing %q", got, want)
		}
	}
	if strings.Contains(got, secret) || strings.Contains(stderr.String(), secret) {
		t.Fatalf("doctor leaked credential: out=%q stderr=%q", got, stderr.String())
	}
}
