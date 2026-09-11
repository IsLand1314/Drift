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
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/IsLand1314/Drift/internal/llm"
)

type Client struct {
	endpoint string
	key      string
	http     *http.Client
}

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

// Stream 把 llm.Request 序列化为 OpenAI Chat Completions SSE 请求，
// 再由 readStream 聚合成一次完整的 llm.Completion。
func (c *Client) Stream(ctx context.Context, input llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	body, err := json.Marshal(request{Model: input.Model, Messages: openAIMessages(input.Messages), Tools: input.Tools, Stream: true})
	if err != nil {
		return llm.Completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return llm.Completion{}, errors.New("无法创建模型请求")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return llm.Completion{}, ctx.Err()
		}
		return llm.Completion{}, errors.New("模型连接失败或超时，请检查地址、网络和服务状态")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Provider bodies may contain credentials or prompts; never echo them.
		return llm.Completion{}, fmt.Errorf("模型请求失败：HTTP %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "text/event-stream" {
		return llm.Completion{}, errors.New("模型未返回 SSE 流，请检查 API 地址及流式支持")
	}
	completion, err := readStream(resp.Body, emit)
	if ctx.Err() != nil {
		return llm.Completion{}, ctx.Err()
	}
	return completion, err
}

type request struct {
	Model    string               `json:"model"`
	Messages []message            `json:"messages"`
	Tools    []llm.ToolDefinition `json:"tools,omitempty"`
	Stream   bool                 `json:"stream"`
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

func readStream(r io.Reader, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	// SSE 以空行分隔事件；工具参数可能跨多个 delta，需要按 index 聚合。
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	size := 0
	completion := llm.Completion{Assistant: llm.Message{Role: "assistant"}}
	calls := make(map[int]*llm.ToolCall)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				value = strings.TrimPrefix(value, " ")
				size += len(value)
				if size > 1<<20 {
					return llm.Completion{}, errors.New("模型流事件超过 1 MiB 限制")
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
			indexes := make([]int, 0, len(calls))
			for index := range calls {
				indexes = append(indexes, index)
			}
			sort.Ints(indexes)
			for _, index := range indexes {
				completion.Assistant.ToolCalls = append(completion.Assistant.ToolCalls, *calls[index])
			}
			return completion, nil
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
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
			return llm.Completion{}, errors.New("模型流包含无效 JSON")
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return llm.Completion{}, errors.New("模型流返回服务端错误")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Role != "" {
				completion.Assistant.Role = choice.Delta.Role
			}
			if choice.Delta.Content != "" {
				completion.Assistant.Content += choice.Delta.Content
				if err := emit(llm.StreamEvent{Text: choice.Delta.Content}); err != nil {
					return llm.Completion{}, err
				}
			}
			if choice.Delta.ReasoningContent != "" {
				completion.Assistant.ReasoningContent += choice.Delta.ReasoningContent
				if err := emit(llm.StreamEvent{ReasoningContent: choice.Delta.ReasoningContent}); err != nil {
					return llm.Completion{}, err
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				call := calls[delta.Index]
				if call == nil {
					call = &llm.ToolCall{}
					calls[delta.Index] = call
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
				if err := emit(llm.StreamEvent{ToolCallDelta: &llm.ToolCallDelta{Index: delta.Index, ID: delta.ID, Name: delta.Function.Name, Arguments: delta.Function.Arguments}}); err != nil {
					return llm.Completion{}, err
				}
			}
			if choice.FinishReason != "" {
				completion.FinishReason = choice.FinishReason
			}
		}
	}
	if scanner.Err() != nil {
		return llm.Completion{}, errors.New("读取模型流失败或事件过大")
	}
	return llm.Completion{}, errors.New("模型流提前断开，未收到 [DONE]")
}
