package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

func collectTurn(ctx context.Context, client llm.Client, request llm.Request, sink EventSink) (TurnResult, error) {
	c := NewCollector()
	streamErr := client.StreamEvents(ctx, request, func(event llm.Event) error {
		if err := c.Accept(event); err != nil {
			return err
		}
		return forwardModelEvent(event, sink)
	})
	if streamErr != nil {
		return TurnResult{}, streamErr
	}
	if !c.Ended() {
		end := llm.StreamEnd{Status: llm.StreamFailed, Error: &llm.StreamError{Kind: llm.StreamErrorProtocol, Message: "模型流未发送结束事件"}}
		_ = c.Accept(end)
		_ = forwardModelEvent(end, sink)
		return TurnResult{}, end.Error
	}
	return c.Result()
}

// TurnResult is the agent-facing aggregate produced from the event stream.
type TurnResult struct {
	Assistant    llm.Message
	FinishReason string
	Usage        *llm.Usage
}

type Collector struct {
	content            string
	reasoning          string
	reasoningSignature string
	encryptedReasoning string
	calls              []llm.ToolCall
	finish             string
	usage              *llm.Usage
	ended              bool
	completedCalls     map[string]struct{}
	status             llm.StreamStatus
	streamError        *llm.StreamError
	callIndexes        map[string]int
}

func NewCollector() *Collector {
	return &Collector{completedCalls: make(map[string]struct{}), callIndexes: make(map[string]int)}
}

func (c *Collector) Ended() bool { return c.ended }

func (c *Collector) Accept(event llm.Event) error {
	if c.ended {
		return errors.New("agent: event received after stream end")
	}
	switch e := event.(type) {
	case llm.TextDelta:
		c.content += e.Text
	case llm.ThinkingDelta:
		c.reasoning += e.Text
	case llm.ThinkingComplete:
		if e.Thinking != "" {
			c.reasoning = e.Thinking
		}
		c.reasoningSignature = e.Signature
		c.encryptedReasoning = e.EncryptedContent
	case llm.ToolCallStart:
		index, err := c.callIndex(e.Index, e.ID)
		if err != nil {
			return err
		}
		for len(c.calls) <= index {
			c.calls = append(c.calls, llm.ToolCall{})
		}
		c.calls[index] = llm.ToolCall{ID: e.ID, Type: "function", Name: e.Name}
	case llm.ToolCallDelta:
		index, err := c.callIndex(e.Index, e.ID)
		if err != nil {
			return err
		}
		for len(c.calls) <= index {
			c.calls = append(c.calls, llm.ToolCall{})
		}
		call := &c.calls[index]
		if e.ID != "" {
			call.ID = e.ID
		}
		if e.Name != "" {
			call.Name = e.Name
		}
		call.Type = "function"
		call.Arguments += e.Arguments
	case llm.ToolCallComplete:
		if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Name) == "" || !json.Valid([]byte(e.Arguments)) {
			return errors.New("agent: incomplete tool call arguments")
		}
		if _, exists := c.completedCalls[e.ID]; exists {
			return errors.New("agent: duplicate tool call completion")
		}
		c.completedCalls[e.ID] = struct{}{}
		index, err := c.callIndex(e.Index, e.ID)
		if err != nil {
			return err
		}
		for len(c.calls) <= index {
			c.calls = append(c.calls, llm.ToolCall{})
		}
		if existing := c.calls[index]; existing.ID != "" && existing.ID != e.ID {
			return errors.New("agent: tool call id changed for index")
		}
		c.calls[index] = llm.ToolCall{ID: e.ID, Type: "function", Name: e.Name, Arguments: e.Arguments}
	case llm.StreamEnd:
		if e.Status == "" {
			e.Status = llm.StreamCompleted
		}
		c.status, c.streamError = e.Status, e.Error
		c.finish, c.usage = e.FinishReason, e.Usage
		c.ended = true
	default:
		return errors.New("agent: unsupported stream event")
	}
	return nil
}

func (c *Collector) callIndex(index int, id string) (int, error) {
	if id != "" {
		if previous, ok := c.callIndexes[id]; ok {
			if index >= 0 && previous != index {
				return 0, errors.New("agent: tool call id changed index")
			}
			return previous, nil
		}
	}
	if index < 0 {
		index = len(c.calls)
	}
	if id != "" {
		c.callIndexes[id] = index
	}
	return index, nil
}

func (c *Collector) Result() (TurnResult, error) {
	if !c.ended {
		return TurnResult{}, errors.New("agent: stream ended without StreamEnd")
	}
	if c.status == llm.StreamFailed || c.status == llm.StreamCancelled {
		if c.streamError != nil {
			return TurnResult{}, c.streamError
		}
		return TurnResult{}, errors.New("agent: model stream did not complete")
	}
	return TurnResult{Assistant: llm.Message{Role: "assistant", Content: c.content, ReasoningContent: c.reasoning, ReasoningSignature: c.reasoningSignature, EncryptedReasoning: c.encryptedReasoning, ToolCalls: append([]llm.ToolCall(nil), c.calls...)}, FinishReason: c.finish, Usage: c.usage}, nil
}
