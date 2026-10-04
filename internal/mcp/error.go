package mcp

import "fmt"

// ConnectionError keeps retry diagnostics structured without exposing
// credentials or raw request bodies.
type ConnectionError struct {
	Transport string
	Phase     string
	Attempt   int
	Err       error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("mcp %s %s failed on attempt %d: %v", e.Transport, e.Phase, e.Attempt, e.Err)
}

func (e *ConnectionError) Unwrap() error { return e.Err }
