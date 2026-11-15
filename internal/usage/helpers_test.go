package usage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// readScript is a script that reads path and fails with what it got.
func readScript(path string) string {
	return `function usage()
		local text, err = omo.read_file(` + luaQuote(path) + `)
		if text then error("read " .. #text .. " bytes") end
		error("refused: " .. err)
	end`
}

func TestRunReadsBoundedFiles(t *testing.T) {
	cleanEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "small.json"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(home, "big")
	if err := os.WriteFile(big, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"~/small.json":                    "read 5 bytes",
		filepath.Join(home, "small.json"): "read 5 bytes",
		big:                               "refused: file exceeds 1048576 bytes",
		filepath.Join(home, "missing"):    "refused: file not found",
		home:                              "refused: file is not a regular file",
		"small.json":                      "refused: path must be absolute or start with ~/",
		"~user/small.json":                "refused: path must be absolute or start with ~/",
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(home, "link")
		if err := os.Symlink(filepath.Join(home, "small.json"), link); err != nil {
			t.Fatal(err)
		}
		cases[link] = "refused: file is a link"
	}
	// A NUL byte, written as a Lua escape.
	nul := strings.Replace(readScript("/x"), `"/x"`, `"/x\0y"`, 1)
	if _, err := runScript(t, nul, RunOptions{Home: home}); err == nil || !strings.HasSuffix(err.Error(), "refused: path must be absolute or start with ~/") {
		t.Errorf("a NUL byte: err = %v", err)
	}
	for path, want := range cases {
		_, err := runScript(t, readScript(path), RunOptions{Home: home})
		if err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("read %q: err = %v, want %q", path, err, want)
		}
		if err != nil && strings.Contains(err.Error(), home) {
			t.Errorf("read %q: the error names the path: %v", path, err)
		}
	}
}

func TestRunReadsAtMostSixteenFiles(t *testing.T) {
	cleanEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runScript(t, `function usage()
		for i = 1, 17 do
			local text, err = omo.read_file("~/f")
			if not text then error(i .. ": " .. err) end
		end
	end`, RunOptions{Home: home})
	if err == nil || !strings.HasSuffix(err.Error(), "17: too many file reads") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunReadFileUsesInjectedReader(t *testing.T) {
	cleanEnv(t)
	var asked []string
	read := func(path string, max int64) ([]byte, error) {
		asked = append(asked, path)
		if max != MaxFile {
			t.Errorf("max = %d", max)
		}
		return []byte("abc"), nil
	}
	home := t.TempDir()
	if _, err := runScript(t, readScript("~/x/../y.json"), RunOptions{Home: home, ReadFile: read}); err == nil || !strings.HasSuffix(err.Error(), "read 3 bytes") {
		t.Fatalf("err = %v", err)
	}
	if want := []string{filepath.Join(home, "y.json")}; !slices.Equal(asked, want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
}

func TestEnvRunsNoCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	cleanEnv(t)
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	agentEnv := map[string]string{
		"COUNTED":    "`echo x >> " + count + "; echo counted-value`",
		"PLAIN":      "plain-value",
		"REFERS":     "$HOME/x",
		"BRACED":     "${HOME}",
		"CODEX_HOME": "/general/codex",
	}
	job := func(text string, accountDir string) Job {
		return Job{Script: "t", Text: text, AgentEnv: agentEnv,
			Profile: api.Profile{Name: "p", CLI: "codex", AccountDir: accountDir, Env: map[string]string{"LITERAL": "`not run`"}}}
	}
	o := RunOptions{Home: dir, HTTPClient: noNetwork(t)}

	text := `function usage()
		omo.log(omo.env("COUNTED") == nil and "left out" or "expanded")
		omo.log(omo.env("PLAIN") == "plain-value" and "plain" or "no plain")
		omo.log((omo.env("REFERS") == nil and omo.env("BRACED") == nil) and "left out" or "expanded")
		omo.log(omo.env("CODEX_HOME") == "/accounts/one" and "account" or "general")
		omo.log(omo.env("LITERAL") == "` + "`not run`" + `" and "literal" or "expanded")
		omo.log(omo.env("NO_SUCH_VARIABLE_HERE") == nil and "nil" or "set")
		return {}
	end`
	// Every run, retry, refresh and Test: the command never runs.
	for i := 0; i < 3; i++ {
		res, err := Run(context.Background(), job(text, "/accounts/one"), o)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"left out", "plain", "left out", "account", "literal", "nil"}; !slices.Equal(res.Log, want) {
			t.Fatalf("log = %q, want %q", res.Log, want)
		}
	}
	// Without an account folder the general environment's literal value
	// names the folder.
	res, err := Run(context.Background(), job(`function usage() omo.log(omo.env("CODEX_HOME") == "/general/codex" and "general" or "other") return {} end`, ""), o)
	if err != nil || !slices.Equal(res.Log, []string{"general"}) {
		t.Fatalf("log = %q, err = %v", res.Log, err)
	}
	if _, err := os.Stat(count); err == nil {
		t.Fatal("a command of the agent environment ran for a usage run")
	}

	// A refresh of Codex gets the environment an agent of the profile
	// gets, so the command runs then, and only then.
	var env []string
	o.RefreshCodex = func(ctx context.Context, _ string, e []string, _ string) error {
		env = e
		return nil
	}
	if _, err := Run(context.Background(), job(`function usage() omo.env("PLAIN") omo.codex_refresh("~/auth.json") return {} end`, "/accounts/one"), o); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(count)
	if string(raw) != "x\n" {
		t.Fatalf("the command ran %d times for the refresh, want once", strings.Count(string(raw), "x"))
	}
	if !slices.Contains(env, "COUNTED=counted-value") || !slices.Contains(env, "CODEX_HOME=/accounts/one") {
		t.Fatalf("refresh env = %q", env)
	}
}

