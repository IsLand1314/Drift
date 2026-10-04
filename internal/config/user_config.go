package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type UserConfig struct {
	Dir           string
	ConfigPresent bool
	Settings      Settings
	Providers     []Provider
	Auth          AuthFile
}

type Settings struct {
	Version         int          `toml:"version"`
	DefaultProvider string       `toml:"default_provider"`
	PermissionMode  string       `toml:"permission_mode"`
	SandboxMode     string       `toml:"sandbox_mode"`
	TUIMode         string       `toml:"tui_mode"`
	Chat            ChatSettings `toml:"chat"`
}

type ChatSettings struct {
	AutoCompact *bool `toml:"auto_compact"`
}

type Provider struct {
	Name            string `toml:"name"`
	Protocol        string `toml:"protocol"`
	BaseURL         string `toml:"base_url"`
	Model           string `toml:"model"`
	APIKeyEnv       string `toml:"api_key_env"`
	ContextWindow   int    `toml:"context_window"`
	MaxOutputTokens int    `toml:"max_output_tokens"`
}

type configFile struct {
	Version   int        `toml:"version"`
	Providers []Provider `toml:"providers"`
}

type AuthFile struct {
	Version   int                     `json:"version"`
	Providers map[string]AuthProvider `json:"providers"`
}

type AuthProvider struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

func LoadUserConfig(dir string) (UserConfig, error) {
	result := UserConfig{Dir: dir, Settings: Settings{Version: 1, PermissionMode: "default", SandboxMode: "auto", TUIMode: "main"}, Auth: AuthFile{Version: 1, Providers: map[string]AuthProvider{}}}
	configPath := filepath.Join(dir, "config.toml")
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read config.toml: %w", err)
	}
	result.ConfigPresent = true
	var cf configFile
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&cf); err != nil {
		return result, fmt.Errorf("parse config.toml: %w", err)
	}
	if err := validateConfigFile(cf); err != nil {
		return result, err
	}
	result.Providers = cf.Providers
	settingsPath := filepath.Join(dir, "settings.toml")
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&result.Settings); err != nil {
			return result, fmt.Errorf("parse settings.toml: %w", err)
		}
		if err := validateSettings(result.Settings, result.Providers); err != nil {
			return result, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("read settings.toml: %w", err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if data, err := os.ReadFile(authPath); err == nil {
		if err := validateAuthFileMode(authPath); err != nil {
			return result, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result.Auth); err != nil {
			return result, fmt.Errorf("parse auth.json: %w", err)
		}
		if err := validateAuth(result.Auth); err != nil {
			return result, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("read auth.json: %w", err)
	}
	return result, nil
}

func validateAuthFileMode(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat auth.json: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("auth.json: permissions are too broad")
	}
	return nil
}

func validateConfigFile(cf configFile) error {
	if cf.Version != 1 {
		return fmt.Errorf("config.toml: unsupported version")
	}
	seen := map[string]bool{}
	for _, p := range cf.Providers {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" || seen[p.Name] {
			return fmt.Errorf("config.toml: invalid or duplicate provider")
		}
		seen[p.Name] = true
		if p.Protocol != "openai" && p.Protocol != "openai-compat" && p.Protocol != "anthropic" {
			return fmt.Errorf("config.toml: unsupported provider protocol")
		}
		if p.Model == "" || p.BaseURL == "" {
			return fmt.Errorf("config.toml: provider requires model and base_url")
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
			return fmt.Errorf("config.toml: provider has unsafe base_url")
		}
		if p.ContextWindow < 0 || p.MaxOutputTokens < 0 {
			return fmt.Errorf("config.toml: provider limits must be non-negative")
		}
	}
	if len(cf.Providers) == 0 {
		return fmt.Errorf("config.toml: at least one provider is required")
	}
	return nil
}

func validateSettings(s Settings, providers []Provider) error {
	if s.Version != 1 {
		return fmt.Errorf("settings.toml: unsupported version")
	}
	if s.PermissionMode != "default" && s.PermissionMode != "acceptEdits" && s.PermissionMode != "plan" {
		return fmt.Errorf("settings.toml: invalid permission_mode")
	}
	if s.SandboxMode != "off" && s.SandboxMode != "auto" && s.SandboxMode != "required" {
		return fmt.Errorf("settings.toml: invalid sandbox_mode")
	}
	if s.TUIMode != "main" {
		return fmt.Errorf("settings.toml: invalid tui_mode")
	}
	if s.DefaultProvider != "" {
		for _, p := range providers {
			if p.Name == s.DefaultProvider {
				return nil
			}
		}
		return fmt.Errorf("settings.toml: default_provider not found")
	}
	return nil
}

func validateAuth(a AuthFile) error {
	if a.Version != 1 {
		return fmt.Errorf("auth.json: unsupported version")
	}
	for _, p := range a.Providers {
		if p.Type != "api_key" || strings.TrimSpace(p.Key) == "" {
			return fmt.Errorf("auth.json: invalid provider credential")
		}
	}
	return nil
}

func (c UserConfig) Provider(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

func (c UserConfig) APIKey(name string, getenv func(string) string) string {
	if p, ok := c.Auth.Providers[name]; ok && p.Type == "api_key" {
		return strings.TrimSpace(p.Key)
	}
	if p, ok := c.Provider(name); ok && p.APIKeyEnv != "" {
		return strings.TrimSpace(getenv(p.APIKeyEnv))
	}
	return ""
}
