package commandpolicy

// Shell identifies the command interpreter used by Bash on the target OS.
// Drift currently executes POSIX commands through /bin/sh and Windows
// commands through cmd.exe.
type Shell string

const (
	ShellPOSIX Shell = "posix"
	ShellCMD   Shell = "cmd"
)

// Risk is the content risk assigned to a command before permission rules are
// evaluated. It is deliberately not an authorization decision: callers still
// decide whether a non-dangerous command needs approval.
type Risk string

const (
	RiskDangerous Risk = "dangerous"
	RiskReadOnly  Risk = "read_only"
	RiskMutating  Risk = "mutating"
	RiskUnknown   Risk = "unknown"
)

// Assessment is the stable, auditable result of command classification.
type Assessment struct {
	Risk       Risk
	RuleID     string
	Reason     string
	Shell      Shell
	HasControl bool
}

func (a Assessment) Dangerous() bool { return a.Risk == RiskDangerous }

func ShellForPlatform(goos string) Shell {
	if goos == "windows" {
		return ShellCMD
	}
	return ShellPOSIX
}
