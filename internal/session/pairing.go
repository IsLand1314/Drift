package session

import "github.com/IsLand1314/Drift/internal/llm"

// NormalizeToolPairing rebuilds an interrupted or partially persisted history
// into the ordering required by providers that pair assistant tool_use blocks
// with immediately following tool results. It never mutates messages.
func NormalizeToolPairing(messages []llm.Message) []llm.Message {
	// First pass: index the first result for every tool call ID. This handles
	// results persisted before their assistant message and lets reconstruction
	// place them next to the owning tool_use.
	results := make(map[string]llm.Message)
	for _, message := range messages {
		if message.Role != "tool" || message.ToolCallID == "" {
			continue
		}
		if _, exists := results[message.ToolCallID]; !exists {
			results[message.ToolCallID] = cloneMessages([]llm.Message{message})[0]
		}
	}

	result := make([]llm.Message, 0, len(messages))
	seenCalls := make(map[string]bool)
	for _, message := range messages {
		switch message.Role {
		case "tool":
			// Every tool message is re-emitted by the assistant owner below.
			// This drops orphan and duplicate results in one place.
			continue
		case "assistant":
			if isEmptyMessage(message) {
				continue
			}
			owner := cloneMessages([]llm.Message{message})[0]
			result = append(result, owner)
			for _, call := range owner.ToolCalls {
				if call.ID == "" {
					continue
				}
				if seenCalls[call.ID] {
					result = append(result, interruptedToolResult(call.ID))
					continue
				}
				seenCalls[call.ID] = true
				if paired, ok := results[call.ID]; ok {
					result = append(result, paired)
				} else {
					result = append(result, interruptedToolResult(call.ID))
				}
			}
		default:
			if !isEmptyMessage(message) {
				result = append(result, cloneMessages([]llm.Message{message})[0])
			}
		}
	}
	return result
}

func interruptedToolResult(id string) llm.Message {
	return llm.Message{Role: "tool", ToolCallID: id, Content: "tool execution interrupted"}
}

func isEmptyMessage(message llm.Message) bool {
	return message.Role == "" && message.Content == "" && message.ToolCallID == "" && message.ReasoningContent == "" && message.ReasoningSignature == "" && message.EncryptedReasoning == "" && len(message.ToolCalls) == 0
}
