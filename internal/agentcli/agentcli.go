// Package agentcli contains the small compatibility layer for officially
// supported interactive agent CLIs. The session package remains a generic
// PTY runner; provider-specific startup behavior lives here.
package agentcli

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Provider string

const (
	Claude Provider = "claude"
	Codex  Provider = "codex"
	Gemini Provider = "gemini"
	// Agy is Google's Antigravity CLI, which Google recommends in place of
	// Gemini CLI.
	Agy Provider = "agy"
)

var Supported = []Provider{Claude, Codex, Agy, Gemini}

// DetectInstalled returns the first supported CLI available on PATH. The
// Supported order is intentional: Claude is preferred, then Codex, then
// Antigravity, then Gemini.
func DetectInstalled() (Provider, bool) {
	return detectInstalled(exec.LookPath)
}

func detectInstalled(lookPath func(string) (string, error)) (Provider, bool) {
	installed := detectAllInstalled(lookPath)
	if len(installed) == 0 {
		return "", false
	}
	return installed[0], true
}

// DetectAllInstalled returns every supported CLI found on PATH in the same
// stable priority order used by automatic single-provider setup.
func DetectAllInstalled() []Provider {
	return detectAllInstalled(exec.LookPath)
}

func detectAllInstalled(lookPath func(string) (string, error)) []Provider {
	installed := make([]Provider, 0, len(Supported))
	for _, provider := range Supported {
		if _, err := lookPath(string(provider)); err == nil {
			installed = append(installed, provider)
		}
	}
	return installed
}

func (p Provider) Valid() bool {
	for _, supported := range Supported {
		if p == supported {
			return true
		}
	}
	return false
}

func Parse(value string) (Provider, error) {
	p := Provider(strings.ToLower(strings.TrimSpace(value)))
	if !p.Valid() {
		return "", fmt.Errorf("unsupported agent CLI %q (choose claude, codex, agy or gemini)", value)
	}
	return p, nil
}

// Detect recognizes direct invocations and conventionally named wrappers.
// An explicit profile provider takes precedence via Resolve.
func Detect(command string) Provider {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(command, `\`, "/")))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	for _, provider := range Supported {
		if base == string(provider) || strings.HasPrefix(base, string(provider)+"-") {
			return provider
		}
	}
	return ""
}

func Resolve(declared Provider, command string) Provider {
	if declared.Valid() {
		return declared
	}
	return Detect(command)
}

type Launch struct {
	Args           []string
	Env            map[string]string
	PromptInjected bool
}

// Prepare adapts only startup mechanics. Permissions and model selection stay
// visible in the configured profile arguments. A non-empty bridge is the
// absolute path of the omo executable: Claude Code and Codex then get
// BridgeArgs, omo's MCP server for this session only.
func Prepare(provider Provider, command string, args []string, env map[string]string, workdir, prompt string, trustWorkdir, injectPrompt bool, bridge string) Launch {
	provider = Resolve(provider, command)
	launch := Launch{
		Args: append([]string(nil), args...),
		Env:  cloneEnv(env),
	}
	for i, arg := range launch.Args {
		if strings.Contains(arg, "%prompt%") {
			launch.Args[i] = strings.ReplaceAll(arg, "%prompt%", prompt)
			launch.PromptInjected = true
		}
	}
	if provider.Valid() {
		// omo provides a real PTY even when its parent was launched with an
		// empty or "dumb" TERM (common in services and test runners). Codex
		// otherwise blocks behind a confirmation before seeing the prompt.
		if _, configured := launch.Env["TERM"]; !configured {
			launch.Env["TERM"] = "xterm-256color"
		}
	}
	switch provider {
	case Claude:
		// --mcp-config takes several values: nothing may follow it.
		if bridge != "" {
			launch.Args = append(launch.Args, BridgeArgs(Claude, bridge)...)
		}
	case Codex:
		// Codex accepts an initial prompt positionally while retaining its TUI.
		// Workspace config and installed plugin hooks have separate trust gates
		// from tool approvals; both must be satisfied before the prompt is read.
		if trustWorkdir {
			launch.Args = append(launch.Args, "-c", fmt.Sprintf("projects={%q={trust_level=\"trusted\"}}", workdir))
			if !hasArg(launch.Args, "--dangerously-bypass-hook-trust") {
				launch.Args = append(launch.Args, "--dangerously-bypass-hook-trust")
			}
		}
		if bridge != "" {
			launch.Args = append(launch.Args, BridgeArgs(Codex, bridge)...)
		}
		if injectPrompt && !launch.PromptInjected {
			launch.Args = append(launch.Args, prompt)
			launch.PromptInjected = true
		}
	case Gemini:
		// Gemini's -p is headless; --prompt-interactive starts the task and
		// keeps the session alive for later omo mail nudges and user input.
		if injectPrompt && !launch.PromptInjected {
			launch.Args = append(launch.Args, "--prompt-interactive", prompt)
			launch.PromptInjected = true
		}
		if trustWorkdir {
			launch.Env["GEMINI_CLI_TRUST_WORKSPACE"] = "true"
		}
	case Agy:
		// Antigravity asks whether to trust a new folder before it reads
		// anything, also with --dangerously-skip-permissions. --settings
		// lays settings over its settings.json for this session only and
		// never saves them, so the folder is trusted without a lasting entry.
		if trustWorkdir {
			trusted, _ := json.Marshal(map[string][]string{"trustedWorkspaces": {workdir}})
			launch.Args = append(launch.Args, "--settings", string(trusted))
		}
		// Like Gemini's: -p is headless, --prompt-interactive runs the
		// prompt and keeps the session.
		if injectPrompt && !launch.PromptInjected {
			launch.Args = append(launch.Args, "--prompt-interactive", prompt)
			launch.PromptInjected = true
		}
	}
	return launch
}

// BridgeName is the name of omo's MCP server in an agent program.
const BridgeName = "omo"

// bridgeVariables are the variables the bridge reads: the session it acts
// as and the socket. Codex passes a stdio server only the variables it
// lists.
var bridgeVariables = []string{"OMO_AGENT_ID", "OMO_SOCKET"}

// BridgeArgs are the launch arguments that give one session of provider
// omo's MCP server: `<omo> mcp stdio`, started by the program with the
// session's environment, so it acts as the session as `omo ai` does. They
// hold no session id and no token. Antigravity and Gemini CLI have no flag
// for a server of one session: nil.
func BridgeArgs(provider Provider, omo string) []string {
	switch provider {
	case Claude:
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		type server struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		_ = enc.Encode(map[string]any{"mcpServers": map[string]server{BridgeName: {Type: "stdio", Command: omo, Args: []string{"mcp", "stdio"}}}})
		return []string{"--mcp-config", strings.TrimSuffix(b.String(), "\n")}
	case Codex:
		key := "mcp_servers." + BridgeName + "."
		return []string{
			"-c", key + "command=" + tomlString(omo),
			"-c", key + "args=" + tomlArray([]string{"mcp", "stdio"}),
			"-c", key + "env_vars=" + tomlArray(bridgeVariables),
		}
	}
	return nil
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlArray is a TOML array of basic strings.
func tomlArray(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = tomlString(item)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func cloneEnv(env map[string]string) map[string]string {
	cloned := make(map[string]string, len(env)+1)
	for key, value := range env {
		cloned[key] = value
	}
	return cloned
}
