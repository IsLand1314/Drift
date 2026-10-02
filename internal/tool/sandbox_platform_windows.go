//go:build windows

package tool

// detectWindowsSandbox remains unavailable until the AppContainer probe is implemented.
func detectWindowsSandbox() SandboxCapabilities { return SandboxCapabilities{} }
