// Package llm defines provider-independent streaming messages.
package llm

import (
	"context"
	"encoding/json"
)

type ToolDefinition struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function"`
	// Deferred asks providers with native tool search to load this schema on demand.
	Deferred bool `json:"-"`
}

// ToolCall 是 Collector 写入对话上下文的完整工具调用；Arguments 仍保持 JSON 字符串，
// 由具体工具自行做严格解码。Provider 只通过 ToolCall* 事件传输片段。
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
	// ReasoningSignature and EncryptedReasoning are provider protocol metadata
	// needed to replay reasoning-capable requests. ReasoningContent is kept with
	// the completed assistant message so a resumed session has one consistent
	// representation of the turn.
	ReasoningSignature string
	EncryptedReasoning string
}

type Request struct {
	Model                string           `json:"model"`
	Messages             []Message        `json:"messages"`
	Tools                []ToolDefinition `json:"tools,omitempty"`
	NativeToolReferences bool             `json:"-"`
}

// Usage 是 Provider 报告的真实 token 用量；nil 表示响应没有提供 usage。
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Client 是唯一的 Provider 流接口。最终回合结果由 Agent Collector 从
// StreamEvents 生成，Provider 不返回聚合回合对象。
type Client interface {
	StreamEvents(context.Context, Request, func(Event) error) error
}

type Capabilities struct {
	NativeToolCalls      bool
	NativeToolReferences bool
}

type CapabilityProvider interface {
	Capabilities() Capabilities
}
