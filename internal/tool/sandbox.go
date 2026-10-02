package tool

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

type SandboxMode string

const (
	SandboxOff      SandboxMode = "off"
	SandboxAuto     SandboxMode = "auto"
	SandboxRequired SandboxMode = "required"
)

type SandboxCapabilities struct {
	Backend      string
	Reliable     bool
	Capabilities []string
}

type SandboxDecision struct {
	Mode         SandboxMode
	Backend      string
	Available    bool
	Capabilities []string
}

func ParseSandboxMode(value string) (SandboxMode, error) {
	switch SandboxMode(strings.TrimSpace(value)) {
	case SandboxOff, SandboxAuto, SandboxRequired:
		return SandboxMode(strings.TrimSpace(value)), nil
	default:
		return "", fmt.Errorf("unknown sandbox mode %q (choose off, auto, or required)", value)
	}
}

func DetectSandbox() SandboxCapabilities {
	var backend string
	switch runtime.GOOS {
	case "linux":
		backend = "bwrap"
	case "darwin":
		backend = "sandbox-exec"
	default:
		return SandboxCapabilities{}
	}
	if _, err := exec.LookPath(backend); err != nil {
		return SandboxCapabilities{}
	}
	// Detection is deliberately conservative: a binary's presence does not
	// prove that Drift has compiled and tested a safe profile for this host.
	return SandboxCapabilities{Backend: backend, Reliable: false}
}

func SelectSandbox(mode SandboxMode, capabilities SandboxCapabilities) (SandboxDecision, error) {
	if mode != SandboxOff && mode != SandboxAuto && mode != SandboxRequired {
		return SandboxDecision{Mode: mode}, fmt.Errorf("unknown sandbox mode %q", mode)
	}
	decision := SandboxDecision{Mode: mode, Backend: capabilities.Backend, Available: capabilities.Reliable, Capabilities: append([]string(nil), capabilities.Capabilities...)}
	if mode == SandboxOff {
		decision.Backend = ""
		decision.Available = false
		decision.Capabilities = nil
		return decision, nil
	}
	if mode == SandboxRequired && !decision.Available {
		return decision, fmt.Errorf("sandbox required but no reliable OS backend is available")
	}
	return decision, nil
}
