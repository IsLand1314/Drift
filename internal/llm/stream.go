package llm

import (
	"context"
	"errors"
)

// WithTerminal guarantees exactly one terminal StreamEnd for every started
// provider stream. Provider parsers remain focused on protocol events; this
// wrapper owns cancellation, failure and premature-end semantics.
func WithTerminal(ctx context.Context, emit func(Event) error, run func(func(Event) error) error) error {
	ended := false
	forward := func(event Event) error {
		if end, ok := event.(StreamEnd); ok {
			if ended {
				return errors.New("llm: duplicate StreamEnd")
			}
			ended = true
			if end.Status == "" {
				end.Status = StreamCompleted
			}
			return emit(end)
		}
		if ended {
			return errors.New("llm: event received after StreamEnd")
		}
		return emit(event)
	}

	err := run(forward)
	if ended {
		return err
	}
	status := StreamFailed
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	streamErr := StreamErrorFrom(err, errors.Is(err, context.Canceled))
	if errors.Is(err, context.Canceled) {
		status = StreamCancelled
	}
	if err == nil {
		streamErr = &StreamError{Kind: StreamErrorProtocol, Message: "模型流未发送结束事件"}
		err = errors.New(streamErr.Message)
	}
	_ = emit(StreamEnd{Status: status, Error: streamErr})
	return err
}
