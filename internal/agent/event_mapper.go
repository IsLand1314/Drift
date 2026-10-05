package agent

import "github.com/IsLand1314/Drift/internal/llm"

// mapModelEvent is the only Provider-event to Agent-event translation point.
// It deliberately does not reduce text or tool arguments; Collector owns that
// state, while this mapper is only the realtime UI/runtime projection.
func forwardModelEvent(event llm.Event, sink EventSink) error {
	if sink == nil {
		return nil
	}
	switch e := event.(type) {
	case llm.TextDelta:
		return sink(Event{Type: EventTextDelta, Text: e.Text})
	case llm.ThinkingDelta:
		return sink(Event{Type: EventThinkingDelta, Text: e.Text})
	case llm.ThinkingComplete:
		return sink(Event{Type: EventThinkingComplete, Text: e.Thinking, Thinking: e.Thinking, ThinkingSignature: e.Signature, EncryptedThinking: e.EncryptedContent})
	case llm.ToolCallStart:
		return sink(Event{Type: EventToolCallStarted, ToolCallID: e.ID, ToolName: e.Name})
	case llm.ToolCallComplete:
		return sink(Event{Type: EventToolCall, ToolCallID: e.ID, ToolName: e.Name, Arguments: e.Arguments})
	case llm.StreamEnd:
		status := e.Status
		if status == "" {
			status = llm.StreamCompleted
		}
		out := Event{Type: EventStreamEnded, FinishReason: e.FinishReason, StreamStatus: string(status)}
		if e.Error != nil {
			out.Error = e.Error.Message
			out.StreamErrorKind = string(e.Error.Kind)
			out.StreamRetryable = e.Error.Retryable
		}
		return sink(out)
	default:
		return nil
	}
}
