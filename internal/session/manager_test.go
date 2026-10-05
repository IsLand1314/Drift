package session

import (
	"testing"

	"github.com/IsLand1314/Drift/internal/llm"
)

func TestManagerCopiesMessagesAndCountsContext(t *testing.T) {
	m := NewManager([]llm.Message{{Role: "user", Content: "hello"}})
	m.Append(llm.Message{Role: "assistant", Content: "world"})
	got := m.Messages()
	got[0].Content = "changed"
	if m.Messages()[0].Content != "hello" || m.ContextBytes() == 0 {
		t.Fatalf("manager did not isolate snapshot: %+v", m.Messages())
	}
}
