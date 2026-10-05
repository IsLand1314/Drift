package session

import "github.com/IsLand1314/Drift/internal/llm"

// Manager owns the mutable model-context snapshot used by one Agent run.
// Store remains responsible for persistence and recovery.
type Manager struct{ messages []llm.Message }

func NewManager(messages []llm.Message) *Manager {
	m := &Manager{}
	m.Restore(messages)
	return m
}

func (m *Manager) Append(message llm.Message) {
	m.messages = append(m.messages, cloneMessages([]llm.Message{message})[0])
}

func (m *Manager) Messages() []llm.Message { return cloneMessages(m.messages) }

func (m *Manager) Restore(messages []llm.Message) { m.messages = cloneMessages(messages) }

func (m *Manager) ContextBytes() int {
	total := 0
	for _, message := range m.messages {
		total += len(message.Role) + len(message.Content) + len(message.ToolCallID) + len(message.ReasoningContent) + len(message.ReasoningSignature) + len(message.EncryptedReasoning)
		for _, call := range message.ToolCalls {
			total += len(call.ID) + len(call.Type) + len(call.Name) + len(call.Arguments)
		}
	}
	return total
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	copy(cloned, messages)
	for i := range cloned {
		cloned[i].ToolCalls = append([]llm.ToolCall(nil), messages[i].ToolCalls...)
	}
	return cloned
}
