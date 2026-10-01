package session

import (
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

// Entry 是 JSONL 会话审计文件中的单行记录。
type Entry struct {
	Version       int       `json:"version"`
	Type          string    `json:"type"`
	Time          time.Time `json:"time"`
	Text          string    `json:"text,omitempty"`
	Skill         string    `json:"skill,omitempty"`
	TextBytes     int       `json:"text_bytes,omitempty"`
	ToolCallID    string    `json:"tool_call_id,omitempty"`
	Tool          string    `json:"tool,omitempty"`
	Path          string    `json:"path,omitempty"`
	Arguments     string    `json:"arguments,omitempty"`
	ArgumentBytes int       `json:"argument_bytes,omitempty"`
	Result        string    `json:"result,omitempty"`
	ResultBytes   int       `json:"result_bytes,omitempty"`
	Error         string    `json:"error,omitempty"`
	Stage         string    `json:"stage,omitempty"`
	FinishReason  string    `json:"finish_reason,omitempty"`
	BeforeBytes   int       `json:"before_bytes,omitempty"`
	AfterBytes    int       `json:"after_bytes,omitempty"`
	MessageCount  int       `json:"message_count,omitempty"`
	KeptMessages  int       `json:"kept_messages,omitempty"`
	InputTokens   int       `json:"input_tokens,omitempty"`
	OutputTokens  int       `json:"output_tokens,omitempty"`
	TotalTokens   int       `json:"total_tokens,omitempty"`
}

// Writer 追加 Agent 事件，并在关闭后拒绝继续写入。
type Writer interface {
	Append(event agent.Event) error
	Close() error
}
