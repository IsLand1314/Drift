package commandpolicy

import "strings"

var readOnlyCommands = map[string]struct{}{
	"pwd":            {},
	"git status":     {},
	"git diff":       {},
	"git log":        {},
	"go version":     {},
	"go env":         {},
	"node -v":        {},
	"node --version": {},
	"npm -v":         {},
	"npm --version":  {},
}

var mutatingPrefixes = []string{
	"go test ",
	"go build ",
	"go install ",
	"go fmt ",
	"git add ",
	"git commit",
	"npm install",
	"npm update",
}

func classifySimpleCommand(command string) Risk {
	normalized := strings.ToLower(strings.Join(strings.Fields(command), " "))
	if _, ok := readOnlyCommands[normalized]; ok {
		return RiskReadOnly
	}
	for _, prefix := range mutatingPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return RiskMutating
		}
	}
	return RiskUnknown
}
