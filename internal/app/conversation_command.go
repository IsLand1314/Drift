package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/IsLand1314/Drift/internal/conversation"
)

func runConversationCommand(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "-h" || args[0] == "--help")) {
		if len(args) == 0 {
			fmt.Fprintln(stderr, "用法：drift conversation list | drift conversation show <id> | drift conversation delete <id> --yes")
			return 2
		}
		fmt.Fprintln(out, "用法：drift conversation list | drift conversation show <id> | drift conversation delete <id> --yes")
		return 0
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取当前目录")
		return 1
	}
	store := conversation.NewStore(root)
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "用法：drift conversation list")
			return 2
		}
		metadata, err := store.List()
		if err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		if len(metadata) == 0 {
			fmt.Fprintln(out, "暂无完整会话")
			return 0
		}
		for _, item := range metadata {
			fmt.Fprintf(out, "%s updated_at=%s messages=%d context_bytes=%d", item.ID, item.UpdatedAt.UTC().Format(timeFormat), item.MessageCount, item.ContextBytes)
			if item.Focus != "" {
				fmt.Fprintf(out, " focus=%s", item.Focus)
			}
			fmt.Fprintln(out)
		}
		return 0
	case "show":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(stderr, "用法：drift conversation show <id>")
			return 2
		}
		snapshot, err := store.Load(args[1])
		if errors.Is(err, conversation.ErrNotFound) {
			fmt.Fprintln(stderr, "错误：完整会话不存在")
			return 2
		}
		if err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		fmt.Fprintf(out, "id=%s version=%d created_at=%s updated_at=%s focus=%s messages=%d context_bytes=%d\n", snapshot.ID, snapshot.Version, snapshot.CreatedAt.UTC().Format(timeFormat), snapshot.UpdatedAt.UTC().Format(timeFormat), snapshot.Focus, len(snapshot.Messages), snapshot.ContextBytes)
		return 0
	case "delete":
		if len(args) != 3 || strings.TrimSpace(args[1]) == "" || args[2] != "--yes" {
			fmt.Fprintln(stderr, "用法：drift conversation delete <id> --yes")
			return 2
		}
		err := store.Delete(args[1])
		if errors.Is(err, conversation.ErrNotFound) {
			fmt.Fprintln(stderr, "错误：完整会话不存在")
			return 2
		}
		if err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(out, "已删除完整会话："+args[1])
		return 0
	default:
		fmt.Fprintln(stderr, "用法：drift conversation list | drift conversation show <id> | drift conversation delete <id> --yes")
		return 2
	}
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"
