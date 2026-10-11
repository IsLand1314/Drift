package commandpolicy

import "strings"

// Assess performs conservative command-content classification. It intentionally
// returns Unknown for syntax it cannot prove to be a simple read-only command.
func Assess(command string, shell Shell) Assessment {
	command = strings.TrimSpace(command)
	result := Assessment{Risk: RiskUnknown, Shell: shell}
	if command == "" {
		result.Reason = "blank command"
		return result
	}
	if shell != ShellCMD && shell != ShellPOSIX {
		shell = ShellPOSIX
		result.Shell = shell
	}
	for _, rule := range dangerousRules {
		if rule.match(command, shell) {
			result.Risk = RiskDangerous
			result.RuleID = rule.id
			result.Reason = rule.reason
			return result
		}
	}
	result.HasControl = hasShellControl(command, shell)
	if result.HasControl {
		result.Reason = "shell control structure requires review"
		return result
	}
	result.Risk = classifySimpleCommand(command)
	switch result.Risk {
	case RiskReadOnly:
		result.Reason = "conservative built-in read-only command"
	case RiskMutating:
		result.Reason = "known mutating command"
	default:
		result.Reason = "command is not in the built-in read-only set"
	}
	return result
}
