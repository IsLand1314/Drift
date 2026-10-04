// Package llm defines provider-independent streaming messages.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

type ToolDefinition struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function"`
}

// ToolCall 是 Provider 聚合后的完整工具调用；Arguments 仍保持 JSON 字符串，
// 由具体工具自行做严格解码。
type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

// Message 是 Provider 无关的对话消息，既可以是 user/assistant，也可以是 tool 结果。
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
	Usage        *Usage
}

// Usage 是 Provider 报告的真实 token 用量；nil 表示响应没有提供 usage。
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// ErrorStage identifies the provider boundary that stopped a request.
type ErrorStage string

const (
	ErrorStageTimeout          ErrorStage = "provider_timeout"
	ErrorStageTransport        ErrorStage = "provider_transport"
	ErrorStageHTTP             ErrorStage = "provider_http"
	ErrorStageNonSSE           ErrorStage = "provider_non_sse"
	ErrorStageSSEInvalidJSON   ErrorStage = "provider_sse_invalid_json"
	ErrorStageSSEServerError   ErrorStage = "provider_sse_server_error"
	ErrorStageSSEEventTooLarge ErrorStage = "provider_sse_event_too_large"
	ErrorStageSSELineTooLarge  ErrorStage = "provider_sse_line_too_large"
	ErrorStageSSERead          ErrorStage = "provider_sse_read"
	ErrorStageSSEDisconnected  ErrorStage = "provider_sse_disconnected"
)

// ProviderError keeps a safe user-facing message separate from its cause.
type ProviderError struct {
	Stage   ErrorStage
	Message string
	Cause   error
}

func (e *ProviderError) Error() string { return e.Message }

func (e *ProviderError) Unwrap() error { return e.Cause }

// ErrorStageOf returns an empty string for errors that did not come from a provider.
func ErrorStageOf(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return string(providerErr.Stage)
	}
	return ""
}

// Client 同步发出文本、推理和工具调用增量事件。
// emit 返回错误时，Provider 应立即停止读取流并把错误传回调用方。
type Client interface {
	Stream(context.Context, Request, func(StreamEvent) error) (Completion, error)
}

type Capabilities struct {
	NativeToolCalls      bool
	NativeToolReferences bool
}

type CapabilityProvider interface {
	Capabilities() Capabilities
}
