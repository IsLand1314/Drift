package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/IsLand1314/Drift/internal/changes"
)

func runChangeCommand(args []string, out, stderr io.Writer) int {
	if len(args) != 3 || args[0] != "restore" || args[2] != "--yes" {
		fmt.Fprintln(stderr, "用法：drift change restore <change-dir> --yes")
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取当前目录：", err)
		return 1
	}
	dir, err := filepath.Abs(args[1])
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
