// Package childenv is the environment omo hands to the programs it starts on
// the user's behalf: agents, the CEO, export jobs, plugin programs, git,
// usage helpers and the user's shells. It is omo's own environment without
// the variables that tie a program to the IDE or terminal session omo was
// started from: an agent started by an omo that runs in a VS Code terminal
// would otherwise try to attach to that VS Code, and a shell would believe
// it runs inside the user's tmux. Everything else stays.
//
// The user's agent environment and a profile's environment are laid over the
// result, so a user who wants one of these variables can set it there.
package childenv

import (
	"os"
	"strings"
)

// exact are variables removed by name.
var exact = map[string]bool{
	// The terminal program omo was started from, not the PTY omo gives.
	"TERM_PROGRAM":         true,
	"TERM_PROGRAM_VERSION": true,
	"TERM_PRODUCT":         true,
	"TERMINAL_EMULATOR":    true, // JetBrains
	// Claude Code: the IDE's MCP port, and the markers of a program that a
	// Claude Code session started.
	"CLAUDE_CODE_SSE_PORT":          true,
	"CLAUDE_CODE_ENTRYPOINT":        true,
	"CLAUDECODE":                    true,
	"CLAUDE_CODE_IDE_HOST_OVERRIDE": true,
	// Set for a Claude Code started by another; that one then keeps no
	// transcript ("Transcript saving is off").
	"CLAUDE_CODE_CHILD_SESSION": true,
	// Markers of a program that a Gemini, Codex or Cursor agent started.
	"GEMINI_CLI":      true,
	"CODEX_THREAD_ID": true,
	"CURSOR_AGENT":    true,
	// The IDE's git credential helper bridge.
	"GIT_ASKPASS": true,
	// Electron hosts (VS Code, Cursor) run a node program with it.
	"ELECTRON_RUN_AS_NODE": true,
	// Terminal multiplexer and terminal sessions: a program would talk to
	// the user's own session through them.
	"TMUX":                   true,
	"TMUX_PANE":              true,
	"STY":                    true,
	"KITTY_WINDOW_ID":        true,
	"KITTY_PID":              true,
	"KITTY_LISTEN_ON":        true,
	"ITERM_SESSION_ID":       true,
	"WT_SESSION":             true,
	"WT_PROFILE_ID":          true,
	"ALACRITTY_SOCKET":       true,
	"ALACRITTY_WINDOW_ID":    true,
	"ALACRITTY_LOG":          true,
	"GNOME_TERMINAL_SCREEN":  true,
	"GNOME_TERMINAL_SERVICE": true,
}

// prefixes are variable name prefixes removed.
var prefixes = []string{
	"VSCODE_",         // VS Code, also its git bridge (VSCODE_GIT_*) and IPC socket
	"CURSOR_",         // Cursor
	"JETBRAINS_",      // JetBrains IDEs
	"ZED_",            // Zed
	"GEMINI_CLI_IDE_", // Gemini CLI's IDE companion: port, workspace, token
	"CODEX_SANDBOX",   // set by Codex for the programs it runs
	"ZELLIJ",          // ZELLIJ and ZELLIJ_*
	"WEZTERM_",
	"KONSOLE_DBUS_",
}

// Detached reports whether omo removes the variable name with value from the
// environment of the programs it starts. Names are compared without regard
// to case. NODE_OPTIONS is removed only when it loads the debugger bootloader
// that VS Code and Cursor inject (js-debug); a NODE_OPTIONS of the user's
// own stays.
func Detached(name, value string) bool {
	upper := strings.ToUpper(name)
	if exact[upper] {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return upper == "NODE_OPTIONS" && strings.Contains(value, "js-debug")
}

// Clean returns env without the detached variables (see Detached), in the
// same order.
func Clean(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if Detached(name, value) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// Inherited is omo's own environment without the detached variables: the
// base environment of every program omo starts. It is the only source of a
// child's inherited environment (a test in this package fails on another
// os.Environ outside it).
func Inherited() []string { return Clean(os.Environ()) }

// Own is omo's own environment as it is. It is for reading values only (a
// reference such as $TERM_PROGRAM in the user's agent environment reads it,
// so a user can pass a variable on explicitly), never the environment of a
// program.
func Own() []string { return os.Environ() }
