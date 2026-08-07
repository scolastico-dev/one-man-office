package agentcli

import (
	"reflect"
	"testing"
)

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
	codex := Prepare(Codex, "wrapper", []string{"--no-alt-screen"}, nil, "run omo ready", true)
	if !codex.PromptInjected || !reflect.DeepEqual(codex.Args, []string{"--no-alt-screen", "run omo ready"}) {
		t.Fatalf("codex launch = %+v", codex)
	}

	gemini := Prepare(Gemini, "wrapper", []string{"--yolo"}, map[string]string{"KEEP": "yes"}, "run omo ready", true)
	if !gemini.PromptInjected || !reflect.DeepEqual(gemini.Args, []string{"--yolo", "--prompt-interactive", "run omo ready"}) {
		t.Fatalf("gemini launch = %+v", gemini)
	}
	if gemini.Env["GEMINI_CLI_TRUST_WORKSPACE"] != "true" || gemini.Env["KEEP"] != "yes" {
		t.Fatalf("gemini env = %v", gemini.Env)
	}

	claude := Prepare(Claude, "wrapper", []string{"--model", "sonnet"}, nil, "run omo ready", true)
	if claude.PromptInjected || !reflect.DeepEqual(claude.Args, []string{"--model", "sonnet"}) {
		t.Fatalf("claude launch = %+v", claude)
	}
}

func TestPrepareDoesNotTrustGeminiWhenDisabled(t *testing.T) {
	launch := Prepare(Gemini, "gemini", nil, map[string]string{}, "ready", false)
	if _, ok := launch.Env["GEMINI_CLI_TRUST_WORKSPACE"]; ok {
		t.Fatal("trust_workdirs: false set Gemini's trust override")
	}
}
