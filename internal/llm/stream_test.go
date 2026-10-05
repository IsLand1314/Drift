package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWithTerminalSynthesizesFailedEndWhenProviderStopsEarly(t *testing.T) {
	var events []Event
	err := WithTerminal(context.Background(), func(event Event) error {
		events = append(events, event)
		return nil
	}, func(emit func(Event) error) error {
		return emit(TextDelta{Text: "partial"})
	})
	if err == nil {
		t.Fatal("WithTerminal() error = nil, want protocol error")
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v, want text and terminal", events)
	}
	end, ok := events[1].(StreamEnd)
	if !ok || end.Status != StreamFailed || end.Error == nil || end.Error.Kind != StreamErrorProtocol {
		t.Fatalf("terminal = %#v, want failed protocol terminal", events[1])
	}
}

func TestWithTerminalMarksCancellation(t *testing.T) {
	var end StreamEnd
	err := WithTerminal(context.Background(), func(event Event) error {
		if value, ok := event.(StreamEnd); ok {
			end = value
		}
		return nil
	}, func(func(Event) error) error {
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) || end.Status != StreamCancelled || end.Error == nil || end.Error.Kind != StreamErrorCancelled {
		t.Fatalf("err=%v terminal=%#v", err, end)
	}
}

func TestWithTerminalUsesCancelledContextWhenProviderReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var end StreamEnd
	err := WithTerminal(ctx, func(event Event) error {
		if value, ok := event.(StreamEnd); ok {
			end = value
		}
		return nil
	}, func(func(Event) error) error { return nil })
	if !errors.Is(err, context.Canceled) || end.Status != StreamCancelled || end.Error == nil || end.Error.Kind != StreamErrorCancelled {
		t.Fatalf("err=%v terminal=%#v", err, end)
	}
}

func TestWithTerminalMarksProviderTimeout(t *testing.T) {
	var end StreamEnd
	err := WithTerminal(context.Background(), func(event Event) error {
		if value, ok := event.(StreamEnd); ok {
			end = value
		}
		return nil
	}, func(func(Event) error) error {
		return &ProviderError{Stage: ErrorStageTimeout, Message: "idle timeout"}
	})
	if err == nil || end.Status != StreamFailed || end.Error == nil || end.Error.Kind != StreamErrorTimeout || !end.Error.Retryable {
		t.Fatalf("err=%v terminal=%#v", err, end)
	}
}

func TestWithTerminalRejectsEventsAfterEnd(t *testing.T) {
	err := WithTerminal(context.Background(), func(Event) error { return nil }, func(emit func(Event) error) error {
		if err := emit(StreamEnd{Status: StreamCompleted}); err != nil {
			return err
		}
		return emit(TextDelta{Text: "late"})
	})
	if err == nil || !strings.Contains(err.Error(), "after StreamEnd") {
		t.Fatalf("WithTerminal() error = %v, want post-terminal protocol error", err)
	}
}

func TestWithTerminalDoesNotDuplicateTerminalWhenProviderReturnsAfterEnd(t *testing.T) {
	var terminals int
	errProvider := errors.New("provider closed after completion")
	err := WithTerminal(context.Background(), func(event Event) error {
		if _, ok := event.(StreamEnd); ok {
			terminals++
		}
		return nil
	}, func(emit func(Event) error) error {
		if err := emit(StreamEnd{Status: StreamCompleted}); err != nil {
			return err
		}
		return errProvider
	})
	if !errors.Is(err, errProvider) || terminals != 1 {
		t.Fatalf("err=%v terminals=%d, want provider error and one terminal", err, terminals)
	}
}
