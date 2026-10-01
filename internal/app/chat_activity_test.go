package app

import (
	"testing"
	"time"
)

func TestSpinnerFrameCycles(t *testing.T) {
	want := []string{"⠋", "⠙", "⠹", "⠸"}
	for i, frame := range want {
		if got := spinnerFrame(i); got != frame {
			t.Fatalf("spinnerFrame(%d) = %q, want %q", i, got, frame)
		}
	}
	if got := spinnerFrame(len(spinnerFrames)); got != want[0] {
		t.Fatalf("spinner did not cycle: %q", got)
	}
}

func TestActivityElapsedUsesSeconds(t *testing.T) {
	if got := formatActivityElapsed(1500 * time.Millisecond); got != "1s" {
		t.Fatalf("elapsed = %q, want 1s", got)
	}
}
