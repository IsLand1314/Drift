package tool

import (
	"encoding/json"
	"testing"
)

func TestPublicToolNames(t *testing.T) {
	readOnly := NewDefaultRegistry()
	for _, name := range []string{"Glob", "Grep", "ReadFile"} {
		if _, ok := readOnly.Lookup(name); !ok {
			t.Fatalf("read-only registry missing %q", name)
		}
	}
	for _, old := range []string{"list_files", "search_text", "read_file", "write_file", "edit_file", "delete_file", "run_command"} {
		if _, ok := readOnly.Lookup(old); ok {
			t.Fatalf("read-only registry still exposes legacy name %q", old)
		}
	}

	chat := NewChatRegistry()
	for _, name := range []string{"AskUserQuestion", "ToolSearch"} {
		if _, ok := chat.Lookup(name); !ok {
			t.Fatalf("chat registry missing %q", name)
		}
	}
	for _, old := range []string{"list_files", "search_text", "read_file", "write_file", "edit_file", "delete_file", "run_command"} {
		if _, ok := chat.Lookup(old); ok {
			t.Fatalf("chat registry still exposes legacy name %q", old)
		}
	}
}

func TestDefinitionsStartWithControlTools(t *testing.T) {
	want := []string{"AskUserQuestion", "ToolSearch"}
	definitions := NewChatRegistry().Definitions()
	if len(definitions) != len(want) {
		t.Fatalf("definition count = %d, want %d", len(definitions), len(want))
	}
	for i, definition := range definitions {
		var function struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(definition.Function, &function); err != nil {
			t.Fatalf("decode tool definition: %v", err)
		}
		if function.Name != want[i] {
			t.Fatalf("definition[%d] = %q, want %q", i, function.Name, want[i])
		}
	}
}
