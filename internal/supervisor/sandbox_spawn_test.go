package supervisor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestSandboxedClaudeUsesSelectedAccountThroughPrivateHome(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	accounts := make([]string, 2)
	for i := range accounts {
		account, err := os.MkdirTemp(home, ".omo-claude-account-test-")
		if err != nil {
			t.Skipf("cannot create home account fixture: %v", err)
		}
		defer os.RemoveAll(account)
		accounts[i] = account
		if err := os.WriteFile(filepath.Join(account, "marker"), []byte(fmt.Sprintf("account-%d", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i, account := range accounts {
		t.Run(fmt.Sprintf("account-%d", i), func(t *testing.T) {
			o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
			other := accounts[1-i]
			command := filepath.Join(o.Dir, "claude-agent")
			script := fmt.Sprintf("#!/bin/sh\n[ \"$CLAUDE_CONFIG_DIR\" = \"$HOME/.claude\" ] || exit 41\n[ \"$CLAUDE_SECURESTORAGE_CONFIG_DIR\" = %q ] || exit 42\n[ \"$(cat \"$CLAUDE_CONFIG_DIR/marker\")\" = account-%d ] || exit 43\nif cat %q >/dev/null 2>&1; then exit 44; fi\nprintf probe > \"$CLAUDE_CONFIG_DIR/probe\" || exit 45\nexec %q fake-agent --scenario %q\n", account, i, filepath.Join(other, "marker"), omoBin, filepath.Join(o.Dir, "freelancer.scenario"))
			if err := os.WriteFile(command, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			o.Sup.Cfg.Models["sandboxed"] = config.Profile{
				Cmd: command, Provider: agentcli.Claude,
				Env:     map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": account},
				Sandbox: &config.Sandbox{Enabled: true},
			}
			name, err := o.Sup.Spawn("freelancer", "sandboxed", 0, o.Dir, "work")
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Second, "selected Claude account ready over office socket", func() bool { return agentState(t, o, name) == "waiting" })
			if _, err := os.Stat(filepath.Join(account, "probe")); err != nil {
				t.Fatalf("selected account was not writable: %v", err)
			}
		})
	}
}

func TestClaudeSandboxEnvironmentPreservesKeychainIdentity(t *testing.T) {
	const privateHome = "/private/home"
	cases := []struct {
		name, goos, configLink, want string
		env                          map[string]string
	}{
		{"darwin config only", "darwin", ".claude", "", map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/config"}},
		{"darwin selected secure account", "darwin", ".claude", "CLAUDE_CONFIG_DIR=/private/home/.claude", map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/config", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/accounts/secure"}},
		{"darwin explicit default secure account", "darwin", ".claude-config", "CLAUDE_CONFIG_DIR=/private/home/.claude-config", map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/config", "CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}},
		{"linux config only", "linux", ".claude", "CLAUDE_CONFIG_DIR=/private/home/.claude", map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/config"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(claudeSandboxEnvironment(tc.env, privateHome, tc.configLink, tc.goos), ",")
			if got != tc.want {
				t.Fatalf("sandbox env = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSandboxedClaudeKeepsSeparateSecureStoragePath(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	account, err := os.MkdirTemp(home, ".omo-claude-config-test-")
	if err != nil {
		t.Skipf("cannot create home account fixture: %v", err)
	}
	defer os.RemoveAll(account)
	secure, err := os.MkdirTemp(home, ".omo-claude-secure-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(secure)
	secret, err := os.CreateTemp(home, ".omo-claude-unrelated-test-")
	if err != nil {
		t.Fatal(err)
	}
	secret.Close()
	defer os.Remove(secret.Name())
	for _, path := range []string{filepath.Join(account, "config-marker"), filepath.Join(secure, "secure-marker")} {
		if err := os.WriteFile(path, []byte("available"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	command := filepath.Join(o.Dir, "claude-agent")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$CLAUDE_CONFIG_DIR\" = \"$HOME/.claude\" ] || exit 41\n[ \"$CLAUDE_SECURESTORAGE_CONFIG_DIR\" = %q ] || exit 42\ncat \"$CLAUDE_CONFIG_DIR/config-marker\" >/dev/null || exit 43\ncat \"$CLAUDE_SECURESTORAGE_CONFIG_DIR/secure-marker\" >/dev/null || exit 44\nif cat %q >/dev/null 2>&1; then exit 45; fi\nprintf probe > \"$CLAUDE_SECURESTORAGE_CONFIG_DIR/probe\" || exit 46\nexec %q fake-agent --scenario %q\n", secure, secret.Name(), omoBin, filepath.Join(o.Dir, "freelancer.scenario"))
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	o.Sup.Cfg.Models["sandboxed"] = config.Profile{
		Cmd: command, Provider: agentcli.Claude,
		Env:     map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": secure},
		Sandbox: &config.Sandbox{Enabled: true},
	}
	name, err := o.Sup.Spawn("freelancer", "sandboxed", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "separate Claude secure storage ready over office socket", func() bool { return agentState(t, o, name) == "waiting" })
	if _, err := os.Stat(filepath.Join(secure, "probe")); err != nil {
		t.Fatalf("secure storage was not writable: %v", err)
	}
}

func TestSandboxedHomeInstalledOmoCanCallReadyWithoutHomeSecrets(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.MkdirTemp(home, ".omo-executable-test-")
	if err != nil {
		t.Skipf("cannot create home fixture: %v", err)
	}
	defer os.RemoveAll(fixture)
	wrapperDir := filepath.Join(fixture, "wrapper")
	profileDir := filepath.Join(fixture, "profile")
	for _, dir := range []string{wrapperDir, profileDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	wrapper := filepath.Join(wrapperDir, "omo")
	source, err := os.Open(omoBin)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(wrapper, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(fixture, "secret")
	if err := os.WriteFile(secret, []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(profileDir, "agent")
	script := fmt.Sprintf("#!/bin/sh\nif cat %q >/dev/null 2>&1; then exit 42; fi\nexec %q fake-agent --scenario %q\n", secret, wrapper, filepath.Join(o.Dir, "freelancer.scenario"))
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return wrapper, nil }
	defer func() { sandboxExecutable = previous }()
	o.Sup.Cfg.Models["sandboxed"] = config.Profile{Cmd: command, Sandbox: &config.Sandbox{Enabled: true}}
	name, err := o.Sup.Spawn("freelancer", "sandboxed", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "home-installed omo ready over socket", func() bool { return agentState(t, o, name) == "waiting" })
}

func TestSandboxedSpawnUsesProfilePATHForExecutable(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	parentBin, err := os.MkdirTemp(home, ".omo-parent-bin-")
	if err != nil {
		t.Skipf("cannot create home fixture: %v", err)
	}
	defer os.RemoveAll(parentBin)
	sessionBin, err := os.MkdirTemp(home, ".omo-session-bin-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sessionBin)
	const command = "omo-sandbox-path-test"
	if err := os.WriteFile(filepath.Join(parentBin, command), []byte("#!/bin/sh\nexit 17\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %q fake-agent --scenario %q\n", omoBin, filepath.Join(o.Dir, "freelancer.scenario"))
	if err := os.WriteFile(filepath.Join(sessionBin, command), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", parentBin+string(os.PathListSeparator)+path)
	profile := config.Profile{Cmd: command, Env: map[string]string{"PATH": sessionBin + string(os.PathListSeparator) + path}, Sandbox: &config.Sandbox{Enabled: true}}
	o.Sup.Cfg.Models["sandboxed"] = profile
	name, err := o.Sup.Spawn("freelancer", "sandboxed", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "agent from profile PATH ready", func() bool { return agentState(t, o, name) == "waiting" })
}

func TestSandboxedSpawnRejectsInvalidPolicyBeforeAgentLaunch(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	profile := o.Sup.Cfg.Models["freelancer"]
	profile.Sandbox = &config.Sandbox{Enabled: true, ReadPaths: []string{filepath.Join(o.Dir, "missing")}}
	o.Sup.Cfg.Models["freelancer"] = profile
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("invalid policy spawn = %v", err)
	}
}

func TestSandboxedEarlyExitUsesConfiguredFailover(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	good := o.Sup.Cfg.Models["freelancer"]
	o.Sup.Cfg.Models["good"] = good
	o.Sup.Cfg.Models["broken"] = config.Profile{Cmd: "/bin/false", Sandbox: &config.Sandbox{Enabled: true}}
	o.Sup.Cfg.Roles["freelancer"] = config.RoleModels{Models: []string{"broken", "good"}, Assignment: config.AssignmentFailover}
	if _, err := o.Sup.SpawnConfiguredRole("freelancer", 0, o.Dir, "work", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "sandbox failure failover", func() bool {
		agents, err := db.LivingAgents(o.DB)
		if err != nil {
			return false
		}
		for _, agent := range agents {
			if agent.Profile == "good" && agent.State == "waiting" {
				return true
			}
		}
		return false
	})
}

func TestSandboxPreparationFailureUsesConfiguredFailover(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	o.Sup.Cfg.Models["good"] = o.Sup.Cfg.Models["freelancer"]
	o.Sup.Cfg.Models["broken"] = config.Profile{Cmd: "/bin/true", Sandbox: &config.Sandbox{Enabled: true, ReadPaths: []string{filepath.Join(o.Dir, "missing")}}}
	o.Sup.Cfg.Roles["freelancer"] = config.RoleModels{Models: []string{"broken", "good"}, Assignment: config.AssignmentFailover}
	if _, err := o.Sup.SpawnConfiguredRole("freelancer", 0, o.Dir, "work", 0); err != nil {
		t.Fatalf("configured failover: %v", err)
	}
	waitFor(t, 5*time.Second, "sandbox preparation failover", func() bool {
		agents, err := db.LivingAgents(o.DB)
		if err != nil {
			return false
		}
		for _, agent := range agents {
			if agent.Profile == "good" && agent.State == "waiting" {
				return true
			}
		}
		return false
	})
}

func TestSmokeAlarmSandboxFailureReleasesSpawnLockBeforeFailover(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"smokealarm": "ready\nwait\n"})
	o.Sup.Cfg.Models["good"] = o.Sup.Cfg.Models["smokealarm"]
	o.Sup.Cfg.Models["broken"] = config.Profile{Cmd: "/bin/true", Sandbox: &config.Sandbox{Enabled: true, ReadPaths: []string{filepath.Join(o.Dir, "missing")}}}
	o.Sup.Cfg.Roles["smokealarm"] = config.RoleModels{Models: []string{"broken", "good"}, Assignment: config.AssignmentFailover}
	result := make(chan error, 1)
	go func() { _, err := o.Sup.SpawnConfiguredRole("smokealarm", 0, o.Dir, "work", 0); result <- err }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("smoke-alarm failover deadlocked")
	}
}

func TestSandboxedSpawnCanReachReadyOverOfficeSocket(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	profile := o.Sup.Cfg.Models["freelancer"]
	profile.Sandbox = &config.Sandbox{Enabled: true}
	o.Sup.Cfg.Models["freelancer"] = profile
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "sandboxed agent ready", func() bool { return agentState(t, o, name) == "waiting" })
}

func TestSandboxedFakeAgentCannotWriteOfficeButCanComplete(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	o := newOffice(t, map[string]string{"product_manager": "ready\nshell|if touch denied-marker; then exit 17; fi\ndone|read-only round complete\n"})
	o.Sup.Cfg.Logs.Keep = -1
	profile := o.Sup.Cfg.Models["product_manager"]
	profile.Sandbox = &config.Sandbox{Enabled: true}
	o.Sup.Cfg.Models["product_manager"] = profile
	name, err := o.Sup.Spawn("product_manager", "product_manager", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "sandboxed fake agent completed ready and done", func() bool {
		state := agentState(t, o, name)
		return state == "done" || state == "dead"
	})
	events, err := db.AllEvents(o.DB)
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	for _, event := range events {
		if event.Kind == "agent_done" && event.Agent == name && event.Detail == "read-only round complete" {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("sandboxed fake agent did not call done")
	}
	if _, err := os.Stat(filepath.Join(o.Dir, ".omo", "storage", "denied-marker")); !os.IsNotExist(err) {
		t.Fatalf("read-only office marker unexpectedly exists: %v", err)
	}
	logs, err := filepath.Glob(filepath.Join(o.Dir, ".omo", "logs", "*-"+name+".log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("expected one session transcript: paths=%v err=%v", logs, err)
	}
	log, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "Permission denied") {
		t.Fatalf("touch did not report a denied write: %s", log)
	}
}

func TestSandboxedCodexUsesSharedAgentStatePath(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.MkdirTemp(home, ".omo-codex-shared-test-")
	if err != nil {
		t.Skipf("cannot create home state fixture: %v", err)
	}
	defer os.RemoveAll(state)
	if err := os.WriteFile(filepath.Join(state, "marker"), []byte("selected"), 0600); err != nil {
		t.Fatal(err)
	}
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	o.Sup.Cfg.Agents.Env = map[string]string{"CODEX_HOME": state}
	command := filepath.Join(o.Dir, "codex-agent")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$CODEX_HOME\" = \"$HOME/.codex\" ] || exit 41\n[ \"$(cat \"$CODEX_HOME/marker\")\" = selected ] || exit 42\nprintf probe > \"$CODEX_HOME/probe\" || exit 43\nexec %q fake-agent --scenario %q\n", omoBin, filepath.Join(o.Dir, "freelancer.scenario"))
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	o.Sup.Cfg.Models["sandboxed"] = config.Profile{Cmd: command, Provider: agentcli.Codex, Sandbox: &config.Sandbox{Enabled: true}}
	name, err := o.Sup.Spawn("freelancer", "sandboxed", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "sandboxed Codex with shared state reached ready", func() bool { return agentState(t, o, name) == "waiting" })
	if _, err := os.Stat(filepath.Join(state, "probe")); err != nil {
		t.Fatalf("shared Codex state was not writable: %v", err)
	}
}

func TestSandboxedClaudeUsesSharedAgentAccountPaths(t *testing.T) {
	previous := sandboxExecutable
	sandboxExecutable = func() (string, error) { return omoBin, nil }
	defer func() { sandboxExecutable = previous }()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	configDir, err := os.MkdirTemp(home, ".omo-claude-shared-config-")
	if err != nil {
		t.Skipf("cannot create home config fixture: %v", err)
	}
	defer os.RemoveAll(configDir)
	secureDir, err := os.MkdirTemp(home, ".omo-claude-shared-secure-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(secureDir)
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	o.Sup.Cfg.Agents.Env = map[string]string{"CLAUDE_CONFIG_DIR": configDir, "CLAUDE_SECURESTORAGE_CONFIG_DIR": secureDir}
	command := filepath.Join(o.Dir, "claude-agent")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$CLAUDE_CONFIG_DIR\" = \"$HOME/.claude\" ] || exit 41\n[ \"$CLAUDE_SECURESTORAGE_CONFIG_DIR\" = %q ] || exit 42\nprintf probe > \"$CLAUDE_CONFIG_DIR/probe\" || exit 43\nprintf probe > \"$CLAUDE_SECURESTORAGE_CONFIG_DIR/probe\" || exit 44\nexec %q fake-agent --scenario %q\n", secureDir, omoBin, filepath.Join(o.Dir, "freelancer.scenario"))
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	o.Sup.Cfg.Models["sandboxed"] = config.Profile{Cmd: command, Provider: agentcli.Claude, Sandbox: &config.Sandbox{Enabled: true}}
	name, err := o.Sup.Spawn("freelancer", "sandboxed", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "sandboxed Claude with shared accounts reached ready", func() bool { return agentState(t, o, name) == "waiting" })
	for _, dir := range []string{configDir, secureDir} {
		if _, err := os.Stat(filepath.Join(dir, "probe")); err != nil {
			t.Fatalf("shared Claude account %q was not writable: %v", dir, err)
		}
	}
}
