package app

import (
	"context"
	"os"
	"sync"
)

// interruptCoordinator 将 Ctrl+C 路由到当前轮；没有活动轮次时结束 chat 生命周期。
type interruptCoordinator struct {
	mu         sync.Mutex
	interrupts <-chan os.Signal
	chatCancel context.CancelFunc
	turnCancel context.CancelFunc
	stop       chan struct{}
}

func newInterruptCoordinator(interrupts <-chan os.Signal, chatCancel context.CancelFunc) *interruptCoordinator {
	return &interruptCoordinator{interrupts: interrupts, chatCancel: chatCancel, stop: make(chan struct{})}
}

func (c *interruptCoordinator) start() func() {
	if c == nil || c.interrupts == nil {
		return func() {}
	}
	go func() {
		for {
			select {
			case <-c.interrupts:
				c.mu.Lock()
				cancel := c.turnCancel
				if cancel == nil {
					cancel = c.chatCancel
				}
				c.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			case <-c.stop:
				return
			}
		}
	}()
	return func() { close(c.stop) }
}

func (c *interruptCoordinator) beginTurn(cancel context.CancelFunc) func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	c.turnCancel = cancel
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.turnCancel = nil
		c.mu.Unlock()
	}
}
