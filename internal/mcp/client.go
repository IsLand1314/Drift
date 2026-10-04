package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

const maxMessageBytes = 64 << 10
const maxResultBytes = 16 << 10

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Result struct{ Text string }

type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

func FormatContent(blocks []ContentBlock) (string, error) {
	var output strings.Builder
	for _, block := range blocks {
		var text string
		switch block.Type {
		case "text":
			text = block.Text
		case "resource":
			text = block.URI
		case "image", "audio":
			text = "[" + block.Type + " content]"
		default:
			continue
		}
		if output.Len()+len(text) > maxResultBytes {
			return "", errors.New("mcp: content exceeds limit")
		}
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(text)
	}
	return output.String(), nil
}

type Client struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	nextID     int
	closed     bool
	httpClient *http.Client
	endpoint   string
	headers    map[string]string
}

func Start(ctx context.Context, server Server, env []string) (*Client, error) {
	if server.Transport == "http" || server.Transport == "streamable-http" {
		return startHTTP(ctx, server)
	}
	if server.Transport != "stdio" || server.Command == "" {
		return nil, errors.New("mcp: invalid stdio server")
	}
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Env = mergeEnv(os.Environ(), env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start server: %w", err)
	}
	client := &Client{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 4096)}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := client.request(ctx, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "Drift", "version": "m3.22"}}, &initialized); err != nil {
		_ = client.Close()
		return nil, err
	}
	if initialized.ProtocolVersion == "" {
		_ = client.Close()
		return nil, errors.New("mcp: server did not negotiate protocol version")
	}
	if err := client.notify("notifications/initialized", map[string]any{}); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func startHTTP(ctx context.Context, server Server) (*Client, error) {
	client := &Client{httpClient: &http.Client{}, endpoint: server.URL, headers: server.Headers}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := client.request(ctx, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "Drift", "version": "m5.14"}}, &initialized); err != nil {
		return nil, err
	}
	if initialized.ProtocolVersion == "" {
		return nil, errors.New("mcp: server did not negotiate protocol version")
	}
	if err := client.notify("notifications/initialized", map[string]any{}); err != nil {
		return nil, err
	}
	return client, nil
}

func mergeEnv(base, overrides []string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if !ok || name == "" || !isRuntimeEnv(name) {
			continue
		}
		key := envKey(name)
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = item
	}
	for _, item := range overrides {
		name, _, ok := strings.Cut(item, "=")
		if !ok || name == "" {
			continue
		}
		key := envKey(name)
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = item
	}
	result := make([]string, 0, len(order))
	for _, key := range order {
		result = append(result, values[key])
	}
	return result
}

func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

func isRuntimeEnv(name string) bool {
	switch strings.ToLower(name) {
	case "path", "systemroot", "windir", "temp", "tmp", "comspec", "pathext", "home", "userprofile":
		return true
	default:
		return false
	}
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var response struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.request(ctx, "tools/list", map[string]any{}, &response); err != nil {
		return nil, err
	}
	for index := range response.Tools {
		item := &response.Tools[index]
		if !identifier.MatchString(item.Name) || len(item.Description) > 1000 || len(item.InputSchema) == 0 || len(item.InputSchema) > maxMessageBytes || !json.Valid(item.InputSchema) {
			return nil, fmt.Errorf("mcp: tool %d is invalid", index+1)
		}
	}
	return response.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	if !identifier.MatchString(name) || len(args) == 0 || len(args) > maxMessageBytes || !json.Valid(args) {
		return Result{}, errors.New("mcp: invalid tool call")
	}
	var response struct {
		Content []ContentBlock `json:"content"`
		IsError bool           `json:"isError"`
	}
	if err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": json.RawMessage(args)}, &response); err != nil {
		return Result{}, err
	}
	var output strings.Builder
	for _, item := range response.Content {
		if item.Type != "text" {
			continue
		}
		if output.Len()+len(item.Text) > maxResultBytes {
			return Result{}, errors.New("mcp: tool result exceeds limit")
		}
		output.WriteString(item.Text)
	}
	if response.IsError {
		return Result{}, errors.New("mcp: tool returned an error")
	}
	return Result{Text: output.String()}, nil
}

