package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
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

type Client struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
	closed bool
}

func Start(ctx context.Context, server Server, env []string) (*Client, error) {
	if server.Transport != "stdio" || server.Command == "" {
		return nil, errors.New("mcp: invalid stdio server")
	}
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Env = append([]string(nil), env...)
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
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
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

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) request(ctx context.Context, method string, params any, result any) error {
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
		_ = c.cmd.Process.Kill()
		return ctx.Err()
	case err := <-readErr:
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
