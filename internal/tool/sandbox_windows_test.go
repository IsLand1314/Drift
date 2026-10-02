//go:build windows

package tool

import (
	"strings"
	"testing"
)

func TestProbeWindowsSandboxDefaultsToUnavailable(t *testing.T) {
	got := probeWindowsSandbox(t.TempDir())
	if got.Reliable || got.Backend != "" || len(got.Capabilities) != 0 || !strings.HasPrefix(got.Probe, "unavailable:") {
		t.Fatalf("probe=%+v, want unavailable", got)
	}
}

func TestProbeWindowsSandboxAcceptsOnlyVerifiedCapabilities(t *testing.T) {
	original := windowsSandboxProbe
	t.Cleanup(func() { windowsSandboxProbe = original })
	windowsSandboxProbe = func(string) SandboxCapabilities {
		return SandboxCapabilities{
			Backend:      "appcontainer",
			Reliable:     true,
			Capabilities: []string{"workspace-write", "network-isolated", "process-tree"},
		}
	}

	got := probeWindowsSandbox(t.TempDir())
	if !got.Reliable || got.Backend != "appcontainer" {
		t.Fatalf("probe=%+v, want verified appcontainer", got)
	}
	want := []string{"workspace-write", "network-isolated", "process-tree"}
	if len(got.Capabilities) != len(want) {
		t.Fatalf("capabilities=%v, want %v", got.Capabilities, want)
	}
	for i := range want {
		if got.Capabilities[i] != want[i] {
			t.Fatalf("capabilities=%v, want %v", got.Capabilities, want)
		}
	}
}
