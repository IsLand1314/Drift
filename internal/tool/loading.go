package tool

import "fmt"

// LoadingStrategy controls how registered tool schemas reach the model.
type LoadingStrategy string

const (
	LoadingEager    LoadingStrategy = "eager"
	LoadingDispatch LoadingStrategy = "dispatch"
	LoadingNative   LoadingStrategy = "native"
)

func ParseToolLoadingStrategy(value string) (LoadingStrategy, error) {
	strategy := LoadingStrategy(value)
	switch strategy {
	case LoadingEager, LoadingDispatch, LoadingNative:
		return strategy, nil
	default:
		return "", fmt.Errorf("invalid tool loading strategy %q", value)
	}
}

// ResolveToolLoadingStrategy reports the executable strategy. Native references
// are not part of Drift's provider-neutral request yet, so they safely fall
// back to the existing dispatch protocol until a provider adapter implements it.
func ResolveToolLoadingStrategy(requested LoadingStrategy, provider string) (LoadingStrategy, bool) {
	if requested == LoadingNative {
		return LoadingDispatch, false
	}
	return requested, false
}
