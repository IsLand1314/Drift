package session

import (
	"time"

	"github.com/IsLand1314/Drift/internal/agent"
)

// Entry 是 JSONL 会话审计文件中的单行记录。
type Entry struct {
	Version           int       `json:"version"`
	Type              string    `json:"type"`
	Time              time.Time `json:"time"`
	Text              string    `json:"text,omitempty"`
	Skill             string    `json:"skill,omitempty"`
	TextBytes         int       `json:"text_bytes,omitempty"`
	ToolCallID        string    `json:"tool_call_id,omitempty"`
	TaskID            string    `json:"task_id,omitempty"`
	Tool              string    `json:"tool,omitempty"`
	MCPServer         string    `json:"mcp_server,omitempty"`
	Path              string    `json:"path,omitempty"`
	CWD               string    `json:"cwd,omitempty"`
	CommandBytes      int       `json:"command_bytes,omitempty"`
	Revision          string    `json:"revision,omitempty"`
	Arguments         string    `json:"arguments,omitempty"`
	ArgumentBytes     int       `json:"argument_bytes,omitempty"`
	Result            string    `json:"result,omitempty"`
	ResultBytes       int       `json:"result_bytes,omitempty"`
	Error             string    `json:"error,omitempty"`
	Stage             string    `json:"stage,omitempty"`
	FinishReason      string    `json:"finish_reason,omitempty"`
	BeforeBytes       int       `json:"before_bytes,omitempty"`
	AfterBytes        int       `json:"after_bytes,omitempty"`
	MessageCount      int       `json:"message_count,omitempty"`
	KeptMessages      int       `json:"kept_messages,omitempty"`
	Operation         string    `json:"operation,omitempty"`
	OldBytes          int       `json:"old_bytes,omitempty"`
	NewBytes          int       `json:"new_bytes,omitempty"`
	Allowed           bool      `json:"allowed,omitempty"`
	DecisionReason    string    `json:"decision_reason,omitempty"`
	PermissionSource  string    `json:"permission_source,omitempty"`
	PermissionOutcome string    `json:"permission_outcome,omitempty"`
	Policy            string    `json:"policy,omitempty"`
	SandboxMode       string    `json:"sandbox_mode,omitempty"`
	SandboxBackend    string    `json:"sandbox_backend,omitempty"`
	SandboxAvailable  bool      `json:"sandbox_available"`
	SandboxProbe      string    `json:"sandbox_probe,omitempty"`
	ExecutionStatus   string    `json:"execution_status,omitempty"`
	ChildState        string    `json:"child_state,omitempty"`
	FailureReason     string    `json:"failure_reason,omitempty"`
	InputTokens       int       `json:"input_tokens,omitempty"`
	OutputTokens      int       `json:"output_tokens,omitempty"`
	TotalTokens       int       `json:"total_tokens,omitempty"`
	PlanID            string    `json:"plan_id,omitempty"`
	PlanPhase         string    `json:"plan_phase,omitempty"`
}

// Writer 追加 Agent 事件，并在关闭后拒绝继续写入。
type Writer interface {
	Append(event agent.Event) error
	Close() error
}
