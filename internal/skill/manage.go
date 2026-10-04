package skill

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

// Install copies only a validated SKILL.md from a local source directory.
// It never executes source content or copies arbitrary binaries/assets.
func Install(root, source, name string) error {
	if !ValidateName(name) {
		return fmt.Errorf("skill: invalid name")
	}
	sourcePath := filepath.Join(source, "SKILL.md")
	info, err := regularFile(sourcePath)
	if err != nil {
		return fmt.Errorf("skill: source unavailable")
	}
	if info.Size() > maxSkillBytes {
		return fmt.Errorf("skill: entry too large")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("skill: source unreadable")
	}
	if len(data) > maxSkillBytes {
		return fmt.Errorf("skill: entry too large")
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("skill: entry is not UTF-8")
	}
	manifest := Manifest{Name: name, Version: "1.0.0"}
	if raw, readErr := os.ReadFile(filepath.Join(source, "skill.json")); readErr == nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if decoder.Decode(&manifest) != nil || manifest.Name != name || !validVersion(manifest.Version) || len([]byte(manifest.Description)) > 512 {
			return fmt.Errorf("skill: invalid manifest")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("skill: manifest unreadable")
	}
	destination := filepath.Join(root, ".drift", "skills", name)
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("skill: destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("skill: destination unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("skill: create directory")
	}
	tmp, err := os.MkdirTemp(filepath.Dir(destination), ".install-")
	if err != nil {
		return fmt.Errorf("skill: create staging directory")
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "SKILL.md"), data, 0o600); err != nil {
		return fmt.Errorf("skill: stage entry")
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(tmp, "skill.json"), append(manifestData, '\n'), 0o600); err != nil {
		return fmt.Errorf("skill: stage manifest")
	}
	if err := os.Rename(tmp, destination); err != nil {
		return fmt.Errorf("skill: install entry")
	}
	_ = appendAudit(root, "install", manifest)
	return nil
}

func Remove(root, name string) error {
	if !ValidateName(name) {
		return fmt.Errorf("skill: invalid name")
	}
	destination := filepath.Join(root, ".drift", "skills", name)
	info, err := os.Lstat(destination)
	if err != nil {
		return fmt.Errorf("skill: destination unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("skill: destination is invalid")
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	_ = appendAudit(root, "remove", Manifest{Name: name, Version: "unknown"})
	return nil
}

func SetEnabled(root, name string, enabled bool) error {
	if !ValidateName(name) {
		return fmt.Errorf("skill: invalid name")
	}
	destination := filepath.Join(root, ".drift", "skills", name)
	if info, err := os.Stat(destination); err != nil || !info.IsDir() {
		return fmt.Errorf("skill: destination unavailable")
	}
	statePath := filepath.Join(root, ".drift", "skills", "state.json")
	state := map[string]bool{}
	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	state[name] = enabled
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(statePath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return appendAudit(root, map[bool]string{true: "enable", false: "disable"}[enabled], Manifest{Name: name, Version: "unknown"})
}

func IsEnabled(root, name string) (bool, error) {
	statePath := filepath.Join(root, ".drift", "skills", "state.json")
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	state := map[string]bool{}
	if err := json.Unmarshal(data, &state); err != nil {
		return false, err
	}
	enabled, ok := state[name]
	if !ok {
		return true, nil
	}
	return enabled, nil
}

func appendAudit(root, action string, manifest Manifest) error {
	dir := filepath.Join(root, ".drift", "audits")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, "skill-lifecycle.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(map[string]any{"action": action, "name": manifest.Name, "version": manifest.Version, "time": time.Now().UTC()})
}