func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	var response struct {
		Resources []Resource `json:"resources"`
	}
	if err := c.request(ctx, "resources/list", map[string]any{}, &response); err != nil {
		return nil, err
	}
	return response.Resources, nil
}
func (c *Client) ReadResource(ctx context.Context, uri string) ([]ContentBlock, error) {
	if strings.TrimSpace(uri) == "" || len(uri) > 2048 {
		return nil, errors.New("mcp: invalid resource URI")
	}
	var response struct {
		Contents []ContentBlock `json:"contents"`
	}
	if err := c.request(ctx, "resources/read", map[string]any{"uri": uri}, &response); err != nil {
		return nil, err
	}
	return response.Contents, nil
}
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	var response struct {
		Prompts []Prompt `json:"prompts"`
	}
	if err := c.request(ctx, "prompts/list", map[string]any{}, &response); err != nil {
		return nil, err
	}
	return response.Prompts, nil
}
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) ([]ContentBlock, error) {
	if !identifier.MatchString(name) {
		return nil, errors.New("mcp: invalid prompt name")
	}
	var response struct {
		Messages []ContentBlock `json:"messages"`
	}
	if err := c.request(ctx, "prompts/get", map[string]any{"name": name, "arguments": args}, &response); err != nil {
		return nil, err
	}
	return response.Messages, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	return c.terminateLocked()
}

func (c *Client) terminateLocked() error {
	c.closed = true
	if c.httpClient != nil {
		return nil
	}
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

func (c *Client) notify(method string, params any) error {
	if c.httpClient != nil {
		return c.httpNotify(context.Background(), method, params)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) request(ctx context.Context, method string, params any, result any) error {
	if c.httpClient != nil {
		return c.httpRequest(ctx, method, params, result)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("mcp: client is closed")
	}
	c.nextID++
	id := c.nextID
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	line := make(chan []byte, 1)
	readErr := make(chan error, 1)
	go func() {
		value, err := readLine(c.stdout)
		if err != nil {
			readErr <- err
			return
		}
		line <- value
	}()
	select {
	case <-ctx.Done():
		_ = c.terminateLocked()
		return ctx.Err()
	case err := <-readErr:
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = c.terminateLocked()
			return ctxErr
		}
		_ = c.terminateLocked()
		return fmt.Errorf("mcp: read response: %w", err)
	case raw := <-line:
		var response struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("mcp: decode response: %w", err)
		}
		if response.JSONRPC != "2.0" || response.ID != id {
			return errors.New("mcp: invalid response id")
		}
		if response.Error != nil {
			return fmt.Errorf("mcp: server error: %s", response.Error.Message)
		}
		if len(response.Result) == 0 {
			return errors.New("mcp: response is missing result")
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("mcp: decode result: %w", err)
		}
		return nil
	}
}

func (c *Client) httpNotify(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp: http status %s", resp.Status)
	}
	return nil
}

func (c *Client) httpRequest(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("mcp: client is closed")
	}
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp: http status %s", resp.Status)
	}
	raw, err := readHTTPResponse(resp.Body, resp.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	return decodeResponse(raw, id, result)
}

func readHTTPResponse(body io.Reader, contentType string) ([]byte, error) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				return []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), nil
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("mcp: empty SSE response")
	}
	return io.ReadAll(io.LimitReader(body, maxMessageBytes+1))
}

func decodeResponse(raw []byte, id int, result any) error {
	if len(raw) > maxMessageBytes {
		return errors.New("mcp: message exceeds limit")
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("mcp: decode response: %w", err)
	}
	if response.JSONRPC != "2.0" || response.ID != id {
		return errors.New("mcp: invalid response id")
	}
	if response.Error != nil {
		return fmt.Errorf("mcp: server error: %s", response.Error.Message)
	}
	if len(response.Result) == 0 {
		return errors.New("mcp: response is missing result")
	}
	return json.Unmarshal(response.Result, result)
}

func (c *Client) write(message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("mcp: encode request: %w", err)
	}
	if len(raw) > maxMessageBytes {
		return errors.New("mcp: request exceeds limit")
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("mcp: write request: %w", err)
	}
	return nil
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > maxMessageBytes {
		return nil, errors.New("message exceeds limit")
	}
	return line, nil
}
