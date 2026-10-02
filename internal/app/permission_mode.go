package app

import (
	"fmt"
	"strings"

	"github.com/IsLand1314/Drift/internal/agent"
)

type permissionMode string

const (
	permissionModeDefault     permissionMode = "default"
	permissionModeAcceptEdits permissionMode = "acceptEdits"
	permissionModePlan        permissionMode = "plan"
	permissionModeBypass      permissionMode = "bypassPermissions"
)

func parsePermissionMode(value string) (permissionMode, error) {
	switch strings.TrimSpace(value) {
	case string(permissionModeDefault):
		return permissionModeDefault, nil
	case string(permissionModeAcceptEdits):
		return permissionModeAcceptEdits, nil
	case string(permissionModePlan):
		return permissionModePlan, nil
	case string(permissionModeBypass):
		return permissionModeBypass, nil
	default:
		return "", fmt.Errorf("未知权限模式 %q（可选 default、acceptEdits、plan、bypassPermissions）", value)
	}
}

func decidePermission(mode permissionMode, request agent.PermissionRequest) agent.PermissionDecision {
	switch mode {
	case permissionModePlan:
		return agent.PermissionDecision{Reason: "plan_read_only"}
	case permissionModeBypass:
		return agent.PermissionDecision{Allow: true, Reason: "mode_bypass"}
	case permissionModeAcceptEdits:
		if request.ToolName == "write_file" || request.ToolName == "edit_file" {
			return agent.PermissionDecision{Allow: true, Reason: "mode_accept_edits"}
		}
	}
	return agent.PermissionDecision{Reason: "approval_required"}
}

func parsePermissionModeCommand(command string) (permissionMode, bool, error) {
	command = strings.TrimSpace(command)
	if command == "/permissions mode" {
		return "", true, nil
	}
	const prefix = "/permissions mode "
	if strings.HasPrefix(command, prefix) {
		mode, err := parsePermissionMode(strings.TrimSpace(strings.TrimPrefix(command, prefix)))
		if err != nil {
			return "", true, err
		}
		return mode, true, nil
	}
	const directPrefix = "/permissions "
	if !strings.HasPrefix(command, directPrefix) {
		return "", false, nil
	}
	value := strings.TrimSpace(strings.TrimPrefix(command, directPrefix))
	if value == "clear" {
		return "", false, nil
	}
	mode, err := parsePermissionMode(value)
	if err != nil {
		return "", true, err
	}
	return mode, true, nil
}
