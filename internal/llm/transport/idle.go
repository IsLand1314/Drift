package transport

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrIdleTimeout = errors.New("llm transport: idle timeout")

// IdleTimeoutReader bounds the time spent waiting for one underlying read.
// Closing the underlying body on timeout releases HTTP connections and avoids
// leaving a blocked reader goroutine behind.
type IdleTimeoutReader struct {
	ctx     context.Context
	r       io.ReadCloser
	timeout time.Duration
}

func NewIdleTimeoutReader(ctx context.Context, r io.ReadCloser, timeout time.Duration) *IdleTimeoutReader {
	if ctx == nil {
		ctx = context.Background()
	}
	return &IdleTimeoutReader{ctx: ctx, r: r, timeout: timeout}
}

func (r *IdleTimeoutReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := r.r.Read(p)
		ch <- result{n: n, err: err}
	}()
	if r.timeout <= 0 {
		select {
		case value := <-ch:
			return value.n, value.err
		case <-r.ctx.Done():
			_ = r.r.Close()
			return 0, r.ctx.Err()
		}
	}
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case value := <-ch:
		return value.n, value.err
	case <-r.ctx.Done():
		_ = r.r.Close()
		return 0, r.ctx.Err()
	case <-timer.C:
		_ = r.r.Close()
		return 0, ErrIdleTimeout
	}
}

func (r *IdleTimeoutReader) Close() error { return r.r.Close() }
