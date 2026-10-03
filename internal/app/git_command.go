package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
	gitops "github.com/IsLand1314/Drift/internal/git"
	"github.com/IsLand1314/Drift/internal/layout"
	"github.com/IsLand1314/Drift/internal/session"
	"github.com/IsLand1314/Drift/internal/tool"
)

func runGitCommand(args []string, out, stderr io.Writer) int {
	return runGitCommandContext(context.Background(), args, out, stderr)
}

func runGitCommandContext(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, gitUsage())
		return 2
	}
	if args[0] == "worktree" {
		return runGitWorktreeCommand(ctx, args[1:], out, stderr)
	}
	if args[0] != "revert" {
		fmt.Fprintln(stderr, gitUsage())
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取当前目录：", err)
		return 1
	}
	var target string
	confirmed := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--yes":
			confirmed = true
		case "-w", "--workspace":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(stderr, gitUsage())
				return 2
			}
			root = args[i+1]
			i++
		default:
			if target != "" {
				fmt.Fprintln(stderr, gitUsage())
				return 2
			}
			target = args[i]
		}
	}
	if target == "" || !confirmed {
		fmt.Fprintln(stderr, gitUsage())
		return 2
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 路径无效：", err)
		return 2
	}
	if repositoryRoot, rootErr := gitops.RepositoryRoot(ctx, root); rootErr == nil {
		root = repositoryRoot
	}
	started := time.Now().UTC()
	storage := layout.ForWorkspace(root)
	if err := storage.Prepare(); err != nil {
		fmt.Fprintln(stderr, "错误：无法准备审计目录：", err)
		return 1
	}
	auditPath := filepath.Join(layout.DateDir(storage.Audits, started), "git-revert-"+layout.FileTimestamp(started)+".jsonl")
	audit, err := session.NewJSONLWriterWithSecrets(auditPath, root)
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法打开审计记录：", err)
		return 1
	}
	defer audit.Close()
	emit := func(event agent.Event) {
		if err := audit.Append(event); err != nil {
			fmt.Fprintln(stderr, "警告：审计记录失败：", err)
		}
	}
	preflight, err := gitops.PreflightRevert(ctx, root, target)
	if err != nil {
		emit(agent.Event{Type: agent.EventToolResult, ToolName: "GitRevert", Revision: target, CWD: ".", ExecutionStatus: "denied", FailureReason: gitFailureReason(err)})
		fmt.Fprintln(stderr, "回滚失败：", err)
		return 1
	}
	emit(agent.Event{Type: agent.EventPermissionDecision, ToolName: "GitRevert", Command: "git revert --no-edit " + preflight.Commit, CWD: ".", Revision: preflight.Commit, Allowed: true, DecisionReason: "cli_explicit_confirmation", PermissionSource: agent.PermissionSourceUser, PermissionOutcome: agent.ApprovalAllowOnce, Policy: agent.PolicyAsk})
	result, err := gitops.Revert(ctx, preflight, tool.SandboxRequired)
	event := agent.Event{Type: agent.EventToolResult, ToolName: "GitRevert", Command: "git revert --no-edit " + preflight.Commit, CWD: ".", Revision: preflight.Commit, Result: result.Output, ExecutionStatus: result.Status, FailureReason: result.FailureReason, SandboxMode: string(tool.SandboxRequired)}
	event.SandboxBackend = result.Sandbox.Backend
	event.SandboxAvailable = result.Sandbox.Available
	event.SandboxProbe = result.Sandbox.Probe
	if err != nil && event.FailureReason == "" {
		event.FailureReason = gitFailureReason(err)
	}
	emit(event)
	if err != nil || result.Status != "success" {
		if err != nil {
			fmt.Fprintln(stderr, "回滚失败：", err)
		} else {
			fmt.Fprintln(stderr, "回滚失败：", result.FailureReason)
		}
		return 1
	}
	fmt.Fprintln(out, "已创建 Git 反向提交：", result.AfterHEAD)
	return 0
}

func gitUsage() string {
	return "用法：drift git revert [-w <workspace>] <commit> --yes | drift git worktree {create <name> [base] --yes|list|remove <name> --yes|merge <name> --yes} [-w <workspace>]"
}

func runGitWorktreeCommand(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, gitUsage())
		return 2
	}
	action := args[0]
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取当前目录：", err)
		return 1
	}
	var name, base string
	confirmed := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--yes":
			confirmed = true
		case "-w", "--workspace":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(stderr, gitUsage())
				return 2
			}
			root = args[i+1]
			i++
		default:
			if name == "" {
				name = args[i]
			} else if base == "" && action == "create" {
				base = args[i]
			} else {
				fmt.Fprintln(stderr, gitUsage())
				return 2
			}
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 路径无效：", err)
		return 2
	}
	switch action {
	case "list":
		if name != "" || confirmed {
			fmt.Fprintln(stderr, gitUsage())
			return 2
		}
		items, err := gitops.ListWorktrees(ctx, root)
		if err != nil {
			fmt.Fprintln(stderr, "列出 worktree 失败：", err)
			return 1
		}
		for _, item := range items {
			fmt.Fprintf(out, "%s\t%s\t%s\n", item.Path, item.HEAD, item.Branch)
		}
		return 0
	case "create":
		if name == "" || !confirmed {
			fmt.Fprintln(stderr, gitUsage())
			return 2
		}
		if base == "" {
			base = "HEAD"
		}
		item, err := gitops.CreateWorktree(ctx, root, name, base)
		if err != nil {
			fmt.Fprintln(stderr, "创建 worktree 失败：", err)
			return 1
		}
		fmt.Fprintln(out, "已创建 worktree：", item.Path)
		return 0
	case "remove":
		if name == "" || !confirmed {
			fmt.Fprintln(stderr, gitUsage())
			return 2
		}
		if err := gitops.RemoveWorktree(ctx, root, name); err != nil {
			fmt.Fprintln(stderr, "删除 worktree 失败：", err)
			return 1
		}
		fmt.Fprintln(out, "已删除 worktree：", name)
		return 0
	case "merge":
		if name == "" || !confirmed {
			fmt.Fprintln(stderr, gitUsage())
			return 2
		}
		result, err := gitops.MergeWorktree(ctx, root, name)
		if err != nil {
			fmt.Fprintln(stderr, "合并 worktree 失败：", err)
			return 1
		}
		if result.Status == "conflict" {
			fmt.Fprintln(stderr, "合并未执行：检测到冲突")
			for _, path := range result.Conflicts {
				fmt.Fprintln(stderr, "冲突文件：", path)
			}
			return 1
		}
		if result.Status != "success" {
			fmt.Fprintln(stderr, "合并 worktree 失败：", result.FailureReason)
			return 1
		}
		fmt.Fprintln(out, "已合并 worktree：", name)
		fmt.Fprintln(out, "merge commit：", result.AfterHEAD)
		return 0
	default:
		fmt.Fprintln(stderr, gitUsage())
		return 2
	}
}

func gitFailureReason(err error) string {
	message := strings.ToLower(err.Error())
	for _, reason := range []string{"invalid_target", "dirty_worktree", "merge_commit", "sandbox_denied", "not_git"} {
		if strings.Contains(message, reason) {
			return reason
		}
	}
	if strings.Contains(message, "sandbox") {
		return "sandbox_denied"
	}
	return "failed"
}
