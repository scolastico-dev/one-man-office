package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/claudetrust"
)

const testSessionID = "0123456789abcdef0123456789abcdef"

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}

func TestBuildEnvLayers(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("OMO_AGENT_ID", "inherited")
	t.Setenv("OMO_TEST_BASE", "base")

	spec := Spec{
		SessionID:  testSessionID,
		CLI:        api.CLIClaude,
		AgentEnv:   map[string]string{"GIT_AUTHOR_NAME": "A", "GIT_COMMITTER_NAME": "${OMO_TEST_MISSING:-fallback-$OMO_TEST_BASE}"},
		ProfileEnv: map[string]string{"GIT_AUTHOR_NAME": "B", "OMO_AGENT_ID": "profile", "SECRET": "lit$eral"},
		AccountDir: "/accounts/one",
		Socket:     "/run/omo.sock",
		PathPrefix: "/opt/omo/bin",
	}
	full := effectiveEnv(mustBuildEnv(t, spec, map[string]string{"OMO_SOCKET": "launch"}))
	for key, want := range map[string]string{
		"GIT_AUTHOR_NAME":    "B",
		"GIT_COMMITTER_NAME": "fallback-base",
		"SECRET":             "lit$eral",
		"OMO_AGENT_ID":       testSessionID,
		"OMO_SOCKET":         "/run/omo.sock",
		"CLAUDE_CONFIG_DIR":  "/accounts/one",
		"TERM":               "xterm-256color",
	} {
		if full[key] != want {
			t.Errorf("%s = %q, want %q", key, full[key], want)
		}
	}
	if path := full["PATH"]; !strings.HasPrefix(path, "/opt/omo/bin"+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q, want it to start with the prefix", path)
	}

	accounts := map[string]string{api.CLIClaude: "CLAUDE_CONFIG_DIR", api.CLICodex: "CODEX_HOME", api.CLIGemini: "GEMINI_CLI_HOME"}
	for cli, variable := range accounts {
		got := envMap(mustBuildEnv(t, Spec{SessionID: testSessionID, CLI: cli, AccountDir: "/acc"}, nil))
		if got[variable] != "/acc" {
			t.Errorf("%s: %s = %q, want /acc", cli, variable, got[variable])
		}
	}
	custom := envMap(mustBuildEnv(t, Spec{SessionID: testSessionID, CLI: api.CLICustom, AccountDir: "/acc"}, nil))
	for _, variable := range accounts {
		if _, ok := custom[variable]; ok {
			t.Errorf("custom CLI got %s", variable)
		}
	}
	own := envMap(mustBuildEnv(t, Spec{SessionID: testSessionID, CLI: api.CLIClaude, AccountDir: "/acc", ProfileEnv: map[string]string{"CLAUDE_CONFIG_DIR": "/mine"}}, nil))
	if own["CLAUDE_CONFIG_DIR"] != "/mine" {
		t.Errorf("profile CLAUDE_CONFIG_DIR = %q, want /mine", own["CLAUDE_CONFIG_DIR"])
	}
	term := envMap(mustBuildEnv(t, Spec{SessionID: testSessionID, AgentEnv: map[string]string{"TERM": "screen"}}, nil))
	if term["TERM"] != "screen" {
		t.Errorf("configured TERM = %q, want screen", term["TERM"])
	}
	agentPath := envMap(mustBuildEnv(t, Spec{SessionID: testSessionID, PathPrefix: "/p", ModelEnv: map[string]string{"PATH": "/model"}}, nil))
	if agentPath["PATH"] != "/p"+string(os.PathListSeparator)+"/model" {
		t.Errorf("PATH with a model PATH = %q", agentPath["PATH"])
	}
}

