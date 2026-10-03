package agent

import (
	"context"

	"github.com/IsLand1314/Drift/internal/tool"
)

// EventType 是 Agent 对上层输出的稳定事件分类。
type EventType string

type PolicyDecision string

const (
	PolicyAllow PolicyDecision = "allow"
	PolicyAsk   PolicyDecision = "ask"
	PolicyDeny  PolicyDecision = "deny"
)

type ApprovalDecision string

const (
	ApprovalAllowOnce       ApprovalDecision = "allow_once"
	ApprovalAllowPersistent ApprovalDecision = "allow_persistent"
	ApprovalDeny            ApprovalDecision = "deny"
	ApprovalCancelled       ApprovalDecision = "cancelled"
)

type PermissionSource string

const (
	PermissionSourceMode       PermissionSource = "mode"
	PermissionSourcePersistent PermissionSource = "persistent"
	PermissionSourceSession    PermissionSource = "session"
	PermissionSourceUser       PermissionSource = "user"
	PermissionSourceSystem     PermissionSource = "system"
)

const (
	EventRunStarted         EventType = "run_started"
	EventTextDelta          EventType = "text_delta"
	EventToolCall           EventType = "tool_call"
	EventToolResult         EventType = "tool_result"
	EventPermissionRequest  EventType = "permission_request"
	EventPermissionDecision EventType = "permission_decision"
	EventModelUsage         EventType = "model_usage"
	EventError              EventType = "error"
	EventRunFinished        EventType = "run_finished"
	EventCompactionStarted  EventType = "compaction_started"
	EventCompactionFinished EventType = "compaction_finished"
	EventCompactionError    EventType = "compaction_error"
)

// Event 是脱离 Provider SSE 分片后的 Runtime 事件。
// Agent 只对内部错误做最小脱敏；完整的凭据和路径脱敏由持久化消费者负责。
type Event struct {
	Type              EventType
	Text              string
	SkillName         string
	ToolCallID        string
	ToolName          string
	Arguments         string
	Result            string
	ErrorSummary      string
	Error             string
	Stage             string
	FinishReason      string
	BeforeBytes       int
	AfterBytes        int
	MessageCount      int
	KeptMessages      int
	Operation         string
	Path              string
	Command           string
	CWD               string
	Revision          string
	OldBytes          int
	NewBytes          int
	Allowed           bool
	DecisionReason    string
	PermissionSource  PermissionSource
	PermissionOutcome ApprovalDecision
	Policy            PolicyDecision
	SandboxMode       string
	SandboxBackend    string
	SandboxAvailable  bool
	SandboxProbe      string
	ExecutionStatus   string
	FailureReason     string
	InputTokens       int
	OutputTokens      int
	TotalTokens       int
	UsageAvailable    bool
}

// PermissionRequest describes a side effect before it is executed.
type PermissionRequest struct {
	ToolName  string
	Operation string
	Path      string
	Command   string
	CWD       string
	OldBytes  int
	NewBytes  int
	Diff      string
}

// PermissionDecision is returned by the interactive approval callback.
type PermissionDecision struct {
	Allow    bool
	Reason   string
	Policy   PolicyDecision
	Approval ApprovalDecision
	Source   PermissionSource
}

// PermissionPrompt asks the caller whether a previewed operation may execute.
type PermissionPrompt func(context.Context, PermissionRequest) (PermissionDecision, error)

// QuestionPrompt gathers a structured clarification. It is intentionally
// separate from PermissionPrompt and cannot authorize tools or sandboxes.
type QuestionPrompt func(context.Context, tool.Question) (tool.QuestionAnswer, error)

// EventSink 消费 Agent 事件；返回错误会立即中止本次运行。
type EventSink func(Event) error
