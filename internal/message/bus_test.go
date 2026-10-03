package message

import (
	"strings"
	"testing"
	"time"
)

func TestBusPreservesEnvelopeAndDeduplicatesByID(t *testing.T) {
	bus := NewBus()
	when := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	first := Message{ID: "msg-1", From: "child-1", To: "main", TaskID: "task-1", Kind: "progress", Text: "started", Time: when}
	if err := bus.Publish(first); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(Message{ID: "msg-1", From: "child-1", To: "main", TaskID: "task-1", Kind: "progress", Text: "duplicate", Time: when.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	items := bus.List("task-1")
	if len(items) != 1 || items[0].From != "child-1" || items[0].To != "main" || items[0].Time != when {
		t.Fatalf("items=%+v", items)
	}
}

func TestBusSummaryContainsResultsOnce(t *testing.T) {
	bus := NewBus()
	if _, err := bus.Send("child-1", "main", "task-1", "result", "created file"); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send("child-1", "main", "task-1", "issue", "none"); err != nil {
		t.Fatal(err)
	}
	summary := bus.Summary("task-1")
	if strings.Count(summary, "created file") != 1 || !strings.Contains(summary, "child-1 -> main") || !strings.Contains(summary, "task-1") {
		t.Fatalf("summary=%q", summary)
	}
}
