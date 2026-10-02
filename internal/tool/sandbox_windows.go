//go:build windows

package tool

// windowsSandboxProbe is deliberately kept behind a seam until the native
// AppContainer launch and ACL cleanup path is implemented and verified on a
// supported Windows host. A false result is safer than claiming isolation.
var windowsSandboxProbe = func(string) SandboxCapabilities {
	return windowsSandboxUnavailable("appcontainer-native-probe-not-implemented")
}

func probeWindowsSandbox(root string) SandboxCapabilities {
	return windowsSandboxProbe(root)
}

func windowsSandboxUnavailable(reason string) SandboxCapabilities {
	return SandboxCapabilities{Probe: "unavailable:" + reason}
}
