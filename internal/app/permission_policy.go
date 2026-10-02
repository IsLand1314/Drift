package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

const permissionPolicyVersion = 1

type permissionPolicy struct {
	root  string
	mu    sync.Mutex
	rules []permissionRule
}

type permissionPolicyDocument struct {
	Version int              `json:"version"`
	Rules   []permissionRule `json:"rules"`
}

type permissionRule struct {
	Tool      string `json:"tool"`
	Operation string `json:"operation"`
	Path      string `json:"path,omitempty"`
	Command   string `json:"command,omitempty"`
	CWD       string `json:"cwd,omitempty"`
	CreatedAt string `json:"created_at"`
}

func newPermissionPolicy(root string) *permissionPolicy {
	return &permissionPolicy{root: root}
}

func loadPermissionPolicy(root string) (*permissionPolicy, error) {
	policy := newPermissionPolicy(root)
	raw, err := os.ReadFile(permissionPolicyPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return policy, nil
	}
	if err != nil {
		return nil, err
	}
	var document permissionPolicyDocument
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if document.Version != permissionPolicyVersion {
		return nil, fmt.Errorf("unsupported permission policy version %d", document.Version)
	}
	seen := make(map[string]struct{}, len(document.Rules))
	for _, rule := range document.Rules {
		if rule.Tool == "" || rule.Operation == "" || rule.CreatedAt == "" {
			return nil, errors.New("invalid permission policy rule")
		}
		key := permissionRuleKey(rule)
		if _, ok := seen[key]; ok {
			return nil, errors.New("duplicate permission policy rule")
		}
		seen[key] = struct{}{}
		policy.rules = append(policy.rules, rule)
	}
	return policy, nil
}

func permissionPolicyPath(root string) string {
	return filepath.Join(root, ".drift", "permissions.json")
}

func permissionRuleFromRequest(request agent.PermissionRequest) permissionRule {
	return permissionRule{
		Tool:      strings.TrimSpace(request.ToolName),
		Operation: strings.TrimSpace(request.Operation),
		Path:      filepath.ToSlash(strings.TrimSpace(request.Path)),
		Command:   strings.TrimSpace(request.Command),
		CWD:       filepath.ToSlash(strings.TrimSpace(request.CWD)),
	}
}

func permissionRuleKey(rule permissionRule) string {
	return rule.Tool + "\x00" + rule.Operation + "\x00" + rule.Path + "\x00" + rule.CWD + "\x00" + rule.Command
}

func (p *permissionPolicy) allows(request agent.PermissionRequest) bool {
	if p == nil {
		return false
	}
	key := permissionRuleKey(permissionRuleFromRequest(request))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, rule := range p.rules {
		if permissionRuleKey(rule) == key {
			return true
		}
	}
	return false
}

func (p *permissionPolicy) remember(request agent.PermissionRequest) error {
	if p == nil {
		return errors.New("permission policy is nil")
	}
	rule := permissionRuleFromRequest(request)
	if rule.Tool == "" || rule.Operation == "" {
		return errors.New("permission request is incomplete")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, existing := range p.rules {
		if permissionRuleKey(existing) == permissionRuleKey(rule) {
			return nil
		}
	}
	rule.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	p.rules = append(p.rules, rule)
	return p.saveLocked()
}

func (p *permissionPolicy) clear() error {
	if p == nil {
		return errors.New("permission policy is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = nil
	return p.saveLocked()
}

func (p *permissionPolicy) saveLocked() error {
	path := permissionPolicyPath(p.root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(permissionPolicyDocument{Version: permissionPolicyVersion, Rules: p.rules}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".permissions-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if retryErr := os.Rename(tmpName, path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (p *permissionPolicy) summary() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]string, 0, len(p.rules))
	for _, rule := range p.rules {
		target := rule.Path
		if rule.Command != "" {
			target = rule.Command + " (cwd " + rule.CWD + ")"
		}
		result = append(result, rule.Tool+"/"+rule.Operation+": "+target)
	}
	return result
}
