package tool

import (
	"context"
	"strings"
	"testing"
)

func TestPlanModeToolsAreAvailableAndSideEffectFree(t *testing.T) {
	registry := NewChatRegistry()
	search, ok := registry.Lookup("ToolSearch")
	if !ok {
		t.Fatal("ToolSearch is not enabled")
	}
	if _, err := search.Execute(context.Background(), t.TempDir(), `{"query":"plan","load":["EnterPlanMode","ExitPlanMode"]}`); err != nil {
		t.Fatalf("load plan tools: %v", err)
	}
	for _, name := range []string{"EnterPlanMode", "ExitPlanMode"} {
		plan, ok := registry.Lookup(name)
		if !ok {
			t.Fatalf("%s is not enabled", name)
		}
		previewable, ok := plan.(Previewable)
		if !ok {
			t.Fatalf("%s must use preview flow", name)
		}
		preview, err := previewable.Preview(context.Background(), t.TempDir(), `{}`)
		if err != nil {
			t.Fatalf("%s preview: %v", name, err)
		}
		result, err := previewable.ExecutePreview(context.Background(), t.TempDir(), preview)
		if err != nil || !strings.Contains(result, "Plan mode") {
			t.Fatalf("%s result=%q err=%v", name, result, err)
		}
	}
}
