package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.Rename(tmp, destination); err != nil {
		return fmt.Errorf("skill: install entry")
	}
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
	return os.RemoveAll(destination)
}
