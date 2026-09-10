// Package llm defines provider-independent streaming messages.
package llm

import "context"

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Client emits text synchronously. Returning an error from emit stops the stream.
type Client interface {
	Stream(context.Context, Request, func(string) error) error
}
