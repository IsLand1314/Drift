// Package message contains the small in-process protocol used by the parent
// Agent and its children. It deliberately does not execute tools or grant
// permissions; it only transports progress and results.
package message

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Message struct {
	ID     string    `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	TaskID string    `json:"task_id"`
	Kind   string    `json:"kind"`
	Text   string    `json:"text"`
	Time   time.Time `json:"time"`
}

type Bus struct {
	mu    sync.Mutex
	next  uint64
	seen  map[string]struct{}
	items []Message
}

func NewBus() *Bus { return &Bus{seen: make(map[string]struct{})} }

func (b *Bus) Publish(item Message) error {
	item.ID = strings.TrimSpace(item.ID)
	item.From = strings.TrimSpace(item.From)
	item.To = strings.TrimSpace(item.To)
	item.TaskID = strings.TrimSpace(item.TaskID)
	item.Kind = strings.TrimSpace(item.Kind)
	item.Text = strings.TrimSpace(item.Text)
	if item.From == "" || item.To == "" || item.TaskID == "" || item.Kind == "" || item.Text == "" {
		return fmt.Errorf("message: sender, receiver, task, kind, and text are required")
	}
	if len([]rune(item.From)) > 80 || len([]rune(item.To)) > 80 || len([]rune(item.TaskID)) > 80 || len([]rune(item.Kind)) > 40 || len([]rune(item.Text)) > 4000 {
		return fmt.Errorf("message: envelope is too large")
	}
	if item.Time.IsZero() {
		item.Time = time.Now().UTC()
	} else {
		item.Time = item.Time.UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen == nil {
		b.seen = make(map[string]struct{})
	}
	if item.ID == "" {
		b.next++
		item.ID = fmt.Sprintf("msg-%d", b.next)
	}
	if _, exists := b.seen[item.ID]; exists {
		return nil
	}
	b.seen[item.ID] = struct{}{}
	b.items = append(b.items, item)
	return nil
}

func (b *Bus) Send(from, to, taskID, kind, text string) (Message, error) {
	b.mu.Lock()
	b.next++
	id := fmt.Sprintf("auto-%d", b.next)
	b.mu.Unlock()
	item := Message{ID: id, From: from, To: to, TaskID: taskID, Kind: kind, Text: text, Time: time.Now().UTC()}
	if err := b.Publish(item); err != nil {
		return Message{}, err
	}
	return item, nil
}

func (b *Bus) List(taskID string) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]Message, 0, len(b.items))
	for _, item := range b.items {
		if taskID == "" || item.TaskID == taskID {
			result = append(result, item)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Time.Equal(result[j].Time) {
			return result[i].ID < result[j].ID
		}
		return result[i].Time.Before(result[j].Time)
	})
	return result
}

func (b *Bus) Summary(taskID string) string {
	items := b.List(taskID)
	if len(items) == 0 {
		return "AgentSummary: no messages"
	}
	var out strings.Builder
	out.WriteString("AgentSummary ")
	out.WriteString(taskID)
	out.WriteString(":\n")
	for _, item := range items {
		fmt.Fprintf(&out, "- %s -> %s [%s] %s\n", item.From, item.To, item.Kind, item.Text)
	}
	return strings.TrimSpace(out.String())
}
