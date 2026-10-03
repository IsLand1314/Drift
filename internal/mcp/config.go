package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type Server struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	EnvRefs   []string `json:"env_refs,omitempty"`
}

type Config struct {
	Servers []Server `json:"servers"`
}

func (c Config) Server(name string) (Server, bool) {
	for _, server := range c.Servers {
		if server.Name == name {
			return server, true
		}
	}
	return Server{}, false
}

func Load(root string) (Config, error) {
	path := filepath.Join(root, ".drift", "mcp.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("mcp: read config: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("mcp: decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("mcp: config has multiple JSON values")
	}
	seen := make(map[string]struct{}, len(config.Servers))
	for index := range config.Servers {
		server := &config.Servers[index]
		server.Name = strings.TrimSpace(server.Name)
		server.Transport = strings.TrimSpace(server.Transport)
		server.Command = strings.TrimSpace(server.Command)
		if !identifier.MatchString(server.Name) || server.Transport != "stdio" || server.Command == "" {
			return Config{}, fmt.Errorf("mcp: server %d is invalid", index+1)
		}
		if _, exists := seen[server.Name]; exists {
			return Config{}, fmt.Errorf("mcp: duplicate server %q", server.Name)
		}
		seen[server.Name] = struct{}{}
		for refIndex, ref := range server.EnvRefs {
			if !environmentName.MatchString(ref) {
				return Config{}, fmt.Errorf("mcp: server %q env_refs[%d] is invalid", server.Name, refIndex)
			}
		}
	}
	return config, nil
}
