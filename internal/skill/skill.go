// Package skill loads explicit, workspace-local read-only Skill instructions.
package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxSkillBytes = 64 << 10

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Info describes a discoverable Skill without loading its contents.
type Info struct {
	Name        string
	Path        string
	Size        int64
	Version     string
	Description string
	Enabled     bool
}

// Skill is one validated workspace Skill.
type Skill struct {
	Info
	Content string
}

type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// ValidateName reports whether name is a safe Skill directory name.
func ValidateName(name string) bool { return skillNamePattern.MatchString(name) }

// List discovers valid immediate Skill directories under the workspace.
func List(root string) ([]Info, error) {
	dir := filepath.Join(root, ".drift", "skills")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Info{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skill: list unavailable")
	}
	items := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !ValidateName(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "SKILL.md")
		info, statErr := regularFile(path)
		if statErr != nil || info.Size() > maxSkillBytes {
			continue
		}
		manifest, manifestErr := readManifest(filepath.Join(dir, entry.Name()), entry.Name())
		if manifestErr != nil {
			continue
		}
		enabled, _ := IsEnabled(root, entry.Name())
		items = append(items, Info{Name: entry.Name(), Path: path, Size: info.Size(), Version: manifest.Version, Description: manifest.Description, Enabled: enabled})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

// Load validates and loads one Skill from the workspace.
func Load(root, name string) (Skill, error) {
	if !ValidateName(name) {
		return Skill{}, fmt.Errorf("skill: invalid name")
	}
	if enabled, err := IsEnabled(root, name); err != nil || !enabled {
		return Skill{}, fmt.Errorf("skill: disabled")
	}
	path := filepath.Join(root, ".drift", "skills", name, "SKILL.md")
	info, err := regularFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("skill: entry unavailable")
	}
	if info.Size() > maxSkillBytes {
		return Skill{}, fmt.Errorf("skill: entry too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("skill: entry unreadable")
	}
	if len(data) > maxSkillBytes {
		return Skill{}, fmt.Errorf("skill: entry too large")
	}
	if !utf8.Valid(data) {
		return Skill{}, fmt.Errorf("skill: entry is not UTF-8")
	}
	manifest, err := readManifest(filepath.Dir(path), name)
	if err != nil {
		return Skill{}, err
	}
	return Skill{Info: Info{Name: name, Path: path, Size: int64(len(data)), Version: manifest.Version, Description: manifest.Description}, Content: string(data)}, nil
}

func readManifest(dir, name string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "skill.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{Name: name, Version: "0.0.0"}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("skill: manifest unreadable")
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if decoder.Decode(&manifest) != nil || manifest.Name != name || !validVersion(manifest.Version) || len([]byte(manifest.Description)) > 512 {
		return Manifest{}, fmt.Errorf("skill: invalid manifest")
	}
	return manifest, nil
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func validVersion(version string) bool { return versionPattern.MatchString(version) }

func regularFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return info, nil
}
