//go:build windows

package tool

// windowsSandboxProbe is deliberately kept behind a seam until the native
// AppContainer launch and ACL cleanup path is implemented and verified on a
// supported Windows host. A false result is safer than claiming isolation.
var windowsSandboxProbe = func(string) SandboxCapabilities {
	return windowsSandboxUnavailable()
}

func probeWindowsSandbox(root string) SandboxCapabilities {
	return windowsSandboxProbe(root)
}

func windowsSandboxUnavailable() SandboxCapabilities { return SandboxCapabilities{} }
