package commandpolicy

import (
	"regexp"
	"strings"
)

type dangerousRule struct {
	id     string
	reason string
	match  func(string, Shell) bool
}

var dangerousRules = []dangerousRule{
	{id: "fork_bomb", reason: "fork bomb", match: matchForkBomb},
	{id: "shell_pipe_to_interpreter", reason: "remote content is piped to a shell", match: matchPipeToInterpreter},
	{id: "destructive_rm_recursive", reason: "recursive file removal", match: matchRecursiveRemoval},
	{id: "destructive_directory_removal", reason: "recursive directory removal", match: matchCMDDirectoryRemoval},
	{id: "destructive_file_removal", reason: "recursive file removal", match: matchCMDFileRemoval},
	{id: "filesystem_format", reason: "filesystem formatting", match: matchFilesystemFormat},
	{id: "device_overwrite", reason: "direct device overwrite", match: matchDeviceOverwrite},
	{id: "recursive_permission_system", reason: "recursive broad permission change", match: matchRecursivePermissionChange},
	{id: "git_reset_hard", reason: "destructive Git reset", match: matchGitResetHard},
	{id: "git_clean_force", reason: "forced Git clean", match: matchGitCleanForce},
	{id: "git_checkout_all", reason: "discarding all Git working tree changes", match: matchGitCheckoutAll},
	{id: "git_branch_delete", reason: "forced Git branch deletion", match: matchGitBranchDelete},
	{id: "git_push_force", reason: "forced Git push", match: matchGitPushForce},
}

var (
	forkBombPattern          = regexp.MustCompile(`:\(\)\{[^}]*:[^}]*\|[^}]*&[^}]*\}`)
	pipeToInterpreterPattern = regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^\r\n;]*\|\s*(?:sh|bash|zsh|dash|cmd(?:\.exe)?|powershell(?:\.exe)?)\b`)
	recursiveRemovalPattern  = regexp.MustCompile(`(?i)\brm\s+(?:--recursive\b|-[^\s]*r[^\s]*\b)`)
	cmdDirectoryPattern      = regexp.MustCompile(`(?i)\b(?:rmdir|rd)\b[^\r\n]*\/(?:s|q)\b`)
	cmdFilePattern           = regexp.MustCompile(`(?i)\bdel\b[^\r\n]*(?:\/s|\/q)\b`)
	formatPattern            = regexp.MustCompile(`(?i)\bmkfs(?:\.[a-z0-9._-]+)?\b|\bformat\s+(?:[a-z]:|\\\\)`)
	deviceOverwritePattern   = regexp.MustCompile(`(?i)\bdd\b[^\r\n]*\bof\s*=\s*\/dev\/`)
	permissionPattern        = regexp.MustCompile(`(?i)\bchmod\b[^\r\n]*(?:-R|--recursive)[^\r\n]*\b777\b`)
	gitResetPattern          = regexp.MustCompile(`(?i)\bgit\s+reset\b[^\r\n]*--hard\b`)
	gitCleanPattern          = regexp.MustCompile(`(?i)\bgit\s+clean\b[^\r\n]*-[^\r\n]*f`)
	gitCheckoutPattern       = regexp.MustCompile(`(?i)\bgit\s+(?:checkout|restore)\b[^\r\n]*(?:\.|--\s+\.)\s*$`)
	gitBranchPattern         = regexp.MustCompile(`(?i)\bgit\s+branch\b[^\r\n]*\s-D\b`)
	gitPushPattern           = regexp.MustCompile(`(?i)\bgit\s+push\b[^\r\n]*(?:--force-with-lease|--force|\s-f(?:\s|$))`)
)

func matchForkBomb(command string, _ Shell) bool {
	normalized := strings.Join(strings.Fields(command), "")
	return forkBombPattern.MatchString(command) || strings.Contains(normalized, ":(){:|:&};:")
}

func matchPipeToInterpreter(command string, _ Shell) bool {
	return pipeToInterpreterPattern.MatchString(command)
}
func matchRecursiveRemoval(command string, _ Shell) bool {
	return recursiveRemovalPattern.MatchString(command)
}
func matchCMDDirectoryRemoval(command string, shell Shell) bool {
	return shell == ShellCMD && cmdDirectoryPattern.MatchString(command)
}
func matchCMDFileRemoval(command string, shell Shell) bool {
	return shell == ShellCMD && cmdFilePattern.MatchString(command)
}
func matchFilesystemFormat(command string, _ Shell) bool { return formatPattern.MatchString(command) }
func matchDeviceOverwrite(command string, _ Shell) bool {
	return deviceOverwritePattern.MatchString(command)
}
func matchRecursivePermissionChange(command string, _ Shell) bool {
	return permissionPattern.MatchString(command)
}
func matchGitResetHard(command string, _ Shell) bool  { return gitResetPattern.MatchString(command) }
func matchGitCleanForce(command string, _ Shell) bool { return gitCleanPattern.MatchString(command) }
func matchGitCheckoutAll(command string, _ Shell) bool {
	return gitCheckoutPattern.MatchString(strings.TrimSpace(command))
}
func matchGitBranchDelete(command string, _ Shell) bool { return gitBranchPattern.MatchString(command) }
func matchGitPushForce(command string, _ Shell) bool    { return gitPushPattern.MatchString(command) }
