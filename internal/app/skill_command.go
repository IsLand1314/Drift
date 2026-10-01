package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/IsLand1314/Drift/internal/skill"
)

func runSkillCommand(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "show") {
		fmt.Fprintln(stderr, "用法：drift skill list [-w 路径] 或 drift skill show [-w 路径] 名称")
		return 2
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
