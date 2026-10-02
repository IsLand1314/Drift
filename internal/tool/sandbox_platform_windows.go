//go:build windows

package tool

func detectWindowsSandbox(root string) SandboxCapabilities {
	return probeWindowsSandbox(root)
}
