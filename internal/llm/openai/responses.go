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
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/llm/transport"
)

// ResponsesClient adapts OpenAI's Responses streaming protocol. It is kept
// separate from Client because Responses and Chat Completions use different
// request and event schemas.
type ResponsesClient struct {
	endpoint string
	key      string
	http     *http.Client
}

func NewResponses(baseURL, key string) (*ResponsesClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("base-url 必须是无凭据、查询参数和片段的 HTTP(S) API 根地址")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/responses"
	u.RawPath = ""
	return &ResponsesClient{endpoint: u.String(), key: key, http: &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *ResponsesClient) Capabilities() llm.Capabilities {
	return llm.Capabilities{NativeToolCalls: true}
}

func (c *ResponsesClient) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return llm.WithTerminal(ctx, emit, func(emit func(llm.Event) error) error {
		body, err := json.Marshal(responsesRequest(request))
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
		return readResponsesEvents(transport.NewSSESanitizer(transport.NewIdleTimeoutReader(ctx, resp.Body, 30*time.Second)), emit)
	})
}

type responsesRequestBody struct {
	Model  string           `json:"model"`
	Input  []map[string]any `json:"input"`
	Tools  []responseTool   `json:"tools,omitempty"`
	Stream bool             `json:"stream"`
}

type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func responsesRequest(input llm.Request) responsesRequestBody {
	result := responsesRequestBody{Model: input.Model, Stream: true}
	for _, message := range input.Messages {
		role := message.Role
		if role == "system" {
			role = "developer"
		}
		if message.Role == "assistant" && (message.ReasoningContent != "" || message.EncryptedReasoning != "") {
			reasoning := map[string]any{"type": "reasoning"}
			if message.EncryptedReasoning != "" {
				reasoning["encrypted_content"] = message.EncryptedReasoning
			}
			if message.ReasoningContent != "" {
				reasoning["summary"] = []map[string]string{{"type": "summary_text", "text": message.ReasoningContent}}
			}
			result.Input = append(result.Input, reasoning)
		}
		if message.Content != "" || (len(message.ToolCalls) == 0 && message.Role != "tool") {
			result.Input = append(result.Input, map[string]any{"type": "message", "role": role, "content": message.Content})
		}
		for _, call := range message.ToolCalls {
			result.Input = append(result.Input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Arguments})
		}
		if message.Role == "tool" {
			result.Input = append(result.Input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
		}
	}
	for _, definition := range input.Tools {
		var function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(definition.Function, &function) == nil && function.Name != "" {
			result.Tools = append(result.Tools, responseTool{Type: "function", Name: function.Name, Description: function.Description, Parameters: function.Parameters})
		}
	}
	return result
}

func readResponsesEvents(r io.Reader, emit func(llm.Event) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	var data []string
	calls := make(map[string]*responseCall)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(value, " "))
			}
			continue
		}
		if len(data) == 0 {
			continue
		}
		payload := strings.Join(data, "\n")
		data = nil
		if strings.TrimSpace(payload) == "[DONE]" {
			return emit(llm.StreamEnd{Status: llm.StreamCompleted, FinishReason: "stop"})
		}
		var event struct {
			Type      string `json:"type"`
			Delta     string `json:"delta"`
			ItemID    string `json:"item_id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Item      struct {
				Type             string `json:"type"`
				EncryptedContent string `json:"encrypted_content"`
				Summary          []struct {
					Text string `json:"text"`
				} `json:"summary"`
			} `json:"item"`
			Response struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return &llm.ProviderError{Stage: llm.ErrorStageSSEInvalidJSON, Message: "模型 SSE 流包含无效 JSON"}
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				if err := emit(llm.TextDelta{Text: event.Delta}); err != nil {
					return err
				}
			}
		case "response.reasoning_summary_text.delta":
			if event.Delta != "" {
				if err := emit(llm.ThinkingDelta{Text: event.Delta}); err != nil {
					return err
				}
			}
		case "response.output_item.done":
			if event.Item.Type == "reasoning" {
				var summary strings.Builder
				for _, part := range event.Item.Summary {
					summary.WriteString(part.Text)
				}
				if err := emit(llm.ThinkingComplete{Thinking: summary.String(), EncryptedContent: event.Item.EncryptedContent}); err != nil {
					return err
				}
			}
		case "response.function_call_arguments.delta":
			callID := event.ItemID
			if callID == "" {
				callID = event.CallID
			}
			call := calls[callID]
			if call == nil {
				call = &responseCall{Index: len(calls), ID: callID, Name: event.Name}
				calls[callID] = call
				if err := emit(llm.ToolCallStart{Index: call.Index, ID: call.ID, Name: call.Name}); err != nil {
					return err
				}
			}
			call.Arguments += event.Delta
		case "response.function_call_arguments.done":
			callID := event.ItemID
			if callID == "" {
				callID = event.CallID
			}
			call := calls[callID]
			if call == nil {
				call = &responseCall{Index: len(calls), ID: callID, Name: event.Name}
				calls[callID] = call
			}
			if event.Arguments != "" {
				call.Arguments = event.Arguments
			}
			if err := emit(llm.ToolCallComplete{Index: call.Index, ID: call.ID, Name: call.Name, Arguments: call.Arguments}); err != nil {
				return err
			}
		case "response.completed":
			var usage *llm.Usage
			if event.Response.Usage != nil {
				usage = &llm.Usage{InputTokens: event.Response.Usage.InputTokens, OutputTokens: event.Response.Usage.OutputTokens, TotalTokens: event.Response.Usage.TotalTokens}
			}
			return emit(llm.StreamEnd{Status: llm.StreamCompleted, FinishReason: "stop", Usage: usage})
		case "response.failed", "response.incomplete":
			return &llm.ProviderError{Stage: llm.ErrorStageSSEServerError, Message: "模型 Responses 流执行失败"}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, transport.ErrIdleTimeout) {
			return &llm.ProviderError{Stage: llm.ErrorStageTimeout, Message: "模型 SSE 流空闲超时", Cause: err}
		}
		return &llm.ProviderError{Stage: llm.ErrorStageSSERead, Message: "模型 SSE 流读取失败", Cause: err}
	}
	return &llm.ProviderError{Stage: llm.ErrorStageSSEDisconnected, Message: "模型 SSE 流意外断开"}
}

type responseCall struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}
