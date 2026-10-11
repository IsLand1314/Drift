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
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"deepseek\"\nprotocol = \"openai-compat\"\nbase_url = \"https://api.deepseek.com\"\nmodel = \"deepseek-chat\"\napi_key = \"DEEPSEEK_API_KEY\"\n"), 0600); err != nil {
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

func TestLoadUserConfigReadsSandboxAutoAllow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("version = 1\nsandbox_mode = \"auto\"\nsandbox_auto_allow = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"test\"\nprotocol = \"openai-compat\"\nbase_url = \"https://example.invalid\"\nmodel = \"test\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Settings.SandboxAutoAllow {
		t.Fatalf("settings=%+v", cfg.Settings)
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
	cfg := UserConfig{Auth: AuthFile{Providers: map[string]AuthProvider{"p": {Type: "api_key", Key: "from-auth"}}}, Providers: []Provider{{Name: "p", APIKey: "P_KEY"}}}
	if got := cfg.APIKey("p", func(string) string { return "from-env" }); got != "from-auth" {
		t.Fatalf("APIKey() = %q", got)
	}
	cfg.Auth.Providers = map[string]AuthProvider{}
	if got := cfg.APIKey("p", func(string) string { return "from-env" }); got != "from-env" {
		t.Fatalf("APIKey() = %q", got)
	}
}

func TestSaveAPIKeyReplacesOnlyRequestedProviderCredential(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"version":1,"providers":{"other":{"type":"api_key","key":"keep"},"target":{"type":"api_key","key":"old"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveAPIKey(dir, "target", "new-key"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth AuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatal(err)
	}
	if got := auth.Providers["target"].Key; got != "new-key" {
		t.Fatalf("target key = %q", got)
	}
	if got := auth.Providers["other"].Key; got != "keep" {
		t.Fatalf("other key = %q", got)
	}
}

func TestSaveAPIKeyRejectsBlankValues(t *testing.T) {
	if err := SaveAPIKey(t.TempDir(), "target", " \t "); err == nil {
		t.Fatal("SaveAPIKey accepted a blank key")
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

func TestLoadUserConfigAcceptsToolLoadingStrategy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"p\"\nprotocol = \"openai\"\nbase_url = \"https://api.openai.com/v1\"\nmodel = \"m\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("version = 1\ntool_loading = \"eager\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfig(dir)
	if err != nil || cfg.Settings.ToolLoading != "eager" {
		t.Fatalf("settings=%+v err=%v", cfg.Settings, err)
	}
}

func TestLoadUserConfigAcceptsCommandHook(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"p\"\nprotocol = \"openai\"\nbase_url = \"https://api.openai.com/v1\"\nmodel = \"m\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := "version = 1\n[[hooks]]\nid = \"format\"\nevent = \"post_tool_use\"\ntool = \"EditFile\"\nmatch = \"*.go\"\ncommand = \"gofmt -w\"\ntimeout_ms = 30000\non_error = \"report\"\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfig(dir)
	if err != nil || len(cfg.Settings.Hooks) != 1 || cfg.Settings.Hooks[0].ID != "format" {
		t.Fatalf("settings=%+v err=%v", cfg.Settings, err)
	}
}

func TestLoadUserConfigAcceptsAutomaticMemorySetting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"p\"\nprotocol = \"openai\"\nbase_url = \"https://api.openai.com/v1\"\nmodel = \"m\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("version = 1\n[memory]\nauto_extract = true\nauto_retrieve = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfig(dir)
	if err != nil || !cfg.Settings.Memory.AutoExtract || !cfg.Settings.Memory.AutoRetrieve {
		t.Fatalf("settings=%+v err=%v", cfg.Settings, err)
	}
}

func TestLoadUserConfigAcceptsLocalObservabilitySettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"p\"\nprotocol = \"openai\"\nbase_url = \"https://api.openai.com/v1\"\nmodel = \"m\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := "version = 1\n[observability]\nenabled = true\npushgateway_url = \"http://127.0.0.1:9091\"\ninstance = \"local\"\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Settings.Observability.Enabled || cfg.Settings.Observability.PushgatewayURL != "http://127.0.0.1:9091" {
		t.Fatalf("observability = %#v", cfg.Settings.Observability)
	}
}

func TestLoadUserConfigRejectsNonLocalObservabilityEndpoint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("version = 1\n[[providers]]\nname = \"p\"\nprotocol = \"openai\"\nbase_url = \"https://api.openai.com/v1\"\nmodel = \"m\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := "version = 1\n[observability]\nenabled = true\npushgateway_url = \"https://metrics.example.com\"\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUserConfig(dir); err == nil || !strings.Contains(err.Error(), "observability") {
		t.Fatalf("err = %v, want local endpoint validation", err)
	}
}
