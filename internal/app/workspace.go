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

// resolveWorkspace 把用户传入的 -w 转为 workspace 根目录和可选的文件 focus。
// 整个 Runtime 后续只拿 Root 做路径边界；拒绝符号链接可避免别名绕过该边界。
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
		// -w 目录：模型可以在该目录下自行探索，不预设关注文件。
		return workspaceSelection{Root: resolved}, nil
	case info.Mode().IsRegular():
		// -w 文件：父目录才是工具可访问的 workspace；文件名只作为首轮提示。
		focus := filepath.ToSlash(filepath.Base(resolved))
		if tool.IsDotEnvCredentialFile(focus) {
			return workspaceSelection{}, errInvalidWorkspaceTarget
		}
		return workspaceSelection{Root: filepath.Dir(resolved), Focus: focus}, nil
	default:
		return workspaceSelection{}, errInvalidWorkspaceTarget
	}
}
