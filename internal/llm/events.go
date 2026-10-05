package llm

// Event is the provider-independent event source used by providers and collectors.
type Event interface{ event() }

type TextDelta struct{ Text string }

func (TextDelta) event() {}

type ThinkingDelta struct{ Text string }

func (ThinkingDelta) event() {}

type ThinkingComplete struct {
	Thinking         string
	Signature        string
	EncryptedContent string
}

func (ThinkingComplete) event() {}

type ToolCallStart struct {
	Index int
	ID    string
	Name  string
}

func (ToolCallStart) event() {}

type ToolCallComplete struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

func (ToolCallComplete) event() {}

type StreamEnd struct {
	Status       StreamStatus
	FinishReason string
	Usage        *Usage
	Error        *StreamError
}

func (StreamEnd) event() {}

type StreamStatus string

const (
	StreamCompleted StreamStatus = "completed"
	StreamFailed    StreamStatus = "failed"
	StreamCancelled StreamStatus = "cancelled"
)

// StreamEvent is a test/migration payload only. Production Providers and the
// Agent Collector use the typed events above.
type StreamEvent struct {
	Text             string
	ReasoningContent string
	ToolCallDelta    *ToolCallDelta
}

func (StreamEvent) event() {}

type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

func (ToolCallDelta) event() {}
