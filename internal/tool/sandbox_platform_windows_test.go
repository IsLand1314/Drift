//go:build windows

package tool

import "testing"

func TestWindowsSandboxBackendIsUnavailableBeforeAppContainerProbe(t *testing.T) {
	got := detectWindowsSandbox(".")
	t.Logf("windows sandbox probe=%+v", got)
	if got.Backend != "" || got.Reliable || len(got.Capabilities) != 0 || got.Probe == "" {
		t.Fatalf("windows sandbox=%+v, want unavailable", got)
	}
}
