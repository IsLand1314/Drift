package app

import (
	"path/filepath"
	"strings"

	"github.com/IsLand1314/Drift/internal/agent"
)

type permissionMemory struct {
	allowed map[string]struct{}
}

func newPermissionMemory() *permissionMemory {
	return &permissionMemory{allowed: make(map[string]struct{})}
}

func (m *permissionMemory) key(request agent.PermissionRequest) string {
	path := filepath.ToSlash(strings.TrimSpace(request.Path))
	command := strings.TrimSpace(request.Command)
	cwd := filepath.ToSlash(strings.TrimSpace(request.CWD))
	return request.ToolName + "\x00" + request.Operation + "\x00" + path + "\x00" + cwd + "\x00" + command
}

func (m *permissionMemory) Allow(request agent.PermissionRequest) bool {
	if m == nil {
		return false
	}
	_, ok := m.allowed[m.key(request)]
	return ok
}

func (m *permissionMemory) Remember(request agent.PermissionRequest) {
	if m == nil {
		return
	}
	if m.allowed == nil {
		m.allowed = make(map[string]struct{})
	}
	m.allowed[m.key(request)] = struct{}{}
}
