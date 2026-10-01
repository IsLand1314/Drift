// Package anthropic adapts Anthropic Messages API streaming to llm.Client.
package anthropic

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
)

const maxTokens = 4096

type Client struct {
	endpoint string
	key      string
	http     *http.Client
}

// New validates an Anthropic API root and appends the Messages endpoint.
func New(baseURL, key string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("base-url 必须是无凭据、查询参数和片段的 HTTP(S) API 根地址")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/messages"
	u.RawPath = ""
	return &Client{
		endpoint: u.String(),
		key:      key,
		http: &http.Client{
			Timeout: 5 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type request struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []message       `json:"messages"`
	Tools     []anthropicTool `json:"tools,omitempty"`
	Stream    bool            `json:"stream"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func (c *Client) Stream(ctx context.Context, input llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	body, err := encodeRequest(input)
	if err != nil {
		return llm.Completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return llm.Completion{}, errors.New("无法创建模型请求")
	}
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return llm.Completion{}, ctx.Err()
		}
		var networkErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkErr) && networkErr.Timeout()) {
			return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageTimeout, Message: "模型请求超时，请检查网络和服务状态", Cause: err}
		}
		return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageTransport, Message: "模型连接失败，请检查地址、网络和服务状态", Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageHTTP, Message: fmt.Sprintf("模型请求失败：HTTP %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))}
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "text/event-stream" {
		return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageNonSSE, Message: "模型未返回 SSE 流，请检查 API 地址及流式支持"}
	}
	completion, err := readStream(resp.Body, emit)
	if ctx.Err() != nil {
		return llm.Completion{}, ctx.Err()
	}
	return completion, err
}

func encodeRequest(input llm.Request) ([]byte, error) {
	result := request{Model: input.Model, MaxTokens: maxTokens, Stream: true}
	for _, current := range input.Messages {
		switch current.Role {
		case "system":
			if result.System != "" {
				result.System += "\n"
			}
			result.System += current.Content
		case "user", "assistant":
			blocks := make([]contentBlock, 0, 1+len(current.ToolCalls))
			if current.Content != "" {
				blocks = append(blocks, contentBlock{Type: "text", Text: current.Content})
			}
			for _, call := range current.ToolCalls {
				var object json.RawMessage
				if err := json.Unmarshal([]byte(call.Arguments), &object); err != nil || len(object) == 0 || string(object) == "null" {
					return nil, errors.New("Anthropic 工具调用参数不是有效 JSON")
				}
				blocks = append(blocks, contentBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: object})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, contentBlock{Type: "text", Text: ""})
			}
			result.Messages = append(result.Messages, message{Role: current.Role, Content: blocks})
		case "tool":
			if current.ToolCallID == "" {
				return nil, errors.New("Anthropic 工具结果缺少 tool_use_id")
			}
			result.Messages = append(result.Messages, message{Role: "user", Content: []contentBlock{{Type: "tool_result", ToolUseID: current.ToolCallID, Content: current.Content}}})
		default:
			return nil, fmt.Errorf("Anthropic 不支持消息角色 %q", current.Role)
		}
	}
	for _, definition := range input.Tools {
		var function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(definition.Function, &function); err != nil || function.Name == "" || len(function.Parameters) == 0 {
			return nil, errors.New("Anthropic 工具定义无效")
		}
		result.Tools = append(result.Tools, anthropicTool{Name: function.Name, Description: function.Description, InputSchema: function.Parameters})
	}
	return json.Marshal(result)
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func readStream(r io.Reader, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var eventName string
	var data []string
	completion := llm.Completion{Assistant: llm.Message{Role: "assistant"}}
	calls := make(map[int]*llm.ToolCall)
	inputTokens, outputTokens := 0, 0
	haveInput, haveOutput := false, false
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if strings.TrimSpace(payload) == "[DONE]" {
			return nil
		}
		var event streamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return &llm.ProviderError{Stage: llm.ErrorStageSSEInvalidJSON, Message: "模型 SSE 流包含无效 JSON", Cause: err}
		}
		if event.Type == "error" || len(event.Error) > 0 && string(event.Error) != "null" {
			return &llm.ProviderError{Stage: llm.ErrorStageSSEServerError, Message: "模型 SSE 流返回服务端错误"}
		}
		switch event.Type {
		case "message_start":
			inputTokens = event.Message.Usage.InputTokens
			haveInput = true
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				calls[event.Index] = &llm.ToolCall{ID: event.ContentBlock.ID, Type: "function", Name: event.ContentBlock.Name}
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				completion.Assistant.Content += event.Delta.Text
				if err := emit(llm.StreamEvent{Text: event.Delta.Text}); err != nil {
					return err
				}
			case "input_json_delta":
				call := calls[event.Index]
				if call == nil {
					return errors.New("Anthropic 工具参数块缺少 tool_use")
				}
				call.Arguments += event.Delta.PartialJSON
				if err := emit(llm.StreamEvent{ToolCallDelta: &llm.ToolCallDelta{Index: event.Index, ID: call.ID, Name: call.Name, Arguments: event.Delta.PartialJSON}}); err != nil {
					return err
				}
			}
		case "message_delta":
			completion.FinishReason = event.Delta.StopReason
			if completion.FinishReason == "end_turn" {
				completion.FinishReason = "stop"
			}
			if event.Usage.OutputTokens > 0 {
				outputTokens = event.Usage.OutputTokens
				haveOutput = true
			}
		case "message_stop":
			indexes := make([]int, 0, len(calls))
			for index := range calls {
				indexes = append(indexes, index)
			}
			sort.Ints(indexes)
			for _, index := range indexes {
				completion.Assistant.ToolCalls = append(completion.Assistant.ToolCalls, *calls[index])
			}
			if haveInput || haveOutput {
				completion.Usage = &llm.Usage{InputTokens: inputTokens, OutputTokens: outputTokens, TotalTokens: inputTokens + outputTokens}
			}
			return io.EOF
		}
		_ = eventName
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				if errors.Is(err, io.EOF) {
					return completion, nil
				}
				return llm.Completion{}, err
			}
			eventName, data = "", nil
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			eventName = strings.TrimSpace(value)
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
			if len(strings.Join(data, "\n")) > 1<<20 {
				return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageSSEEventTooLarge, Message: "模型 SSE 事件超过 1 MiB 限制"}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageSSELineTooLarge, Message: "模型 SSE 单行超过 1 MiB 限制", Cause: err}
		}
		return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageSSERead, Message: "读取模型 SSE 流失败", Cause: err}
	}
	return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageSSEDisconnected, Message: "模型 SSE 流提前断开，未收到 message_stop"}
}
