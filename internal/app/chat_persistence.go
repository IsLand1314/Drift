package app

import (
	"fmt"
	"strings"
)

type chatPersistenceOptions struct {
	noSession bool
	resumeID  string
	resume    bool
}

func parseChatPersistenceArgs(args []string) ([]string, chatPersistenceOptions, error) {
	var options chatPersistenceOptions
	cleaned := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-session":
			if options.noSession {
				return nil, options, fmt.Errorf("--no-session 重复")
			}
			options.noSession = true
		case arg == "--resume":
			if options.resume {
				return nil, options, fmt.Errorf("--resume 重复")
			}
			options.resume = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				options.resumeID = args[i]
			}
		case strings.HasPrefix(arg, "--resume="):
			if options.resume {
				return nil, options, fmt.Errorf("--resume 重复")
			}
			options.resume = true
			options.resumeID = strings.TrimPrefix(arg, "--resume=")
			if options.resumeID == "" {
				return nil, options, fmt.Errorf("--resume 需要会话 ID")
			}
		default:
			cleaned = append(cleaned, arg)
		}
	}
	if options.noSession && options.resume {
		return nil, options, fmt.Errorf("--no-session 与 --resume 不能同时使用")
	}
	return cleaned, options, nil
}
