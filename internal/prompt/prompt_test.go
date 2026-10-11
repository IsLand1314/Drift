package prompt

import (
	"strings"
	"testing"
)

func TestBuilderRendersSectionsInStableOrder(t *testing.T) {
	builder := NewBuilder()
	got := builder.Build(Context{
		Platform:             "windows",
		HasToolSearch:        true,
		HasWritableTools:     true,
		HasBash:              true,
		HasPlanTools:         true,
		NativeToolReferences: true,
		Focus:                "README.md",
		ShortTermMemory:      "short-term rule",
		LongTermMemory:       []string{"verified fact"},
		SkillName:            "go-style",
		SkillContent:         "Prefer small functions.",
	})
	for _, want := range []string{
		"Drift is workspace-scoped.",
		`"README.md"`,
		"Windows",
		"native deferred tool references",
		"EnterPlanMode and ExitPlanMode",
		"short-term rule",
		"verified fact",
		"Selected Skill",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
	if strings.Index(got, "Selected Skill") < strings.Index(got, "verified fact") {
		t.Fatal("skill section rendered before memory section")
	}
}

func TestBuilderDoesNotRenderCapabilitySectionsWhenDisabled(t *testing.T) {
	got := NewBuilder().Build(Context{})
	for _, forbidden := range []string{"ToolSearch and AskUserQuestion", "EnterPlanMode", "Selected Skill", "Long-term memory"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("prompt unexpectedly contains %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "native read-only tools") {
		t.Fatalf("base read-only policy missing: %q", got)
	}
}
