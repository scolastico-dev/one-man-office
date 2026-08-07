// Package agentcli contains the small compatibility layer for officially
// supported interactive agent CLIs. The session package remains a generic
// PTY runner; provider-specific startup behavior lives here.
package agentcli

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Provider string

const (
	Claude Provider = "claude"
	Codex  Provider = "codex"
	Gemini Provider = "gemini"
)

var Supported = []Provider{Claude, Codex, Gemini}

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
		return "", fmt.Errorf("unsupported agent CLI %q (choose claude, codex, or gemini)", value)
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
// visible in the configured profile arguments.
func Prepare(provider Provider, command string, args []string, env map[string]string, prompt string, trustWorkdir bool) Launch {
	provider = Resolve(provider, command)
	launch := Launch{
		Args: append([]string(nil), args...),
		Env:  cloneEnv(env),
	}
	switch provider {
	case Codex:
		// Codex accepts an initial prompt positionally while retaining its TUI.
		launch.Args = append(launch.Args, prompt)
		launch.PromptInjected = true
	case Gemini:
		// Gemini's -p is headless; --prompt-interactive starts the task and
		// keeps the session alive for later omo mail nudges and user input.
		launch.Args = append(launch.Args, "--prompt-interactive", prompt)
		launch.PromptInjected = true
		if trustWorkdir {
			launch.Env["GEMINI_CLI_TRUST_WORKSPACE"] = "true"
		}
	}
	return launch
}

func cloneEnv(env map[string]string) map[string]string {
	cloned := make(map[string]string, len(env)+1)
	for key, value := range env {
		cloned[key] = value
	}
	return cloned
}
