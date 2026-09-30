package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/IsLand1314/Drift/internal/tool"
)

var errInvalidWorkspaceTarget = errors.New("workspace target is invalid")

type workspaceSelection struct {
	Root  string
	Focus string
}

// resolveWorkspace turns the user-facing -w value into a canonical workspace
// root and an optional file focus. The returned paths never expose a symlink
// alias to the rest of the runtime.
func resolveWorkspace(launchDir, rawTarget string) (workspaceSelection, error) {
	target := launchDir
	if rawTarget != "" {
		target = rawTarget
		if !filepath.IsAbs(target) {
			target = filepath.Join(launchDir, target)
		}
	}

	absolute, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return workspaceSelection{}, errInvalidWorkspaceTarget
	}
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return workspaceSelection{}, errInvalidWorkspaceTarget
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return workspaceSelection{}, errInvalidWorkspaceTarget
	}
	info, err = os.Stat(resolved)
	if err != nil {
		return workspaceSelection{}, errInvalidWorkspaceTarget
	}

	switch {
	case info.IsDir():
		return workspaceSelection{Root: resolved}, nil
	case info.Mode().IsRegular():
		focus := filepath.ToSlash(filepath.Base(resolved))
		if tool.IsDotEnvCredentialFile(focus) {
			return workspaceSelection{}, errInvalidWorkspaceTarget
		}
		return workspaceSelection{Root: filepath.Dir(resolved), Focus: focus}, nil
	default:
		return workspaceSelection{}, errInvalidWorkspaceTarget
	}
}
