package session

import (
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

// Entry 是 JSONL 会话审计文件中的单行记录。
type Entry struct {
	Version    int       `json:"version"`
	Type       string    `json:"type"`
	Time       time.Time `json:"time"`
	Text       string    `json:"text,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	Arguments  string    `json:"arguments,omitempty"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	Stage      string    `json:"stage,omitempty"`
}

// Writer 追加 Agent 事件，并在关闭后拒绝继续写入。
type Writer interface {
	Append(event agent.Event) error
	Close() error
}
