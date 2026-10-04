package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUserConfigReadsTOMLAndAuth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("version = 1\ndefault_provider = \"deepseek\"\npermission_mode = \"default\"\nsandbox_mode = \"auto\"\ntui_mode = \"main\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"deepseek\"\nprotocol = \"openai-compat\"\nbase_url = \"https://api.deepseek.com\"\nmodel = \"deepseek-chat\"\napi_key_env = \"DEEPSEEK_API_KEY\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"version":1,"providers":{"deepseek":{"type":"api_key","key":"secret"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ConfigPresent || cfg.Settings.DefaultProvider != "deepseek" || len(cfg.Providers) != 1 || cfg.Auth.Providers["deepseek"].Key != "secret" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadUserConfigRejectsInvalidProviderAndDoesNotEchoSecret(t *testing.T) {
	dir := t.TempDir()
	secret := "super-secret-value"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"bad\"\nprotocol = \"unknown\"\nbase_url = \"http://example.com\"\nmodel = \"x\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"version":1,"providers":{"bad":{"type":"api_key","key":"`+secret+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadUserConfig(dir)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error=%v, secret leaked", err)
	}
}

func TestLoadUserConfigRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\nunknown = true\n[[providers]]\nname = \"x\"\nprotocol = \"openai\"\nbase_url = \"https://api.openai.com/v1\"\nmodel = \"m\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUserConfig(dir); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestAuthJSONUsesStandardJSON(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"version":1}`), &raw); err != nil {
		t.Fatal(err)
	}
}

func TestUserConfigAPIKeyPrefersAuthThenEnvironment(t *testing.T) {
	cfg := UserConfig{Auth: AuthFile{Providers: map[string]AuthProvider{"p": {Type: "api_key", Key: "from-auth"}}}, Providers: []Provider{{Name: "p", APIKeyEnv: "P_KEY"}}}
	if got := cfg.APIKey("p", func(string) string { return "from-env" }); got != "from-auth" {
		t.Fatalf("APIKey() = %q", got)
	}
	cfg.Auth.Providers = map[string]AuthProvider{}
	if got := cfg.APIKey("p", func(string) string { return "from-env" }); got != "from-env" {
		t.Fatalf("APIKey() = %q", got)
	}
}

func TestLoadUserConfigAcceptsCodexProviderWithoutAPIKey(t *testing.T) {
	dir := t.TempDir()
	data := []byte("version = 1\n[[providers]]\nname = \"codex\"\nprotocol = \"codex\"\ncodex_home = \"C:/Users/test/.codex\"\nmodel = \"gpt-5.6-luna\"\n")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers[0].Protocol != "codex" || cfg.Providers[0].CodexHome == "" {
		t.Fatalf("provider=%+v", cfg.Providers[0])
	}
}

func TestLoadDotEnvParsesCommentsBlanksAndQuotedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	contents := "\n# comment\n KEY = value \nSINGLE='quoted value'\nDOUBLE=\"another value\"\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv() error = %v", err)
	}
	want := map[string]string{"KEY": "value", "SINGLE": "quoted value", "DOUBLE": "another value"}
	if len(got) != len(want) {
		t.Fatalf("LoadDotEnv() = %#v, want %#v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("LoadDotEnv()[%q] = %q, want %q", key, got[key], value)
		}
	}
}

func TestLoadDotEnvRejectsMalformedLineWithLineNumberWithoutValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	const secret = "SECRET_VALUE"
	if err := os.WriteFile(path, []byte("GOOD=ok\n"+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDotEnv(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), secret) {
		t.Fatalf("LoadDotEnv() error = %v, want line number and no value", err)
	}
}

func TestLoadDotEnvMissingFileReturnsEmptyMap(t *testing.T) {
	got, err := LoadDotEnv(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("LoadDotEnv() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("LoadDotEnv() = %#v, want empty map", got)
	}
}

func TestMergeLookupProcessValueWinsAndEmptyFallsBack(t *testing.T) {
	dotenv := map[string]string{"KEY": "from dotenv"}
	if got := MergeLookup(dotenv, func(string) string { return "from process" }, "KEY"); got != "from process" {
		t.Fatalf("MergeLookup() = %q, want process value", got)
	}
	if got := MergeLookup(dotenv, func(string) string { return "" }, "KEY"); got != "from dotenv" {
		t.Fatalf("MergeLookup() = %q, want dotenv value", got)
	}
}
