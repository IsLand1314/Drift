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

func (c *Client) Stream(ctx context.Context, request llm.Request, emit func(llm.StreamEvent) error) (llm.Completion, error) {
	if len(request.Tools) != 0 {
		return llm.Completion{}, errors.New("Codex Provider 当前仅支持 text-only 请求，暂不接管 Drift 工具")
	}
	cmd := exec.Command(c.command, c.args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return llm.Completion{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return llm.Completion{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return llm.Completion{}, err
	}
	if c.codexHome != "" {
		cmd.Env = append(os.Environ(), "CODEX_HOME="+filepath.Clean(c.codexHome))
	}
	if err := cmd.Start(); err != nil {
		return llm.Completion{}, &llm.ProviderError{Stage: llm.ErrorStageTransport, Message: "Codex app-server 启动失败", Cause: err}
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
	send := func(method string, params any) error {
		id := c.nextID.Add(1)
		payload, err := json.Marshal(map[string]any{"method": method, "id": id, "params": params})
		if err != nil {
			return err
		}
		if _, err := io.WriteString(stdin, string(payload)+"\n"); err != nil {
			return err
		}
		return nil
	}
	if err := send("initialize", map[string]any{"clientInfo": map[string]string{"name": "drift", "title": "Drift", "version": "m5.2"}}); err != nil {
		return llm.Completion{}, err
	}
	if _, err := waitResponse(ctx, lines, 1); err != nil {
		return llm.Completion{}, err
	}
	if _, err := io.WriteString(stdin, `{"method":"initialized","params":{}}`+"\n"); err != nil {
		return llm.Completion{}, err
	}
	system, input := splitMessages(request.Messages)
	if system != "" {
		system += "\n\nYou are the model backend for Drift. Answer as Drift's assistant; do not claim that you are Codex."
	}
	threadParams := map[string]any{"model": request.Model, "ephemeral": true}
	if system != "" {
		threadParams["developerInstructions"] = system
	}
	if err := send("thread/start", threadParams); err != nil {
		return llm.Completion{}, err
	}
	threadResp, err := waitResponse(ctx, lines, 2)
	if err != nil {
		return llm.Completion{}, err
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadResp.Result, &thread); err != nil || thread.Thread.ID == "" {
		return llm.Completion{}, errors.New("Codex app-server 未返回 thread id")
	}
	if err := send("turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []map[string]string{{"type": "text", "text": input}}}); err != nil {
		return llm.Completion{}, err
	}
	turnResp, err := waitResponse(ctx, lines, 3)
	if err != nil {
		return llm.Completion{}, err
	}
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(turnResp.Result, &turn)
	completion := llm.Completion{Assistant: llm.Message{Role: "assistant"}, FinishReason: "stop"}
	for {
		select {
		case <-ctx.Done():
			return llm.Completion{}, ctx.Err()
		case msg := <-lines:
			if msg.Method == "item/agentMessage/delta" {
				var p struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(msg.Params, &p) == nil {
					completion.Assistant.Content += p.Delta
					if err := emit(llm.StreamEvent{Text: p.Delta}); err != nil {
						return llm.Completion{}, err
					}
				}
			} else if msg.Method == "turn/completed" {
				return completion, nil
			}
		case err := <-readErr:
			if err != nil {
				return llm.Completion{}, fmt.Errorf("Codex app-server 连接失败: %w", err)
			}
			return llm.Completion{}, errors.New("Codex app-server 提前退出")
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
					return wireMessage{}, errors.New("Codex app-server 请求失败")
				}
				return msg, nil
			}
		}
	}
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