func TestEnvProfileWinsOverAccountFolder(t *testing.T) {
	cleanEnv(t)
	j := Job{Script: "t", Text: `function usage() omo.log(omo.env("CLAUDE_CONFIG_DIR")) return {} end`,
		Profile: api.Profile{CLI: "claude", AccountDir: "/acct", Env: map[string]string{"CLAUDE_CONFIG_DIR": "/from-profile"}}}
	res, err := Run(context.Background(), j, RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)})
	if err != nil {
		t.Fatal(err)
	}
	// The value is a secret of the run, so the log shows it redacted: what
	// counts is which layer won, checked through the length.
	if len(res.Log) != 1 || res.Log[0] != "[redacted]" {
		t.Fatalf("log = %q", res.Log)
	}
	j.Text = `function usage() error(omo.env("CLAUDE_CONFIG_DIR") == "/from-profile" and "profile" or "account") end`
	_, err = Run(context.Background(), j, RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)})
	if err == nil || err.Error() != "line 1: profile" {
		t.Fatalf("err = %v", err)
	}
}

func TestKeychainArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	cleanEnv(t)
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "security")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argvFile + "; done\nprintf 'keychain-secret-value\\n'\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	old := securityPath
	securityPath = fake
	t.Cleanup(func() { securityPath = old })
	setGOOS(t, "darwin")

	text := `function usage()
		local s, err = omo.keychain(SERVICE)
		if not s then error("refused: " .. err) end
		error(s == "keychain-secret-value" and "got it" or "wrong")
	end`
	run := func(service string) string {
		_, err := runScript(t, strings.Replace(text, "SERVICE", luaQuote(service), 1), RunOptions{})
		return err.Error()
	}
	if got := run("Claude Code-credentials"); got != "line 4: got it" {
		t.Fatalf("err = %q", got)
	}
	raw, _ := os.ReadFile(argvFile)
	if want := "find-generic-password\n-s\nClaude Code-credentials\n-w\n"; string(raw) != want {
		t.Fatalf("argv = %q, want %q", raw, want)
	}
	os.Remove(argvFile)
	for _, bad := range []string{"-x", "", "a\nb", "a‮b", strings.Repeat("s", 201)} {
		if got := run(bad); got != "line 3: refused: keychain service name not allowed" {
			t.Errorf("service %q: err = %q", bad, got)
		}
	}
	if _, err := os.Stat(argvFile); err == nil {
		t.Fatal("a refused service name reached the keychain tool")
	}

	setGOOS(t, "linux")
	if got := run("Claude Code-credentials"); got != "line 3: refused: not available" {
		t.Fatalf("on linux: err = %q", got)
	}
	if _, err := os.Stat(argvFile); err == nil {
		t.Fatal("the keychain tool ran on linux")
	}
}

