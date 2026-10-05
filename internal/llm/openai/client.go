// Package openai adapts OpenAI-compatible Chat Completions SSE.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/llm/transport"
)

type Client struct {
	endpoint string
	key      string
	http     *http.Client
}

func (c *Client) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return llm.WithTerminal(ctx, emit, func(emit func(llm.Event) error) error {
		return c.streamEvents(ctx, request, emit)
	})
}

func (c *Client) streamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	body, err := json.Marshal(requestBody(request))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("无法创建模型请求")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var networkErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkErr) && networkErr.Timeout()) {
			return &llm.ProviderError{Stage: llm.ErrorStageTimeout, Message: "模型请求超时，请检查网络和服务状态", Cause: err}
		}
		return &llm.ProviderError{Stage: llm.ErrorStageTransport, Message: "模型连接失败，请检查地址、网络和服务状态", Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return &llm.ProviderError{Stage: llm.ErrorStageHTTP, Message: "模型认证失败：HTTP 401，请检查当前 Provider 的 API Key 是否有效"}
		}
		return &llm.ProviderError{Stage: llm.ErrorStageHTTP, Message: fmt.Sprintf("模型请求失败：HTTP %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))}
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "text/event-stream" {
		return &llm.ProviderError{Stage: llm.ErrorStageNonSSE, Message: "模型未返回 SSE 流，请检查 API 地址及流式支持"}
	}
	return readStreamEvents(transport.NewSSESanitizer(transport.NewIdleTimeoutReader(ctx, resp.Body, 30*time.Second)), emit)
}

func (c *Client) Capabilities() llm.Capabilities { return llm.Capabilities{NativeToolCalls: true} }

// New 将 API 根地址规范化为 /chat/completions，并保存请求所需的密钥。
// 密钥只保存在内存中，不会出现在错误文本或日志中。
func New(baseURL, key string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("base-url 必须是无凭据、查询参数和片段的 HTTP(S) API 根地址")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	u.RawPath = ""
	return &Client{endpoint: u.String(), key: key, http: &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func requestBody(input llm.Request) request {
	return request{Model: input.Model, Messages: openAIMessages(input.Messages), Tools: input.Tools, Stream: true, StreamOptions: &streamOptions{IncludeUsage: true}}
}

type request struct {
	Model         string               `json:"model"`
	Messages      []message            `json:"messages"`
	Tools         []llm.ToolDefinition `json:"tools,omitempty"`
	Stream        bool                 `json:"stream"`
	StreamOptions *streamOptions       `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func openAIMessages(messages []llm.Message) []message {
	// Provider 的 wire message 与内部 message 分离，避免 OpenAI 字段污染核心接口。
	result := make([]message, len(messages))
	for i, input := range messages {
		result[i] = message{Role: input.Role, Content: input.Content, ToolCallID: input.ToolCallID, ReasoningContent: input.ReasoningContent}
		for _, call := range input.ToolCalls {
			wire := toolCall{ID: call.ID, Type: call.Type}
			wire.Function.Name = call.Name
			wire.Function.Arguments = call.Arguments
			result[i].ToolCalls = append(result[i].ToolCalls, wire)
		}
	}
	return result
}

func readStreamEvents(r io.Reader, emit func(llm.Event) error) error {
	// SSE 以空行分隔事件；工具参数可能跨多个 delta，需要按 index 聚合。
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	size := 0
	calls := make(map[int]*llm.ToolCall)
	var reasoning strings.Builder
	finishReason := ""
	var usage *llm.Usage
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				value = strings.TrimPrefix(value, " ")
				size += len(value)
				if size > 1<<20 {
					return &llm.ProviderError{Stage: llm.ErrorStageSSEEventTooLarge, Message: "模型 SSE 事件超过 1 MiB 限制"}
				}
				data = append(data, value)
			}
			continue
		}
		if len(data) == 0 {
			continue
		}
		payload := strings.Join(data, "\n")
		data, size = nil, 0
		if strings.TrimSpace(payload) == "[DONE]" {
			if reasoning.Len() > 0 {
				if err := emit(llm.ThinkingComplete{Thinking: reasoning.String()}); err != nil {
					return err
				}
			}
			indexes := make([]int, 0, len(calls))
			for index := range calls {
				indexes = append(indexes, index)
			}
			sort.Ints(indexes)
			for _, index := range indexes {
				call := calls[index]
				if err := emit(llm.ToolCallComplete{Index: index, ID: call.ID, Name: call.Name, Arguments: call.Arguments}); err != nil {
					return err
				}
			}
			return emit(llm.StreamEnd{Status: llm.StreamCompleted, FinishReason: finishReason, Usage: usage})
		}
		var chunk struct {
			Error json.RawMessage `json:"error"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Role             string `json:"role"`
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			return &llm.ProviderError{Stage: llm.ErrorStageSSEInvalidJSON, Message: "模型 SSE 流包含无效 JSON"}
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return &llm.ProviderError{Stage: llm.ErrorStageSSEServerError, Message: "模型 SSE 流返回服务端错误"}
		}
		if chunk.Usage != nil {
			if chunk.Usage.PromptTokens < 0 || chunk.Usage.CompletionTokens < 0 || chunk.Usage.TotalTokens < 0 {
				return &llm.ProviderError{Stage: llm.ErrorStageSSEInvalidJSON, Message: "模型 SSE usage 数值无效"}
			}
			usage = &llm.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, TotalTokens: chunk.Usage.TotalTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Content != "" {
				if err := emit(llm.TextDelta{Text: choice.Delta.Content}); err != nil {
					return err
				}
			}
			if choice.Delta.ReasoningContent != "" {
				reasoning.WriteString(choice.Delta.ReasoningContent)
				if err := emit(llm.ThinkingDelta{Text: choice.Delta.ReasoningContent}); err != nil {
					return err
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				call := calls[delta.Index]
				if call == nil {
					call = &llm.ToolCall{}
					calls[delta.Index] = call
					if err := emit(llm.ToolCallStart{Index: delta.Index, ID: delta.ID, Name: delta.Function.Name}); err != nil {
						return err
					}
				}
				if call.ID == "" {
					call.ID = delta.ID
				}
				if call.Type == "" {
					call.Type = delta.Type
				}
				if call.Name == "" {
					call.Name = delta.Function.Name
				}
				call.Arguments += delta.Function.Arguments
				if delta.Function.Arguments != "" {
					if err := emit(llm.ToolCallDelta{Index: delta.Index, ID: call.ID, Name: call.Name, Arguments: delta.Function.Arguments}); err != nil {
						return err
					}
				}
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
	}
	if scanner.Err() != nil {
		if errors.Is(scanner.Err(), transport.ErrIdleTimeout) {
			return &llm.ProviderError{Stage: llm.ErrorStageTimeout, Message: "模型 SSE 流空闲超时", Cause: scanner.Err()}
		}
		if strings.Contains(scanner.Err().Error(), "token too long") {
			return &llm.ProviderError{Stage: llm.ErrorStageSSELineTooLarge, Message: "模型 SSE 单行超过 1 MiB 限制", Cause: scanner.Err()}
		}
		return &llm.ProviderError{Stage: llm.ErrorStageSSERead, Message: "读取模型 SSE 流失败", Cause: scanner.Err()}
	}
	return &llm.ProviderError{Stage: llm.ErrorStageSSEDisconnected, Message: "模型 SSE 流提前断开，未收到 [DONE]"}
}
