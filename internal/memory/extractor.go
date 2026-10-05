package memory

import (
	"regexp"
	"strings"
)

var extractedPathPattern = regexp.MustCompile(`(?:[A-Za-z]:[\\/]|/)[^\s]+`)

// ExtractCandidates only accepts explicit preference/correction language from
// the user's turn. It deliberately does not inspect assistant or tool output.
func ExtractCandidates(text string) []Item {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	patterns := []struct {
		kind Kind
		re   *regexp.Regexp
	}{
		{KindExperience, regexp.MustCompile(`(?i)^(?:以后|今后|后续|默认|请始终|请一直|我偏好|我习惯|我喜欢|from now on|always|i prefer)\s*[:：,，]?\s*(.+)$`)},
		{KindExperience, regexp.MustCompile(`(?i)^(?:更正|纠正|修正|不要再|以后不要|请不要|不再|correction|don't)\s*[:：,，]?\s*(.+)$`)},
	}
	var candidates []Item
	for _, pattern := range patterns {
		match := pattern.re.FindStringSubmatch(text)
		if len(match) != 2 {
			continue
		}
		value := strings.TrimSpace(match[1])
		if value == "" {
			continue
		}
		if extractedPathPattern.MatchString(value) {
			continue
		}
		// Reuse Store's validation so automatic extraction has the same safety
		// ceiling as explicit memory writes.
		if err := (&Store{}).Remember(pattern.kind, value, "auto:user_turn"); err != nil {
			continue
		}
		candidates = append(candidates, Item{Kind: pattern.kind, Text: value, Source: "auto:user_turn", Status: "candidate"})
		break
	}
	return candidates
}
