package agents

import (
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

// AccountDirVariable is the environment variable a profile's account folder
// is exported as, by CLI type:
//
//   - claude: CLAUDE_CONFIG_DIR, the folder that holds the account's
//     settings, credentials and .claude.json.
//   - codex: CODEX_HOME, the folder that holds config.toml and auth.json.
//   - gemini: GEMINI_CLI_HOME. Unlike the other two it names the folder
//     Gemini CLI uses in place of the home directory: the account's
//     settings and credentials live in its .gemini subfolder.
//   - custom: none; a custom CLI sets its own variables in the profile
//     environment.
func AccountDirVariable(cli string) string {
	switch cli {
	case api.CLIClaude:
		return "CLAUDE_CONFIG_DIR"
	case api.CLICodex:
		return "CODEX_HOME"
	case api.CLIGemini:
		return "GEMINI_CLI_HOME"
	}
	return ""
}

// terminalDefaults describe the terminal agents run in: omo's own PTY,
// shown in browser terminals. Whatever terminal omo itself was started
// from says nothing about it. A configured value wins.
var terminalDefaults = map[string]string{
	"TERM":      "xterm-256color",
	"COLORTERM": "truecolor",
	"COLORFGBG": "15;0",
}

// BuildEnv returns the variables an agent process gets on top of omo's own
// environment (from which omo's control credentials are always removed).
// Later layers win:
//
//  1. spec.AgentEnv, expanded: $VAR, ${VAR}, ${VAR:-fallback} and
//     `command` read omo's environment and the other shared keys.
//  2. The account folder as AccountDirVariable(spec.CLI).
//  3. spec.ProfileEnv, then spec.ModelEnv, then launchEnv (what the CLI's
//     start adapter added), all literal.
//  4. OMO_AGENT_ID (the session id) and OMO_SOCKET, which nothing can
//     override.
//
// PATH, wherever it came from, is then prefixed with spec.PathPrefix.
// TERM, COLORTERM and COLORFGBG describe omo's terminal unless a layer sets
// them.
//
// Each backtick command runs with session.DefaultCommandTimeout; one that
// does not finish fails the build with a *session.CommandTimeoutError that
// names the variable.
func BuildEnv(spec Spec, launchEnv map[string]string) ([]string, error) {
	return buildEnv(spec, launchEnv, runtime.GOOS == "windows", session.DefaultCommandTimeout)
}

func buildEnv(spec Spec, launchEnv map[string]string, windows bool, timeout time.Duration) ([]string, error) {
	account := map[string]string{}
	if name := AccountDirVariable(spec.CLI); name != "" && spec.AccountDir != "" {
		account[name] = spec.AccountDir
	}
	layers := []map[string]string{account, spec.ProfileEnv, spec.ModelEnv, launchEnv}
	defaults := map[string]string{}
	for key, value := range terminalDefaults {
		if !anyLayerHas(key, windows, append([]map[string]string{spec.AgentEnv}, layers...)...) {
			defaults[key] = value
		}
	}
	all := append([]map[string]string{defaults}, layers...)
	all = append(all, map[string]string{
		"OMO_AGENT_ID": spec.SessionID,
		"OMO_SOCKET":   spec.Socket,
	})
	env, err := session.ExpandEnvironment(timeout, spec.AgentEnv, all...)
	if err != nil {
		return nil, err
	}
	if spec.PathPrefix == "" {
		return env, nil
	}
	sep := string(os.PathListSeparator)
	for i, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if sameKey(key, "PATH", windows) {
			env[i] = key + "=" + joinPath(spec.PathPrefix, value, sep)
			return env, nil
		}
	}
	return append(env, "PATH="+joinPath(spec.PathPrefix, inheritedPath(windows), sep)), nil
}

func joinPath(prefix, rest, sep string) string {
	if rest == "" {
		return prefix
	}
	return prefix + sep + rest
}

func inheritedPath(windows bool) string {
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if sameKey(key, "PATH", windows) {
			return value
		}
	}
	return ""
}

func anyLayerHas(key string, windows bool, layers ...map[string]string) bool {
	for _, layer := range layers {
		for candidate := range layer {
			if sameKey(candidate, key, windows) {
				return true
			}
		}
	}
	return false
}

func sameKey(a, b string, windows bool) bool {
	if windows {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// EffectiveEnv returns the complete environment an agent started from spec
// receives (omo's environment without its control credentials, overlaid
// with BuildEnv and the CLI's launch variables), except OMO_AGENT_ID, which
// the caller already has. Start trusts Claude Code folders in the account
// this environment selects, so pass the same map to
// claudetrust.ForgetForEnv when the folder goes away. Backtick commands in
// spec.AgentEnv run again (see BuildEnv).
func EffectiveEnv(spec Spec) (map[string]string, error) {
	env, err := BuildEnv(spec, prepareLaunch(spec).Env)
	if err != nil {
		return nil, err
	}
	full := effectiveEnv(env)
	delete(full, "OMO_AGENT_ID")
	return full, nil
}

// effectiveEnv is the complete environment the process receives, as a map:
// omo's environment without its control credentials, overlaid with env.
func effectiveEnv(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range session.ProcessEnvironment(env) {
		if key, value, ok := strings.Cut(entry, "="); ok {
			out[key] = value
		}
	}
	return out
}
