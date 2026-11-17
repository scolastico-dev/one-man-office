package agentcli

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDetectInstalledUsesSupportedPriority(t *testing.T) {
	tests := []struct {
		name      string
		available map[string]bool
		want      Provider
		wantOK    bool
	}{
		{"prefers claude", map[string]bool{"claude": true, "codex": true, "gemini": true}, Claude, true},
		{"falls back to codex", map[string]bool{"codex": true, "gemini": true}, Codex, true},
		{"falls back to gemini", map[string]bool{"gemini": true}, Gemini, true},
		{"none installed", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := detectInstalled(func(command string) (string, error) {
				if tt.available[command] {
					return "/tools/" + command, nil
				}
				return "", errors.New("not found")
			})
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("detectInstalled() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestDetectAllInstalledReturnsEverySupportedCLIInPriorityOrder(t *testing.T) {
	got := detectAllInstalled(func(command string) (string, error) {
		if command == "claude" || command == "gemini" {
			return "/tools/" + command, nil
		}
		return "", errors.New("not found")
	})
	want := []Provider{Claude, Gemini}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detectAllInstalled() = %v, want %v", got, want)
	}
}

func TestDetectAndResolve(t *testing.T) {
	tests := []struct {
		cmd  string
		want Provider
	}{
		{"claude", Claude},
		{"/usr/local/bin/codex", Codex},
		{"C:\\Tools\\gemini.exe", Gemini},
		{"/tmp/claude-stub", Claude},
		{"custom-agent", ""},
	}
	for _, tt := range tests {
		if got := Detect(tt.cmd); got != tt.want {
			t.Errorf("Detect(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
	if got := Resolve(Gemini, "/tmp/custom-agent"); got != Gemini {
		t.Fatalf("explicit provider lost: %q", got)
	}
}

func TestPrepareInjectsReliableInitialPrompts(t *testing.T) {
	codex := Prepare(Codex, "wrapper", []string{"--no-alt-screen"}, nil, "/work/repo", "run omo ready", true, true, "")
	if !codex.PromptInjected || !reflect.DeepEqual(codex.Args, []string{"--no-alt-screen", "-c", `projects={"/work/repo"={trust_level="trusted"}}`, "--dangerously-bypass-hook-trust", "run omo ready"}) {
		t.Fatalf("codex launch = %+v", codex)
	}

	gemini := Prepare(Gemini, "wrapper", []string{"--yolo"}, map[string]string{"KEEP": "yes"}, "/work/repo", "run omo ready", true, true, "")
	if !gemini.PromptInjected || !reflect.DeepEqual(gemini.Args, []string{"--yolo", "--prompt-interactive", "run omo ready"}) {
		t.Fatalf("gemini launch = %+v", gemini)
	}
	if gemini.Env["GEMINI_CLI_TRUST_WORKSPACE"] != "true" || gemini.Env["KEEP"] != "yes" {
		t.Fatalf("gemini env = %v", gemini.Env)
	}

	claude := Prepare(Claude, "wrapper", []string{"--model", "sonnet"}, nil, "/work/repo", "run omo ready", true, true, "")
	if claude.PromptInjected || !reflect.DeepEqual(claude.Args, []string{"--model", "sonnet"}) {
		t.Fatalf("claude launch = %+v", claude)
	}
}

func TestPrepareSubstitutesPromptArgumentsIndependently(t *testing.T) {
	launch := Prepare(Codex, "codex", []string{"--initial=%prompt%"}, nil, "/work/repo", "run omo ready", false, false, "")
	if !launch.PromptInjected || !reflect.DeepEqual(launch.Args, []string{"--initial=run omo ready"}) {
		t.Fatalf("explicit prompt argument = %+v", launch)
	}

	launch = Prepare(Codex, "codex", []string{"--no-alt-screen"}, nil, "/work/repo", "run omo ready", false, false, "")
	if launch.PromptInjected || !reflect.DeepEqual(launch.Args, []string{"--no-alt-screen"}) {
		t.Fatalf("disabled automatic injection = %+v", launch)
	}
}

func TestPrepareDoesNotTrustGeminiWhenDisabled(t *testing.T) {
	launch := Prepare(Gemini, "gemini", nil, map[string]string{}, "/work/repo", "ready", false, true, "")
	if _, ok := launch.Env["GEMINI_CLI_TRUST_WORKSPACE"]; ok {
		t.Fatal("trust_workdirs: false set Gemini's trust override")
	}
}

func TestPrepareDoesNotBypassCodexHookTrustWhenDisabled(t *testing.T) {
	launch := Prepare(Codex, "codex", nil, nil, "/work/repo", "ready", false, true, "")
	if hasArg(launch.Args, "--dangerously-bypass-hook-trust") {
		t.Fatal("trust_workdirs: false bypassed Codex hook trust")
	}
	if hasArg(launch.Args, "-c") {
		t.Fatal("trust_workdirs: false bypassed Codex workspace trust")
	}
}

func TestPrepareSetsUsableTermUnlessProfileOverridesIt(t *testing.T) {
	launch := Prepare(Codex, "codex", nil, nil, "/work/repo", "ready", true, true, "")
	if launch.Env["TERM"] != "xterm-256color" {
		t.Fatalf("TERM = %q", launch.Env["TERM"])
	}
	launch = Prepare(Gemini, "gemini", nil, map[string]string{"TERM": "screen-256color"}, "/work/repo", "ready", true, true, "")
	if launch.Env["TERM"] != "screen-256color" {
		t.Fatalf("explicit TERM overridden: %q", launch.Env["TERM"])
	}
}

func TestDetectRecognizesAgy(t *testing.T) {
	if got := Detect("/home/u/.local/bin/agy"); got != Agy {
		t.Fatalf("Detect(agy) = %q", got)
	}
	if p, err := Parse(" AGY "); err != nil || p != Agy {
		t.Fatalf("Parse(agy) = %q, %v", p, err)
	}
}

func TestPrepareStartsAgyInteractivelyInATrustedFolder(t *testing.T) {
	args := []string{"--model", "gemini-3.8-flash-high", "--dangerously-skip-permissions"}
	launch := Prepare(Agy, "agy", args, map[string]string{"KEEP": "yes"}, `/work/"odd" repo`, "run omo ready", true, true, "")
	want := append(append([]string(nil), args...),
		"--settings", `{"trustedWorkspaces":["/work/\"odd\" repo"]}`,
		"--prompt-interactive", "run omo ready")
	if !launch.PromptInjected || !reflect.DeepEqual(launch.Args, want) {
		t.Fatalf("agy launch = %q", launch.Args)
	}
	if launch.Env["KEEP"] != "yes" || launch.Env["TERM"] != "xterm-256color" {
		t.Fatalf("agy env = %v", launch.Env)
	}
}

func TestPrepareDoesNotTrustAgyWhenDisabled(t *testing.T) {
	launch := Prepare(Agy, "agy", nil, nil, "/work/repo", "ready", false, false, "")
	if hasArg(launch.Args, "--settings") || launch.PromptInjected || len(launch.Args) != 0 {
		t.Fatalf("agy launch without trust and injection = %q", launch.Args)
	}
}

// The bridge entry each program gets, pinned in the form its documentation
// gives:
//
//   - Claude Code, `claude --help`: "--mcp-config <configs...>  Load MCP
//     servers from JSON files or strings (space-separated)", the JSON being
//     a .mcp.json document: {"mcpServers": {"<name>": {"type": "stdio",
//     "command": "...", "args": [...]}}} (code.claude.com/docs/en/mcp).
//   - Codex, `codex --help`: "-c, --config <key=value>  Override a
//     configuration value that would otherwise be loaded from
//     `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override
//     nested values. The `value` portion is parsed as TOML.", with the keys
//     of [mcp_servers.<id>] from the configuration reference: command
//     (string), args (array of strings), env_vars (array of strings: names
//     of variables passed through to the server).
func TestBridgeArgsAsDocumented(t *testing.T) {
	omo := `/opt/omo "x"\bin/omo`
	claude := BridgeArgs(Claude, omo)
	if want := []string{"--mcp-config", `{"mcpServers":{"omo":{"type":"stdio","command":"/opt/omo \"x\"\\bin/omo","args":["mcp","stdio"]}}}`}; !reflect.DeepEqual(claude, want) {
		t.Errorf("claude = %q\nwant     %q", claude, want)
	}
	codex := BridgeArgs(Codex, omo)
	if want := []string{
		"-c", `mcp_servers.omo.command="/opt/omo \"x\"\\bin/omo"`,
		"-c", `mcp_servers.omo.args=["mcp","stdio"]`,
		"-c", `mcp_servers.omo.env_vars=["OMO_AGENT_ID","OMO_SOCKET"]`,
	}; !reflect.DeepEqual(codex, want) {
		t.Errorf("codex = %q\nwant    %q", codex, want)
	}
	for _, p := range []Provider{Agy, Gemini, ""} {
		if got := BridgeArgs(p, omo); got != nil {
			t.Errorf("%q has no per-session MCP entry, got %q", p, got)
		}
	}
}

// The entry goes where the program reads it: Claude's after every other
// argument (its --mcp-config takes several values), Codex's before the
// prompt it takes as an argument.
func TestPrepareAddsTheBridge(t *testing.T) {
	claude := Prepare(Claude, "claude", []string{"--model", "sonnet"}, nil, "/work/repo", "run omo ready", true, true, "/bin/omo")
	if want := append([]string{"--model", "sonnet"}, BridgeArgs(Claude, "/bin/omo")...); !reflect.DeepEqual(claude.Args, want) {
		t.Errorf("claude args = %q", claude.Args)
	}
	codex := Prepare(Codex, "codex", []string{"--no-alt-screen"}, nil, "/work/repo", "run omo ready", true, true, "/bin/omo")
	want := append([]string{"--no-alt-screen", "-c", `projects={"/work/repo"={trust_level="trusted"}}`, "--dangerously-bypass-hook-trust"}, BridgeArgs(Codex, "/bin/omo")...)
	if want = append(want, "run omo ready"); !reflect.DeepEqual(codex.Args, want) {
		t.Errorf("codex args = %q", codex.Args)
	}
	for _, p := range []Provider{Agy, Gemini} {
		with := Prepare(p, string(p), []string{"--x"}, nil, "/work/repo", "ready", true, true, "/bin/omo")
		without := Prepare(p, string(p), []string{"--x"}, nil, "/work/repo", "ready", true, true, "")
		if !reflect.DeepEqual(with, without) {
			t.Errorf("%s got a per-session entry: %q", p, with.Args)
		}
	}
	if none := Prepare(Codex, "codex", nil, nil, "/work/repo", "ready", false, true, ""); slices.ContainsFunc(none.Args, func(a string) bool { return strings.Contains(a, "mcp_servers") }) {
		t.Errorf("an entry without a bridge: %q", none.Args)
	}
}
