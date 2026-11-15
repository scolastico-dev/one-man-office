package webterm

import (
	"strings"
)

// CleanEnvironment returns base without omo control variables and terminal
// capability hints, plus the terminal settings web terminals need.
func CleanEnvironment(base []string) []string {
	var env []string
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "OMO_CONTROL_") || key == "OMO_AGENT_ID" || key == "OMO_SOCKET" || key == "TERM" || key == "COLORTERM" || key == "COLORFGBG" {
			continue
		}
		env = append(env, entry)
	}
	// These terminals can have several attached displays, like a multiplexer.
	// screen-256color also prevents Bubble Tea's package-init OSC color probe
	// from consuming early user keystrokes before the child starts reading.
	return append(env, "TERM=screen-256color", "COLORTERM=truecolor", "COLORFGBG=15;0")
}
