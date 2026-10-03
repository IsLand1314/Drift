package tool

import (
	"context"
	"strings"
	"testing"
)

func TestChatRegistryStartsWithOnlyControlSchemas(t *testing.T) {
	registry := NewChatRegistry()
	definitions := registry.Definitions()
	if len(definitions) != 2 {
		t.Fatalf("initial definitions=%d, want AskUserQuestion and ToolSearch only", len(definitions))
	}
	for _, name := range []string{"AskUserQuestion", "ToolSearch"} {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("initial control tool %q is unavailable", name)
		}
	}
	if _, ok := registry.Lookup("ReadFile"); ok {
		t.Fatal("ReadFile schema loaded before ToolSearch selection")
	}
}

func TestToolSearchLoadsOnlySelectedMatchingSchemas(t *testing.T) {
	registry := NewChatRegistry()
	search, ok := registry.Lookup("ToolSearch")
	if !ok {
		t.Fatal("ToolSearch unavailable")
	}
	result, err := search.Execute(context.Background(), t.TempDir(), `{"query":"file","load":["ReadFile"]}`)
	if err != nil || !strings.Contains(result, "ReadFile") {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if _, ok := registry.Lookup("ReadFile"); !ok {
		t.Fatal("selected ReadFile schema was not loaded")
	}
	if _, ok := registry.Lookup("WriteFile"); ok {
		t.Fatal("unselected WriteFile schema was loaded")
	}
}

func TestToolSearchMatchesMeaningfulWordsInsteadOfOnlyTheWholeQuery(t *testing.T) {
	registry := NewChatRegistry()
	search, _ := registry.Lookup("ToolSearch")
	result, err := search.Execute(context.Background(), t.TempDir(), `{"query":"send progress message from child to main","load":["AgentMessageSend"]}`)
	if err != nil || !strings.Contains(result, "AgentMessageSend") {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if _, ok := registry.Lookup("AgentMessageSend"); !ok {
		t.Fatal("AgentMessageSend schema was not loaded")
	}
}

func TestAskUserQuestionParsesStructuredArguments(t *testing.T) {
	registry := NewChatRegistry()
	questionTool, ok := registry.Lookup("AskUserQuestion")
	if !ok {
		t.Fatal("AskUserQuestion unavailable")
	}
	questionable, ok := questionTool.(Questionable)
	if !ok {
		t.Fatal("AskUserQuestion does not expose structured question parsing")
	}
	question, err := questionable.Question(`{"question":"choose target","options":[{"id":"first","label":"first"},{"id":"second","label":"second"}],"multi_select":true,"allow_free_text":true}`)
	if err != nil || question.Question != "choose target" || len(question.Options) != 2 || !question.MultiSelect || !question.AllowFreeText {
		t.Fatalf("question=%+v err=%v", question, err)
	}
}
