//go:build darwin

package tool

import "testing"

func TestDarwinSandboxBackendIsUnavailable(t *testing.T) {
	got := DetectSandbox()
	if got.Backend != "" || got.Reliable || len(got.Capabilities) != 0 {
		t.Fatalf("darwin sandbox=%+v, want unavailable", got)
	}
}