func TestCodexRefreshOnlyForCodex(t *testing.T) {
	cleanEnv(t)
	type call struct {
		command, path string
		env           []string
	}
	var calls []call
	refresh := func(ctx context.Context, command string, env []string, path string) error {
		calls = append(calls, call{command, path, env})
		return nil
	}
	text := `function usage()
		local ok, err = omo.codex_refresh("~/.codex/auth.json")
		local again, err2 = omo.codex_refresh("~/.codex/auth.json")
		error(tostring(ok) .. "|" .. tostring(err) .. "|" .. tostring(again) .. "|" .. tostring(err2))
	end`
	home := t.TempDir()
	o := RunOptions{Home: home, HTTPClient: noNetwork(t), RefreshCodex: refresh}

	for _, cli := range []string{"claude", "gemini", "custom"} {
		j := Job{Script: "t", Text: text, Profile: api.Profile{CLI: cli, Command: "/bin/" + cli}}
		_, err := Run(context.Background(), j, o)
		want := "line 4: nil|codex_refresh is only available for codex profiles|nil|codex_refresh is only available for codex profiles"
		if err == nil || err.Error() != want {
			t.Fatalf("%s: err = %v", cli, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("refresh ran for a profile that is not codex: %+v", calls)
	}

	j := Job{Script: "t", Text: text, Profile: api.Profile{CLI: "codex", Command: "/opt/codex-cli", AccountDir: "/accounts/c"}}
	_, err := Run(context.Background(), j, o)
	want := "line 4: true|nil|nil|codex_refresh runs at most once per run"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if len(calls) != 1 || calls[0].command != "/opt/codex-cli" || calls[0].path != filepath.Join(home, ".codex", "auth.json") {
		t.Fatalf("calls = %+v", calls)
	}
	if !slices.Contains(calls[0].env, "CODEX_HOME=/accounts/c") {
		t.Fatalf("env = %q, want CODEX_HOME of the account folder", calls[0].env)
	}
}

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name, text, err string
	}{
		{"fine", "local x = 1\nfunction usage(a) return {} end", ""},
		{"syntax", "function usage()\nx = = 1\nend", "line 2: "},
		{"no usage", "function other() end", "the script does not define a function usage"},
		{"usage not a function", "usage = 5", "the script does not define a function usage"},
		{"top level http", "omo.http({url = 'https://example.com'})\nfunction usage() end", "line 1: omo.http cannot be called at the top level"},
		{"top level read swallowed", "pcall(omo.read_file, '/etc/hostname')\nfunction usage() end", "the top level of the script calls a helper"},
		{"top level env", "local v = omo.env('HOME')\nfunction usage() end", "omo.env cannot be called"},
		{"endless top level", "while true do end", "time limit reached"},
		{"too large", "function usage() end\n--" + strings.Repeat("x", MaxScript), "the script exceeds 65536 bytes"},
		{"error at top level", "error('boom')", "line 1: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Check("t", tc.text)
			if tc.err == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("err = %v, want %q", err, tc.err)
			}
		})
	}
}
