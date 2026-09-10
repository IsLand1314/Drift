// Package llm defines provider-independent streaming messages.
package llm

import (
	"context"
	"encoding/json"
)

type ToolDefinition struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function"`
}

type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

type Message struct {
	Role             string
	Content          string
	ToolCalls        []ToolCall
	ToolCallID       string
	ReasoningContent string
}

type Request struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

type StreamEvent struct {
	Text             string
	ReasoningContent string
	ToolCallDelta    *ToolCallDelta
}

type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type Completion struct {
	Assistant    Message
	FinishReason string
}

// Client emits stream events synchronously. Returning an error from emit stops the stream.
type Client interface {
	Stream(context.Context, Request, func(StreamEvent) error) (Completion, error)
}
