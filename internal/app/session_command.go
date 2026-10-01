package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/layout"
	"github.com/IsLand1314/Drift/internal/session"
)

func runAuditCommand(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "-h" || args[0] == "--help")) {
		if len(args) == 0 {
			fmt.Fprintln(stderr, "用法：drift audit list | drift audit show <jsonl路径>")
			return 2
		}
		fmt.Fprintln(out, "用法：drift audit list | drift audit show <jsonl路径>")
		return 0
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "用法：drift audit list")
			return 2
		}
		return listAudits(out, stderr)
	case "show":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(stderr, "用法：drift audit show <jsonl路径>")
			return 2
		}
		return showAudit(args[1], out, stderr)
	default:
		fmt.Fprintln(stderr, "用法：drift audit list | drift audit show <jsonl路径>")
		return 2
	}
}

func listAudits(out, stderr io.Writer) int {
	storageLayout := layout.ForWorkspace(".")
	if err := storageLayout.Prepare(); err != nil {
		fmt.Fprintln(stderr, "错误：无法准备本地存储目录：", err)
		return 1
	}
	files, err := session.ListFiles(storageLayout.Audits)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(out, "暂无会话记录")
			return 0
		}
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintln(out, "暂无会话记录")
		return 0
	}
	for _, path := range files {
		entries, err := session.ReadEntries(path)
		if err != nil {
			fmt.Fprintln(stderr, "错误：", err)
			return 1
		}
		fmt.Fprintf(out, "%s events=%d\n", filepath.Base(path), len(entries))
	}
	return 0
}

func showAudit(path string, out, stderr io.Writer) int {
	entries, err := session.ReadEntries(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "错误：审计文件不存在")
			return 2
		}
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	var textDeltaCount, textDeltaBytes int
	var textDeltaTime time.Time
	flushTextDeltas := func() {
		if textDeltaCount == 0 {
			return
		}
		fmt.Fprintf(out, "%s text_delta events=%d text_bytes=%d\n",
			textDeltaTime.UTC().Format("2006-01-02T15:04:05Z07:00"), textDeltaCount, textDeltaBytes)
		textDeltaCount, textDeltaBytes = 0, 0
		textDeltaTime = time.Time{}
	}
	for _, entry := range entries {
		if entry.Type == "text_delta" {
			if textDeltaCount == 0 {
				textDeltaTime = entry.Time
			}
			textDeltaCount++
			textDeltaBytes += entry.TextBytes
			continue
		}
		flushTextDeltas()
		fmt.Fprintln(out, formatSessionEntry(entry))
	}
	flushTextDeltas()
	return 0
}

func formatSessionEntry(entry session.Entry) string {
	line := entry.Time.UTC().Format("2006-01-02T15:04:05Z07:00") + " " + entry.Type
	if entry.Tool != "" {
		line += " tool=" + entry.Tool
	}
	if entry.Path != "" {
		line += " path=" + entry.Path
	}
	if entry.TextBytes > 0 {
		line += fmt.Sprintf(" text_bytes=%d", entry.TextBytes)
	}
	if entry.ArgumentBytes > 0 {
		line += fmt.Sprintf(" argument_bytes=%d", entry.ArgumentBytes)
	}
	if entry.ResultBytes > 0 {
		line += fmt.Sprintf(" result_bytes=%d", entry.ResultBytes)
	}
	if entry.Stage != "" {
		line += " stage=" + entry.Stage
	}
	if entry.FinishReason != "" {
		line += " finish_reason=" + entry.FinishReason
	}
	return line
}
