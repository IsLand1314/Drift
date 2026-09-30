package agent

// EventType 是 Agent 对上层输出的稳定事件分类。
type EventType string

const (
	EventRunStarted  EventType = "run_started"
	EventTextDelta   EventType = "text_delta"
	EventToolCall    EventType = "tool_call"
	EventToolResult  EventType = "tool_result"
	EventError       EventType = "error"
	EventRunFinished EventType = "run_finished"
)

// Event 是脱离 Provider SSE 分片后的 Runtime 事件。
// Agent 只对内部错误做最小脱敏；完整的凭据和路径脱敏由持久化消费者负责。
type Event struct {
	Type         EventType
	Text         string
	ToolCallID   string
	ToolName     string
	Arguments    string
	Result       string
	Error        string
	Stage        string
	FinishReason string
}

// EventSink 消费 Agent 事件；返回错误会立即中止本次运行。
type EventSink func(Event) error
