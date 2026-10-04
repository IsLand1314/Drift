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

// Server describes an external MCP server that Drift connects to as a client.
// Drift does not expose an MCP server endpoint or accept inbound MCP sessions.
type Server struct {
	Name           string            `json:"name"`
	Transport      string            `json:"transport"`
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	EnvRefs        []string          `json:"env_refs,omitempty"`
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	NetworkEnabled bool              `json:"network_enabled,omitempty"`
	RetryCount     int               `json:"retry_count,omitempty"`
	TimeoutMS      int               `json:"timeout_ms,omitempty"`
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
		if !identifier.MatchString(server.Name) || (server.Transport != "stdio" && server.Transport != "http" && server.Transport != "streamable-http") || (server.Transport == "stdio" && server.Command == "") || (server.Transport != "stdio" && server.URL == "") {
			return Config{}, fmt.Errorf("mcp: server %d is invalid", index+1)
		}
		if _, exists := seen[server.Name]; exists {
			return Config{}, fmt.Errorf("mcp: duplicate server %q", server.Name)
		}
		seen[server.Name] = struct{}{}
		if server.RetryCount < 0 || server.RetryCount > 3 {
			return Config{}, fmt.Errorf("mcp: server %q retry_count must be between 0 and 3", server.Name)
		}
		if server.TimeoutMS != 0 && (server.TimeoutMS < 100 || server.TimeoutMS > 120000) {
			return Config{}, fmt.Errorf("mcp: server %q timeout_ms must be between 100 and 120000", server.Name)
		}
		for refIndex, ref := range server.EnvRefs {
			if !environmentName.MatchString(ref) {
				return Config{}, fmt.Errorf("mcp: server %q env_refs[%d] is invalid", server.Name, refIndex)
			}
		}
	}
	return config, nil
}
