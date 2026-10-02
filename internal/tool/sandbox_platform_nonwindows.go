//go:build !windows

package tool

func detectWindowsSandbox() SandboxCapabilities { return SandboxCapabilities{} }
