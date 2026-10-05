package llm

import (
	"context"
	"errors"
)

type StreamErrorKind string

const (
	StreamErrorTransport StreamErrorKind = "transport"
	StreamErrorTimeout   StreamErrorKind = "timeout"
	StreamErrorCancelled StreamErrorKind = "cancelled"
	StreamErrorProtocol  StreamErrorKind = "protocol"
	StreamErrorProvider  StreamErrorKind = "provider"
	StreamErrorUnknown   StreamErrorKind = "unknown"
)

// StreamError is the sanitized terminal error carried by StreamEnd.
type StreamError struct {
	Kind      StreamErrorKind
	Message   string
	Retryable bool
}

func (e *StreamError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// ErrorStage identifies the provider boundary that stopped a request.
type ErrorStage string

const (
	ErrorStageTimeout          ErrorStage = "provider_timeout"
	ErrorStageTransport        ErrorStage = "provider_transport"
	ErrorStageHTTP             ErrorStage = "provider_http"
	ErrorStageNonSSE           ErrorStage = "provider_non_sse"
	ErrorStageSSEInvalidJSON   ErrorStage = "provider_sse_invalid_json"
	ErrorStageSSEServerError   ErrorStage = "provider_sse_server_error"
	ErrorStageSSEEventTooLarge ErrorStage = "provider_sse_event_too_large"
	ErrorStageSSELineTooLarge  ErrorStage = "provider_sse_line_too_large"
	ErrorStageSSERead          ErrorStage = "provider_sse_read"
	ErrorStageSSEDisconnected  ErrorStage = "provider_sse_disconnected"
)

// ProviderError keeps a safe user-facing message separate from its cause.
type ProviderError struct {
	Stage   ErrorStage
	Message string
	Cause   error
}

func (e *ProviderError) Error() string { return e.Message }

func (e *ProviderError) Unwrap() error { return e.Cause }

// ErrorStageOf returns an empty string for errors that did not come from a provider.
func ErrorStageOf(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return string(providerErr.Stage)
	}
	return ""
}

func StreamErrorFrom(err error, cancelled bool) *StreamError {
	if cancelled {
		return &StreamError{Kind: StreamErrorCancelled, Message: "模型流已取消"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &StreamError{Kind: StreamErrorTimeout, Message: "模型流等待超时", Retryable: true}
	}
	if errors.Is(err, context.Canceled) {
		return &StreamError{Kind: StreamErrorCancelled, Message: "模型流已取消"}
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		if providerErr.Stage == ErrorStageTimeout {
			return &StreamError{Kind: StreamErrorTimeout, Message: providerErr.Message, Retryable: true}
		}
		retryable := providerErr.Stage == ErrorStageTimeout || providerErr.Stage == ErrorStageTransport || providerErr.Stage == ErrorStageSSERead || providerErr.Stage == ErrorStageSSEDisconnected
		kind := StreamErrorProvider
		switch providerErr.Stage {
		case ErrorStageNonSSE, ErrorStageSSEInvalidJSON, ErrorStageSSEEventTooLarge, ErrorStageSSELineTooLarge:
			kind = StreamErrorProtocol
		}
		if retryable {
			kind = StreamErrorTransport
		}
		return &StreamError{Kind: kind, Message: providerErr.Message, Retryable: retryable}
	}
	return &StreamError{Kind: StreamErrorUnknown, Message: "模型流执行失败"}
}
