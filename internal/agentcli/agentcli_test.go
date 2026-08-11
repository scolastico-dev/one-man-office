package agentcli

import (
	"errors"
	"reflect"
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
	codex := Prepare(Codex, "wrapper", []string{"--no-alt-screen"}, nil, "/work/repo", "run omo ready", true, true)
	if !codex.PromptInjected || !reflect.DeepEqual(codex.Args, []string{"--no-alt-screen", "-c", `projects={"/work/repo"={trust_level="trusted"}}`, "--dangerously-bypass-hook-trust", "run omo ready"}) {
		t.Fatalf("codex launch = %+v", codex)
	}

	gemini := Prepare(Gemini, "wrapper", []string{"--yolo"}, map[string]string{"KEEP": "yes"}, "/work/repo", "run omo ready", true, true)
	if !gemini.PromptInjected || !reflect.DeepEqual(gemini.Args, []string{"--yolo", "--prompt-interactive", "run omo ready"}) {
		t.Fatalf("gemini launch = %+v", gemini)
	}
	if gemini.Env["GEMINI_CLI_TRUST_WORKSPACE"] != "true" || gemini.Env["KEEP"] != "yes" {
		t.Fatalf("gemini env = %v", gemini.Env)
	}

	claude := Prepare(Claude, "wrapper", []string{"--model", "sonnet"}, nil, "/work/repo", "run omo ready", true, true)
	if claude.PromptInjected || !reflect.DeepEqual(claude.Args, []string{"--model", "sonnet"}) {
		t.Fatalf("claude launch = %+v", claude)
	}
}

func TestPrepareSubstitutesPromptArgumentsIndependently(t *testing.T) {
	launch := Prepare(Codex, "codex", []string{"--initial=%prompt%"}, nil, "/work/repo", "run omo ready", false, false)
	if !launch.PromptInjected || !reflect.DeepEqual(launch.Args, []string{"--initial=run omo ready"}) {
		t.Fatalf("explicit prompt argument = %+v", launch)
	}

	launch = Prepare(Codex, "codex", []string{"--no-alt-screen"}, nil, "/work/repo", "run omo ready", false, false)
	if launch.PromptInjected || !reflect.DeepEqual(launch.Args, []string{"--no-alt-screen"}) {
		t.Fatalf("disabled automatic injection = %+v", launch)
	}
}

func TestPrepareDoesNotTrustGeminiWhenDisabled(t *testing.T) {
	launch := Prepare(Gemini, "gemini", nil, map[string]string{}, "/work/repo", "ready", false, true)
	if _, ok := launch.Env["GEMINI_CLI_TRUST_WORKSPACE"]; ok {
		t.Fatal("trust_workdirs: false set Gemini's trust override")
	}
}

func TestPrepareDoesNotBypassCodexHookTrustWhenDisabled(t *testing.T) {
	launch := Prepare(Codex, "codex", nil, nil, "/work/repo", "ready", false, true)
	if hasArg(launch.Args, "--dangerously-bypass-hook-trust") {
		t.Fatal("trust_workdirs: false bypassed Codex hook trust")
	}
	if hasArg(launch.Args, "-c") {
		t.Fatal("trust_workdirs: false bypassed Codex workspace trust")
	}
}

func TestPrepareSetsUsableTermUnlessProfileOverridesIt(t *testing.T) {
	launch := Prepare(Codex, "codex", nil, nil, "/work/repo", "ready", true, true)
	if launch.Env["TERM"] != "xterm-256color" {
		t.Fatalf("TERM = %q", launch.Env["TERM"])
	}
	launch = Prepare(Gemini, "gemini", nil, map[string]string{"TERM": "screen-256color"}, "/work/repo", "ready", true, true)
	if launch.Env["TERM"] != "screen-256color" {
		t.Fatalf("explicit TERM overridden: %q", launch.Env["TERM"])
	}
}
