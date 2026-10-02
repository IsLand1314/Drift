package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/IsLand1314/Drift/internal/changes"
)

func runChangeCommand(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "restore" {
		fmt.Fprintln(stderr, "用法：drift change restore [-w <workspace>] <change-dir> --yes")
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取当前目录：", err)
		return 1
	}
	var dirArg string
	confirmed := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--yes":
			confirmed = true
		case "-w", "--workspace":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(stderr, "用法：drift change restore [-w <workspace>] <change-dir> --yes")
				return 2
			}
			root = args[i+1]
			i++
		default:
			if dirArg != "" {
				fmt.Fprintln(stderr, "用法：drift change restore [-w <workspace>] <change-dir> --yes")
				return 2
			}
			dirArg = args[i]
		}
	}
	if dirArg == "" || !confirmed {
		fmt.Fprintln(stderr, "用法：drift change restore [-w <workspace>] <change-dir> --yes")
		return 2
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(stderr, "错误：workspace 路径无效：", err)
		return 2
	}
	dir, err := filepath.Abs(dirArg)
	if err != nil {
		fmt.Fprintln(stderr, "错误：change set 路径无效：", err)
		return 2
	}
	if err := changes.Restore(root, dir); err != nil {
		fmt.Fprintln(stderr, "恢复失败：", err)
		return 1
	}
	fmt.Fprintln(out, "已恢复 change set：", dir)
	return 0
}
