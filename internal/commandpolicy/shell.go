package commandpolicy

import "strings"

// hasShellControl reports shell syntax that makes a command more than one
// simple executable invocation. It is intentionally a conservative lexer,
// not a complete shell parser. Quoted text is ignored so a literal `|` in an
// argument does not turn a read-only command into a compound command.
func hasShellControl(command string, shell Shell) bool {
	var single, double, escaped bool
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if shell == ShellPOSIX && ch == '\\' && !single {
			escaped = true
			continue
		}
		if ch == '\'' && !double {
			single = !single
			continue
		}
		if ch == '"' && !single {
			double = !double
			continue
		}
		if single || double {
			continue
		}
		if strings.ContainsRune("|&;><\r\n", rune(ch)) {
			return true
		}
		if shell == ShellPOSIX && ch == '`' {
			return true
		}
		if shell == ShellPOSIX && ch == '$' && i+1 < len(command) && command[i+1] == '(' {
			return true
		}
		if shell == ShellCMD && ch == '(' {
			return true
		}
		if shell == ShellCMD && (ch == '%' || ch == '!') {
			// Environment expansion is deliberately not interpreted here. It
			// changes the command after policy evaluation, so it cannot qualify
			// for the built-in read-only fast path.
			return true
		}
	}
	return false
}
