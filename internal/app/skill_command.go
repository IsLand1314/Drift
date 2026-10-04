package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/IsLand1314/Drift/internal/skill"
)

func runSkillCommand(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "show" && args[0] != "install" && args[0] != "remove" && args[0] != "enable" && args[0] != "disable") {
		fmt.Fprintln(stderr, "用法：drift skill list|show|install|remove|enable|disable [-w 路径]")
		return 2
	}
	if args[0] == "install" {
		return runSkillInstall(args[1:], out, stderr)
	}
	if args[0] == "remove" {
		return runSkillRemove(args[1:], out, stderr)
	}
	if args[0] == "enable" || args[0] == "disable" {
		return runSkillToggle(args[1:], args[0] == "enable", out, stderr)
	}
	workspaceTarget, name, err := parseSkillArgs(args[1:], args[0] == "show")
	if err != nil {
		fmt.Fprintln(stderr, "错误：", err)
		return 2
	}
	launchDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取启动目录")
		return 1
	}
	selection, err := resolveWorkspace(launchDir, workspaceTarget)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 目标无效")
		return 2
	}
	if args[0] == "list" {
		items, err := skill.List(selection.Root)
		if err != nil {
			fmt.Fprintln(stderr, "错误：Skill 列表不可用")
			return 1
		}
		for _, item := range items {
			fmt.Fprintln(out, item.Name)
		}
		return 0
	}
	loaded, err := skill.Load(selection.Root, name)
	if err != nil {
		fmt.Fprintln(stderr, "错误：Skill 不可用")
		return 2
	}
	_, _ = io.WriteString(out, loaded.Content)
	return 0
}

func runSkillToggle(args []string, enabled bool, out, stderr io.Writer) int {
	workspaceTarget, name, err := parseSkillRemoveArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "错误：", err)
		return 2
	}
	launchDir, err := os.Getwd()
	if err != nil {
		return 1
	}
	selection, err := resolveWorkspace(launchDir, workspaceTarget)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 目标无效")
		return 2
	}
	if err := skill.SetEnabled(selection.Root, name, enabled); err != nil {
		fmt.Fprintln(stderr, "错误：Skill 状态更新失败")
		return 1
	}
	fmt.Fprintf(out, "已%s Skill：%s\n", map[bool]string{true: "启用", false: "禁用"}[enabled], name)
	return 0
}

func runSkillInstall(args []string, out, stderr io.Writer) int {
	workspaceTarget, source, name, preview, err := parseSkillInstallArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "错误：", err)
		return 2
	}
	if preview {
		fmt.Fprintf(out, "预览 Skill 安装：source=%s name=%s（只复制 SKILL.md 和 skill.json，不执行内容）\n", source, name)
		return 0
	}
	launchDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取启动目录")
		return 1
	}
	selection, err := resolveWorkspace(launchDir, workspaceTarget)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 目标无效")
		return 2
	}
	if err := skill.Install(selection.Root, source, name); err != nil {
		fmt.Fprintln(stderr, "错误：Skill 安装失败")
		return 1
	}
	fmt.Fprintln(out, "已安装 Skill：", name)
	return 0
}

func runSkillRemove(args []string, out, stderr io.Writer) int {
	workspaceTarget, name, err := parseSkillRemoveArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "错误：", err)
		return 2
	}
	launchDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取启动目录")
		return 1
	}
	selection, err := resolveWorkspace(launchDir, workspaceTarget)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 目标无效")
		return 2
	}
	if err := skill.Remove(selection.Root, name); err != nil {
		fmt.Fprintln(stderr, "错误：Skill 删除失败")
		return 1
	}
	fmt.Fprintln(out, "已删除 Skill：", name)
	return 0
}

func parseSkillInstallArgs(args []string) (workspace, source, name string, preview bool, err error) {
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-w" {
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", "", "", false, fmt.Errorf("-w 需要路径")
			}
			workspace, i = args[i+1], i+1
			continue
		}
		if args[i] == "--preview" {
			preview = true
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			return "", "", "", false, fmt.Errorf("不支持参数 %s", args[i])
		}
		positional = append(positional, args[i])
	}
	if len(positional) < 1 || len(positional) > 2 {
		return "", "", "", false, fmt.Errorf("install 需要 source [name]")
	}
	source = positional[0]
	name = filepath.Base(filepath.Clean(source))
	if len(positional) == 2 {
		name = positional[1]
	}
	return workspace, source, name, preview, nil
}

func parseSkillRemoveArgs(args []string) (workspace, name string, err error) {
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-w" {
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", "", fmt.Errorf("-w 需要路径")
			}
			workspace, i = args[i+1], i+1
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			return "", "", fmt.Errorf("不支持参数 %s", args[i])
		}
		positional = append(positional, args[i])
	}
	if len(positional) != 1 {
		return "", "", fmt.Errorf("remove 需要名称")
	}
	return workspace, positional[0], nil
}

func parseSkillArgs(args []string, requireName bool) (workspace, name string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-w":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", "", fmt.Errorf("-w 需要路径")
			}
			workspace = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("不支持参数 %s", args[i])
			}
			if name != "" {
				return "", "", fmt.Errorf("参数过多")
			}
			name = args[i]
		}
	}
	if requireName && name == "" {
		return "", "", fmt.Errorf("show 需要 Skill 名称")
	}
	if !requireName && name != "" {
		return "", "", fmt.Errorf("list 不接受 Skill 名称")
	}
	return workspace, name, nil
}