func TestSpecMustNotExposeSessionID(t *testing.T) {
	base := Spec{SessionID: testSessionID, Name: "developer-ada", Command: "agent", LogPath: "/logs/developer-ada.log"}
	if err := checkSpec(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Spec){
		"args":    func(s *Spec) { s.Args = []string{"--id", testSessionID} },
		"log":     func(s *Spec) { s.LogPath = "/logs/" + testSessionID + ".log" },
		"prompt":  func(s *Spec) { s.StartPrompt = "you are " + testSessionID },
		"dir":     func(s *Spec) { s.Dir = "/w/" + testSessionID },
		"command": func(s *Spec) { s.Command = testSessionID },
	} {
		spec := base
		mutate(&spec)
		err := checkSpec(spec)
		if !errors.Is(err, ErrSessionIDExposed) {
			t.Errorf("%s: err = %v, want ErrSessionIDExposed", name, err)
		}
		if err != nil && strings.Contains(err.Error(), testSessionID) {
			t.Errorf("%s: error message contains the session id", name)
		}
	}
}

func TestResolveDeliveryDefaults(t *testing.T) {
	d := ResolveDelivery(api.PromptDelivery{})
	if d != (Delivery{Inject: true, Delay: 3 * time.Second, RetryWait: 30 * time.Second, Retries: 3,
		EchoWait: 5 * time.Second, SubmitCheck: 3 * time.Second, SubmitRetries: 3}) {
		t.Fatalf("defaults = %+v", d)
	}
	if got := d.ReadyTimeout(); got != 2*time.Minute+33*time.Second {
		t.Fatalf("ReadyTimeout = %v, want 2m33s", got)
	}
	if got := (Delivery{}).ReadyTimeout(); got != 2*time.Minute {
		t.Fatalf("minimum ReadyTimeout = %v, want 2m", got)
	}
	off, delay, retries, wait := false, api.Duration(time.Second), 0, api.Duration(5*time.Second)
	got := ResolveDelivery(api.PromptDelivery{Inject: &off, Delay: &delay, Retries: &retries, RetryWait: &wait})
	if got != (Delivery{Inject: false, Delay: time.Second, RetryWait: 5 * time.Second, Retries: 0,
		EchoWait: 5 * time.Second, SubmitCheck: 3 * time.Second, SubmitRetries: 3}) {
		t.Fatalf("explicit = %+v", got)
	}
}

func mustBuildEnv(t *testing.T, spec Spec, launchEnv map[string]string) []string {
	t.Helper()
	env, err := BuildEnv(spec, launchEnv)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestEffectiveEnvSelectsTheSameClaudeAccountAsStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	account := t.TempDir()
	spec := Spec{SessionID: testSessionID, CLI: api.CLIClaude, AccountDir: account, Dir: t.TempDir(), ProfileEnv: map[string]string{"PROFILE_VALUE": "p"}}
	env, err := EffectiveEnv(spec)
	if err != nil {
		t.Fatal(err)
	}
	if env["CLAUDE_CONFIG_DIR"] != account || env["PROFILE_VALUE"] != "p" || env["HOME"] != home {
		t.Fatalf("effective environment = %v", env)
	}
	if _, ok := env["OMO_AGENT_ID"]; ok {
		t.Fatal("EffectiveEnv returns the session id")
	}
	for key, value := range env {
		if strings.Contains(value, testSessionID) {
			t.Fatalf("%s carries the session id", key)
		}
	}
	// What Start trusts with this environment, ForgetForEnv removes.
	if err := claudetrust.EnsureForEnv(env, spec.Dir); err != nil {
		t.Fatal(err)
	}
	if err := claudetrust.ForgetForEnv(env, spec.Dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(account, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), spec.Dir) {
		t.Fatalf("folder still trusted: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("trust written below HOME instead of the account folder")
	}
}

// Finding the account to forget a folder's trust in runs none of the agent
// environment's commands: a variable set by one is left out.
func TestEffectiveEnvRunsNoCommand(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	marker := filepath.Join(t.TempDir(), "ran")
	spec := Spec{SessionID: testSessionID, CLI: api.CLIClaude, Dir: t.TempDir(), AgentEnv: map[string]string{
		"SECRET":  "`touch " + marker + "; echo s`",
		"DERIVED": "x${SECRET}",
		"PLAIN":   "plain",
	}}
	env, err := EffectiveEnv(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command of the agent environment ran")
	}
	if _, ok := env["SECRET"]; ok || env["DERIVED"] != "x" || env["PLAIN"] != "plain" {
		t.Fatalf("SECRET %q DERIVED %q PLAIN %q", env["SECRET"], env["DERIVED"], env["PLAIN"])
	}
}
