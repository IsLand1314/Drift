package tool

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

var discoverySkipDirs = map[string]struct{}{
	".git":         {},
	".foxcode":     {},
	".codex":       {},
	".codex-temp":  {},
	".claude":      {},
	".drift":       {},
	".worktrees":   {},
	"node_modules": {},
	".venv":        {},
	"__pycache__":  {},
	".tox":         {},
	".mypy_cache":  {},
}

func validateRelativePath(path string, allowEmpty bool) error {
	// 先统一分隔符，再同时检查 Unix 与 Windows 形式，避免 -w 在 Windows
	// 运行时被模型传入的另一种路径格式绕过。
	path = strings.ReplaceAll(path, `\`, "/")
	if path == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("path must not be empty")
	}
	if pathpkg.IsAbs(path) || filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must be relative")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return fmt.Errorf("path must not contain ..")
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if IsDotEnvCredentialFile(segment) {
			return fmt.Errorf("path is restricted")
		}
	}
	return nil
}

func openWorkspace(root string) (*os.Root, error) {
	return os.OpenRoot(root)
}

func walkRegularFiles(workspace *os.Root, relative string, fn func(path string, entry fs.DirEntry) error) error {
	return walkRegularFilesContext(context.Background(), workspace, relative, fn)
}

func walkRegularFilesContext(ctx context.Context, workspace *os.Root, relative string, fn func(path string, entry fs.DirEntry) error) error {
	if err := validateRelativePath(relative, true); err != nil {
		return err
	}
	start := strings.ReplaceAll(relative, `\`, "/")
	if start == "" {
		start = "."
	}
	start = pathpkg.Clean(start)
	hasSymlink, err := hasSymlinkComponent(workspace, start)
	if err != nil {
		return err
	}
	if hasSymlink {
		return fmt.Errorf("walk root contains a symlink")
	}
	info, err := workspace.Lstat(start)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("walk root is not a directory")
	}
	return fs.WalkDir(workspace.FS(), start, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir
		}
		if entry.IsDir() {
			// discovery 只看源码候选，跳过依赖、Git 元数据和其他运行时目录。
			if _, skip := discoverySkipDirs[strings.ToLower(entry.Name())]; skip {
				return fs.SkipDir
			}
		}
		if IsDotEnvCredentialFile(pathpkg.Base(path)) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if path == "." {
			return nil
		}
		return fn(strings.TrimPrefix(path, "./"), entry)
	})
}
