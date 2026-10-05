// Package codex adapts the official Codex app-server stdio JSON-RPC protocol.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/IsLand1314/Drift/internal/llm"
)

type Client struct {
	command   string
	args      []string
	codexHome string
	nextID    atomic.Int64
}

func (c *Client) StreamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	return llm.WithTerminal(ctx, emit, func(emit func(llm.Event) error) error {
		emitted := false
		forward := func(event llm.Event) error {
			emitted = true
			return emit(event)
		}
		err := c.streamEvents(ctx, request, forward)
		if err != nil && !emitted && isCodexAuthFailure(err) {
			// Restarting app-server gives the Codex CLI one opportunity to reload
			// its refreshed login state. Never retry after model output began.
			return c.streamEvents(ctx, request, emit)
		}
		return err
	})
}

func (c *Client) Capabilities() llm.Capabilities { return llm.Capabilities{} }

func New(codexHome string) (*Client, error) {
	command := "codex"
	if runtime.GOOS == "windows" {
		command = "codex.cmd"
	}
	return NewWithCommand(command, []string{"app-server", "--listen", "stdio://"}, codexHome)
}

func NewWithCommand(command string, args []string, codexHome string) (*Client, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("codex command is empty")
	}
	return &Client{command: command, args: append([]string(nil), args...), codexHome: codexHome}, nil
}

type wireMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func (c *Client) streamEvents(ctx context.Context, request llm.Request, emit func(llm.Event) error) error {
	if len(request.Tools) != 0 {
		return errors.New("Codex Provider 当前仅支持 text-only 请求，暂不接管 Drift 工具")
	}
	cmd := exec.Command(c.command, c.args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if c.codexHome != "" {
		cmd.Env = append(os.Environ(), "CODEX_HOME="+filepath.Clean(c.codexHome))
	}
	if err := cmd.Start(); err != nil {
		return &llm.ProviderError{Stage: llm.ErrorStageTransport, Message: "Codex app-server 启动失败", Cause: err}
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	stop := func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }
	defer stop()
	lines := make(chan wireMessage, 16)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var msg wireMessage
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				readErr <- err
				return
			}
			lines <- msg
		}
		readErr <- scanner.Err()
	}()
	send := func(method string, params any) (int64, error) {
		id := c.nextID.Add(1)
		payload, err := json.Marshal(map[string]any{"method": method, "id": id, "params": params})
		if err != nil {
			return 0, err
		}
		if _, err := io.WriteString(stdin, string(payload)+"\n"); err != nil {
			return 0, err
		}
		return id, nil
	}
	initializeID, err := send("initialize", map[string]any{"clientInfo": map[string]string{"name": "drift", "title": "Drift", "version": "m5.2"}})
	if err != nil {
		return err
	}
	if _, err := waitResponse(ctx, lines, initializeID); err != nil {
		return err
	}
	if _, err := io.WriteString(stdin, `{"method":"initialized","params":{}}`+"\n"); err != nil {
		return err
	}
	system, input := splitMessages(request.Messages)
	if system != "" {
		system += "\n\nYou are the model backend for Drift. Answer as Drift's assistant; do not claim that you are Codex."
	}
	threadParams := map[string]any{"model": request.Model, "ephemeral": true}
	if system != "" {
		threadParams["developerInstructions"] = system
	}
	threadStartID, err := send("thread/start", threadParams)
	if err != nil {
		return err
	}
	threadResp, err := waitResponse(ctx, lines, threadStartID)
	if err != nil {
		return err
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadResp.Result, &thread); err != nil || thread.Thread.ID == "" {
		return errors.New("Codex app-server 未返回 thread id")
	}
	turnStartID, err := send("turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []map[string]string{{"type": "text", "text": input}}})
	if err != nil {
		return err
	}
	turnResp, err := waitResponse(ctx, lines, turnStartID)
	if err != nil {
		return err
	}
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(turnResp.Result, &turn)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-lines:
			if msg.Method == "item/agentMessage/delta" {
				var p struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(msg.Params, &p) == nil {
					if err := emit(llm.TextDelta{Text: p.Delta}); err != nil {
						return err
					}
				}
			} else if msg.Method == "turn/completed" {
				return emit(llm.StreamEnd{Status: llm.StreamCompleted, FinishReason: "stop"})
			}
		case err := <-readErr:
			if err != nil {
				return fmt.Errorf("Codex app-server 连接失败: %w", err)
			}
			return errors.New("Codex app-server 提前退出")
		}
	}
}

func waitResponse(ctx context.Context, lines <-chan wireMessage, id int64) (wireMessage, error) {
	for {
		select {
		case <-ctx.Done():
			return wireMessage{}, ctx.Err()
		case msg := <-lines:
			if msg.ID != nil && *msg.ID == id {
				if len(msg.Error) > 0 && string(msg.Error) != "null" {
					var failure struct {
						Code    json.RawMessage `json:"code"`
						Message string          `json:"message"`
					}
					_ = json.Unmarshal(msg.Error, &failure)
					message := strings.ToLower(failure.Message)
					if strings.Contains(message, "401") || strings.Contains(message, "unauthoriz") || strings.Contains(message, "token") {
						return wireMessage{}, &llm.ProviderError{Stage: llm.ErrorStageHTTP, Message: "Codex 登录态无效"}
					}
					return wireMessage{}, errors.New("Codex app-server 请求失败")
				}
				return msg, nil
			}
		}
	}
}

func isCodexAuthFailure(err error) bool {
	var providerErr *llm.ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Stage == llm.ErrorStageHTTP && providerErr.Message == "Codex 登录态无效"
	}
	return false
}

func splitMessages(messages []llm.Message) (string, string) {
	var system, input []string
	for _, m := range messages {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		if m.Role == "system" {
			system = append(system, m.Content)
		} else {
			input = append(input, m.Content)
		}
	}
	return strings.Join(system, "\n\n"), strings.Join(input, "\n\n")
}
