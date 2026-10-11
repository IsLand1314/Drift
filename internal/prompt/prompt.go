// Package prompt builds the provider-independent system instructions used by
// the Agent runtime. Sections are deliberately kept independent from llm and
// tool packages so prompt policy can evolve without creating import cycles.
package prompt

import "strings"

// Context describes the runtime capabilities and context that may be exposed
// to the model for one request.
type Context struct {
	Platform             string
	Focus                string
	HasToolSearch        bool
	HasWritableTools     bool
	HasBash              bool
	HasPlanTools         bool
	NativeToolReferences bool
	ShortTermMemory      string
	LongTermMemory       []string
	SkillName            string
	SkillContent         string
}

// Section is one independently testable prompt fragment.
type Section interface {
	Name() string
	Enabled(Context) bool
	Render(Context) string
}

// Builder renders sections in a stable, policy-first order.
type Builder struct {
	sections []Section
}

func NewBuilder() Builder {
	return Builder{sections: []Section{
		identitySection{},
		focusSection{},
		platformSection{},
		toolSection{},
		planSection{},
		memorySection{},
		skillSection{},
	}}
}

func (b Builder) Build(ctx Context) string {
	parts := make([]string, 0, len(b.sections))
	for _, section := range b.sections {
		if section.Enabled(ctx) {
			if text := strings.TrimSpace(section.Render(ctx)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
