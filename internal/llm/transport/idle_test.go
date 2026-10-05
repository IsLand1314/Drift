package transport

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type blockingReadCloser struct{ released chan struct{} }

func (r *blockingReadCloser) Read([]byte) (int, error) { <-r.released; return 0, io.EOF }
func (r *blockingReadCloser) Close() error {
	select {
	case <-r.released:
	default:
		close(r.released)
	}
	return nil
}

func TestIdleTimeoutReaderClosesBlockedReader(t *testing.T) {
	raw := &blockingReadCloser{released: make(chan struct{})}
	r := NewIdleTimeoutReader(context.Background(), raw, 10*time.Millisecond)
	_, err := r.Read(make([]byte, 1))
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("Read() error = %v, want idle timeout", err)
	}
	select {
	case <-raw.released:
	case <-time.After(time.Second):
		t.Fatal("underlying reader was not closed")
	}
}
