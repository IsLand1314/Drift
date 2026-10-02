//go:build windows

package tool

import "testing"

func TestWindowsSandboxBackendIsUnavailableBeforeAppContainerProbe(t *testing.T) {
	got := detectWindowsSandbox(".")
	if got.Backend != "" || got.Reliable || len(got.Capabilities) != 0 {
		t.Fatalf("windows sandbox=%+v, want unavailable", got)
	}
}
