package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
	Probe        string
}

type SandboxDecision struct {
	Mode         SandboxMode
	Backend      string
	Available    bool
	Capabilities []string
	Probe        string
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
	return DetectSandboxForWorkspace(".")
}

func DetectSandboxForWorkspace(root string) SandboxCapabilities {
	if runtime.GOOS == "windows" {
		return detectWindowsSandbox(root)
	}
	var backend string
	switch runtime.GOOS {
	case "linux":
		backend = "bwrap"
	case "darwin":
		return SandboxCapabilities{}
	default:
		return SandboxCapabilities{}
	}
	if _, err := exec.LookPath(backend); err != nil {
		return SandboxCapabilities{}
	}
	if backend == "bwrap" {
		probe := exec.Command("bwrap", "--die-with-parent", "--new-session", "--unshare-net", "--ro-bind", "/", "/", "true")
		probeTimeout := time.AfterFunc(2*time.Second, func() { _ = probe.Process.Kill() })
		err := probe.Run()
		probeTimeout.Stop()
		if err == nil {
			return SandboxCapabilities{Backend: backend, Reliable: true, Probe: "passed", Capabilities: []string{"workspace-write", "network-isolated", "process-tree"}}
		}
	}
	return SandboxCapabilities{Backend: backend, Reliable: false, Probe: "failed"}
}

func bwrapArguments(root, cwd, command string) []string {
	workspace, _ := filepath.Abs(root)
	workdir := "/mnt"
	if cwd != "." && cwd != "" {
		workdir = filepath.Join(workdir, filepath.FromSlash(cwd))
	}
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-net",
		"--ro-bind", "/", "/",
		"--tmpfs", "/home",
		"--tmpfs", "/root",
		"--tmpfs", "/tmp",
		"--proc", "/proc",
		"--dev", "/dev",
		"--bind", workspace, "/mnt",
	}
	for _, protected := range []string{".drift", ".git"} {
		if _, err := os.Stat(filepath.Join(workspace, protected)); err == nil {
			args = append(args, "--tmpfs", filepath.Join("/mnt", protected))
		}
	}
	args = append(args, "--chdir", workdir, "/bin/sh", "-c", command)
	return args
}

func sandboxCommand(ctx context.Context, command, root, cwd string, decision SandboxDecision) *exec.Cmd {
	if decision.Available && decision.Backend == "bwrap" {
		return exec.CommandContext(ctx, "bwrap", bwrapArguments(root, cwd, command)...)
	}
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

func SelectSandbox(mode SandboxMode, capabilities SandboxCapabilities) (SandboxDecision, error) {
	if mode != SandboxOff && mode != SandboxAuto && mode != SandboxRequired {
		return SandboxDecision{Mode: mode}, fmt.Errorf("unknown sandbox mode %q", mode)
	}
	decision := SandboxDecision{Mode: mode, Backend: capabilities.Backend, Available: capabilities.Reliable, Capabilities: append([]string(nil), capabilities.Capabilities...), Probe: capabilities.Probe}
	if mode == SandboxOff {
		decision.Backend = ""
		decision.Available = false
		decision.Capabilities = nil
		decision.Probe = "off"
		return decision, nil
	}
	if mode == SandboxRequired && !decision.Available {
		return decision, fmt.Errorf("sandbox required but no reliable OS backend is available")
	}
	return decision, nil
}
