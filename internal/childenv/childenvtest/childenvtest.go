// Package childenvtest holds samples of the variables childenv removes, for
// tests in other packages. Only tests import it.
package childenvtest

import "testing"

// Samples are variables an IDE or terminal session puts into the
// environment of a program started from it, one or more per rule of
// childenv's list. Tests set them to check that none reaches a child.
var Samples = map[string]string{
	"TERM_PROGRAM":                    "vscode",
	"TERM_PROGRAM_VERSION":            "1.99.0",
	"TERM_PRODUCT":                    "Trae",
	"TERMINAL_EMULATOR":               "JetBrains-JediTerm",
	"VSCODE_IPC_HOOK_CLI":             "/run/user/1000/vscode-ipc.sock",
	"VSCODE_GIT_ASKPASS_MAIN":         "/usr/share/code/askpass-main.js",
	"VSCODE_GIT_IPC_HANDLE":           "/run/user/1000/vscode-git.sock",
	"VSCODE_INJECTION":                "1",
	"GIT_ASKPASS":                     "/usr/share/code/askpass.sh",
	"CLAUDE_CODE_SSE_PORT":            "41234",
	"CLAUDE_CODE_ENTRYPOINT":          "cli",
	"CLAUDECODE":                      "1",
	"CLAUDE_CODE_IDE_HOST_OVERRIDE":   "localhost",
	"GEMINI_CLI_IDE_SERVER_PORT":      "41235",
	"GEMINI_CLI_IDE_WORKSPACE_PATH":   "/src",
	"GEMINI_CLI":                      "1",
	"CODEX_THREAD_ID":                 "thread",
	"CODEX_SANDBOX":                   "seatbelt",
	"CODEX_SANDBOX_NETWORK_DISABLED":  "1",
	"CURSOR_TRACE_ID":                 "abc",
	"JETBRAINS_IDE":                   "idea",
	"ZED_TERM":                        "true",
	"ELECTRON_RUN_AS_NODE":            "1",
	"TMUX":                            "/tmp/tmux-1000/default,1,0",
	"TMUX_PANE":                       "%1",
	"STY":                             "1.pts-0.host",
	"ZELLIJ":                          "0",
	"ZELLIJ_SESSION_NAME":             "main",
	"KITTY_WINDOW_ID":                 "1",
	"KITTY_LISTEN_ON":                 "unix:/tmp/kitty",
	"WEZTERM_PANE":                    "0",
	"ITERM_SESSION_ID":                "w0t0p0",
	"WT_SESSION":                      "guid",
	"ALACRITTY_SOCKET":                "/run/user/1000/Alacritty.sock",
	"ALACRITTY_WINDOW_ID":             "1",
	"GNOME_TERMINAL_SCREEN":           "/org/gnome/Terminal/screen/1",
	"KONSOLE_DBUS_SESSION":            "/Sessions/1",
	"NODE_OPTIONS":                    "--require /usr/share/code/ms-vscode.js-debug/src/bootloader.js",
	"VSCODE_INSPECTOR_OPTIONS":        "{}",
	"CURSOR_AGENT":                    "1",
	"GEMINI_CLI_IDE_SERVER_STDIO_CMD": "x",
}

// Set sets every sample in the test process for the duration of t.
func Set(t testing.TB) {
	t.Helper()
	for k, v := range Samples {
		t.Setenv(k, v)
	}
}
