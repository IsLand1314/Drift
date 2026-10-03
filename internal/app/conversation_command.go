package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/conversation"
	"github.com/IsLand1314/Drift/internal/layout"
)

func runSessionCommand(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "-h" || args[0] == "--help")) {
		if len(args) == 0 {
			fmt.Fprintln(stderr, sessionUsage)
			return 2
		}
		fmt.Fprintln(out, sessionUsage)
		return 0
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "错误：无法获取当前目录")
		return 1
	}
	if err := layout.ForWorkspace(root).Prepare(); err != nil {
		fmt.Fprintln(stderr, "错误：无法准备本地存储目录：", err)
		return 1
	}
	store := conversation.NewStore(root)
	switch args[0] {
	case "list":
		limit, ok := parseConversationLimit(args[1:])
		if !ok {
			fmt.Fprintln(stderr, "用法：drift session list [--limit N]")
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
		if limit < len(metadata) {
			metadata = metadata[:limit]
		}
		for _, item := range metadata {
			writeConversationMetadata(out, item)
		}
		return 0
	case "show":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(stderr, "用法：drift session show <id>")
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
		fmt.Fprintf(out, "id=%s version=%d title=%s created_at=%s updated_at=%s focus=%s messages=%d context_bytes=%d\n", snapshot.ID, snapshot.Version, snapshot.Title, snapshot.CreatedAt.UTC().Format(timeFormat), snapshot.UpdatedAt.UTC().Format(timeFormat), snapshot.Focus, len(snapshot.Messages), snapshot.ContextBytes)
		return 0
	case "timeline":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(stderr, "用法：drift session timeline <id>")
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
		writeConversationTimeline(out, snapshot)
		return 0
	case "search":
		query, limit, ok := parseConversationSearchArgs(args[1:])
		if !ok {
			fmt.Fprintln(stderr, "用法：drift session search <关键词> [--limit N]")
			return 2
		}
		results, err := store.Search(query, limit)
		if err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		if len(results) == 0 {
			fmt.Fprintln(out, "没有匹配的完整会话")
			return 0
		}
		for _, result := range results {
			writeConversationSearchResult(out, result)
		}
		return 0
	case "rename":
		if len(args) != 3 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(stderr, "用法：drift session rename <id> <title>")
			return 2
		}
		if err := store.Rename(args[1], args[2]); err != nil {
			if errors.Is(err, conversation.ErrNotFound) || errors.Is(err, conversation.ErrInvalidID) || errors.Is(err, conversation.ErrInvalidSnapshot) {
				fmt.Fprintln(stderr, "错误：会话标题或 ID 无效")
				return 2
			}
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		fmt.Fprintln(out, "已更新会话标题："+args[1])
		return 0
	case "prune":
		before, confirmed, ok := parsePruneArgs(args[1:])
		if !ok {
			fmt.Fprintln(stderr, "用法：drift session prune --before <RFC3339> [--yes]")
			return 2
		}
		if !confirmed {
			metadata, err := store.Before(before)
			if err != nil {
				fmt.Fprintln(stderr, "错误：", err)
				return 1
			}
			if len(metadata) == 0 {
				fmt.Fprintln(out, "没有匹配的完整会话")
				return 0
			}
			for _, item := range metadata {
				writeConversationMetadata(out, item)
			}
			fmt.Fprintln(out, "预览完成：未删除任何会话；如需删除请加 --yes")
			return 0
		}
		removed, err := store.Prune(before)
		if err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		fmt.Fprintf(out, "已清理完整会话：%d 个\n", len(removed))
		return 0
	case "delete":
		if len(args) != 3 || strings.TrimSpace(args[1]) == "" || args[2] != "--yes" {
			fmt.Fprintln(stderr, "用法：drift session delete <id> --yes")
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
		fmt.Fprintln(stderr, sessionUsage)
		return 2
	}
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

const sessionUsage = "用法：drift session list [--limit N] | drift session show <id> | drift session timeline <id> | drift session search <关键词> [--limit N] | drift session rename <id> <title> | drift session delete <id> --yes | drift session prune --before <RFC3339> [--yes]"

func parseConversationLimit(args []string) (int, bool) {
	if len(args) == 0 {
		return 50, true
	}
	if len(args) != 2 || args[0] != "--limit" {
		return 0, false
	}
	limit, err := strconv.Atoi(args[1])
	return limit, err == nil && limit > 0
}

func parseConversationSearchArgs(args []string) (string, int, bool) {
	if len(args) == 0 {
		return "", 0, false
	}
	limit := 50
	queryParts := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == "--limit" {
			if index+1 >= len(args) {
				return "", 0, false
			}
			parsed, err := strconv.Atoi(args[index+1])
			if err != nil || parsed < 1 || parsed > 50 {
				return "", 0, false
			}
			limit = parsed
			index++
			continue
		}
		queryParts = append(queryParts, args[index])
	}
	query := strings.TrimSpace(strings.Join(queryParts, " "))
	return query, limit, query != ""
}

func parsePruneArgs(args []string) (time.Time, bool, bool) {
	if len(args) < 2 || args[0] != "--before" {
		return time.Time{}, false, false
	}
	before, err := time.Parse(time.RFC3339, args[1])
	if err != nil {
		return time.Time{}, false, false
	}
	if len(args) == 2 {
		return before, false, true
	}
	if len(args) == 3 && args[2] == "--yes" {
		return before, true, true
	}
	return time.Time{}, false, false
}

func writeConversationMetadata(out io.Writer, item conversation.Metadata) {
	fmt.Fprintf(out, "%s", item.ID)
	if item.Title != "" {
		fmt.Fprintf(out, " title=%s", item.Title)
	}
	fmt.Fprintf(out, " updated_at=%s messages=%d context_bytes=%d", item.UpdatedAt.UTC().Format(timeFormat), item.MessageCount, item.ContextBytes)
	if item.Focus != "" {
		fmt.Fprintf(out, " focus=%s", item.Focus)
	}
	fmt.Fprintln(out)
}

func writeConversationSearchResult(out io.Writer, item conversation.SearchResult) {
	fmt.Fprintf(out, "%s", item.ID)
	if item.Title != "" {
		fmt.Fprintf(out, " title=%s", item.Title)
	}
	fmt.Fprintf(out, " updated_at=%s messages=%d context_bytes=%d matches=", item.UpdatedAt.UTC().Format(timeFormat), item.MessageCount, item.ContextBytes)
	for index, match := range item.Matches {
		if index > 0 {
			fmt.Fprint(out, ",")
		}
		fmt.Fprintf(out, "%s#%d", match.Role, match.Index)
	}
	fmt.Fprintln(out)
}

func writeConversationTimeline(out io.Writer, snapshot conversation.Snapshot) {
	for index, message := range snapshot.Messages {
		fmt.Fprintf(out, "%02d %s", index+1, message.Role)
		if len(message.ToolCalls) > 0 {
			names := make([]string, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				names = append(names, call.Name)
			}
			fmt.Fprintf(out, " tool_calls=%s", strings.Join(names, ","))
		}
		if message.ToolCallID != "" {
			fmt.Fprintf(out, " tool_call_id=%s", message.ToolCallID)
		}
		fmt.Fprintf(out, " bytes=%d\n", len(message.Content))
	}
}
