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
	"strings"
	"time"

	"gitee.com/island0920/drift/internal/llm"
)

type Client struct {
	endpoint string
	key      string
	http     *http.Client
}

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

func (c *Client) Stream(ctx context.Context, input llm.Request, emit func(string) error) error {
	body, err := json.Marshal(struct {
		llm.Request
		Stream bool `json:"stream"`
	}{input, true})
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
		return errors.New("模型连接失败或超时，请检查地址、网络和服务状态")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Provider bodies may contain credentials or prompts; never echo them.
		return fmt.Errorf("模型请求失败：HTTP %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "text/event-stream" {
		return errors.New("模型未返回 SSE 流，请检查 API 地址及流式支持")
	}
	err = readStream(resp.Body, emit)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func readStream(r io.Reader, emit func(string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	size := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				value = strings.TrimPrefix(value, " ")
				size += len(value)
				if size > 1<<20 {
					return errors.New("模型流事件超过 1 MiB 限制")
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
			return nil
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			return errors.New("模型流包含无效 JSON")
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return errors.New("模型流返回服务端错误")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Content != "" {
				if err := emit(choice.Delta.Content); err != nil {
					return err
				}
			}
			if choice.FinishReason != "" && choice.FinishReason != "stop" {
				return errors.New("模型未正常完成文本回复（长度限制、内容过滤或不支持的工具调用）")
			}
		}
	}
	if scanner.Err() != nil {
		return errors.New("读取模型流失败或事件过大")
	}
	return errors.New("模型流提前断开，未收到 [DONE]")
}
