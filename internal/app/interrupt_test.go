package app

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestInterruptCoordinatorCancelsTurnBeforeChat(t *testing.T) {
	signals := make(chan os.Signal, 2)
	chatCtx, cancelChat := context.WithCancel(context.Background())
	coordinator := newInterruptCoordinator(signals, cancelChat)
	stop := coordinator.start()
	defer stop()

	turnCtx, cancelTurn := context.WithCancel(context.Background())
	endTurn := coordinator.beginTurn(cancelTurn)
	signals <- os.Interrupt
	waitDone(t, turnCtx)
	if chatCtx.Err() != nil {
		t.Fatal("first interrupt canceled chat context")
	}
	endTurn()

	signals <- os.Interrupt
	waitDone(t, chatCtx)
}

func waitDone(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
}
