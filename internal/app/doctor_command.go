package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/IsLand1314/Drift/internal/config"
	"github.com/IsLand1314/Drift/internal/mcp"
	"github.com/IsLand1314/Drift/internal/tool"
)

func runDoctorCommand(args []string, getenv func(string) string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspaceTarget := flags.String("w", "", "workspace path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "用法：drift doctor [-w 路径]")
		return 2
	}
	launchDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "doctor: 无法获取启动目录")
		return 1
	}
	selection, err := resolveWorkspace(launchDir, *workspaceTarget)
	if err != nil {
		fmt.Fprintln(stderr, "doctor: workspace 目标无效")
		return 2
	}
	fmt.Fprintf(out, "workspace: %s\n", selection.Root)

	userConfig, configErr := config.LoadUserConfig(filepath.Join(selection.Root, ".drift"))
	if configErr != nil {
		fmt.Fprintf(out, "config: error (%s)\n", configErr)
		return 1
	}
	if !userConfig.ConfigPresent {
		fmt.Fprintln(out, "config: missing")
	} else {
		fmt.Fprintln(out, "config: ok")
	}
	for _, provider := range userConfig.Providers {
		state := "missing-key"
		if provider.Protocol == "codex" || userConfig.APIKey(provider.Name, getenv) != "" {
			state = "configured"
		}
		fmt.Fprintf(out, "provider %s: %s (%s/%s)\n", provider.Name, state, provider.Protocol, provider.Model)
	}

	mcpConfig, mcpErr := mcp.Load(selection.Root)
	if mcpErr != nil {
		fmt.Fprintf(out, "mcp: error (%s)\n", mcpErr)
		return 1
	}
	fmt.Fprintf(out, "mcp: %d server(s)\n", len(mcpConfig.Servers))
	fmt.Fprintln(out, "boundary: workspace-only (.drift/.git protected)")

	caps := tool.DetectSandboxForWorkspace(selection.Root)
	if caps.Backend == "" && caps.Probe == "" {
		fmt.Fprintln(out, "sandbox: unavailable")
	} else {
		fmt.Fprintf(out, "sandbox: backend=%s probe=%s reliable=%t\n", caps.Backend, caps.Probe, caps.Reliable)
	}
	return 0
}
