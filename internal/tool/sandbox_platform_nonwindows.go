//go:build !windows

package tool

func detectWindowsSandbox(string) SandboxCapabilities {
	return SandboxCapabilities{Probe: "unavailable"}
}
