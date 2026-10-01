package app

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerFrame(index int) string {
	return spinnerFrames[index%len(spinnerFrames)]
}

func formatActivityElapsed(elapsed time.Duration) string {
	seconds := int(elapsed / time.Second)
	return fmt.Sprintf("%ds", seconds)
}

type chatActivity struct {
	out     io.Writer
	mu      sync.Mutex
	started time.Time
	stop    chan struct{}
	done    chan struct{}
	running bool
}

func newChatActivity(out io.Writer) *chatActivity {
	return &chatActivity{out: out}
}

func (a *chatActivity) Start() {
	if a == nil || !chatUsesColor(a.out) {
		return
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	a.started = time.Now()
	a.stop = make(chan struct{})
	a.done = make(chan struct{})
	stop, done := a.stop, a.done
	a.mu.Unlock()
	a.render(0)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		defer close(done)
		index := 1
		for {
			select {
			case <-ticker.C:
				a.render(index)
				index++
			case <-stop:
				return
			}
		}
	}()
}

func (a *chatActivity) Stop() {
	if a == nil || !chatUsesColor(a.out) {
		return
	}
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	stop, done := a.stop, a.done
	a.running = false
	a.mu.Unlock()
	close(stop)
	<-done
	a.mu.Lock()
	_, _ = io.WriteString(a.out, "\r\x1b[K")
	a.mu.Unlock()
}

func (a *chatActivity) render(index int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.running {
		return
	}
	text := spinnerFrame(index) + " Thinking...  (" + formatActivityElapsed(time.Since(a.started)) + ")"
	_, _ = io.WriteString(a.out, "\r"+chatMuted(a.out)+text+chatReset(a.out)+"\x1b[K")
}
