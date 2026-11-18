package usage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// fakeAPI answers every request of an injected client in process: nothing
// reaches the network. It records the hosts asked.
type fakeAPI struct {
	mu      sync.Mutex
	hosts   []string
	handler http.HandlerFunc
}

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.hosts = append(f.hosts, r.URL.Host)
	h := f.handler
	f.mu.Unlock()
	rec := httptest.NewRecorder()
	if h == nil {
		rec.WriteHeader(http.StatusNotFound)
	} else {
		h(rec, r)
	}
	return rec.Result(), nil
}

func (f *fakeAPI) client() *http.Client { return &http.Client{Transport: f} }

func (f *fakeAPI) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hosts...)
}

// noNetwork is a client that fails every request.
func noNetwork(t *testing.T) *http.Client {
	return (&fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Host)
		w.WriteHeader(http.StatusTeapot)
	}}).client()
}

// cleanEnv removes the account variables of the developer's environment, so
// a test never reads a real credential folder.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// setGOOS makes scripts and the keychain believe they run on goosName.
func setGOOS(t *testing.T, goosName string) {
	old := goos
	goos = goosName
	t.Cleanup(func() { goos = old })
}

func runScript(t *testing.T, text string, o RunOptions) (Result, error) {
	t.Helper()
	if o.HTTPClient == nil {
		o.HTTPClient = noNetwork(t)
	}
	if o.Home == "" {
		o.Home = t.TempDir()
	}
	j := Job{Script: "test", Text: text, Profile: api.Profile{Name: "p", CLI: "custom", Command: "custom"}}
	return Run(context.Background(), j, o)
}

func TestRunResultValidation(t *testing.T) {
	cleanEnv(t)
	many := "function usage() local t = {} for i = 1, 33 do t[i] = {name = 'w' .. i, used = 1} end return t end"
	for _, tc := range []struct {
		name, body string
		err        string
		want       []Window
	}{
		{"not a list", `return {name = "a", used = 1}`, "usage must return a list of windows", nil},
		{"nil", `return nil`, "usage must return a list of windows", nil},
		{"a string", `return "x"`, "usage must return a list of windows", nil},
		{"item not a table", `return {"a"}`, "window 1: must be a table", nil},
		{"duplicate", `return {{name = "a", used = 1}, {name = "a", used = 2}}`, "window 2: the name is used twice", nil},
		{"bad name", `return {{name = "Session", used = 1}}`, "window 1: the name must be", nil},
		{"long name", `return {{name = string.rep("a", 41), used = 1}}`, "window 1: the name must be", nil},
		{"name not text", `return {{name = 7, used = 1}}`, "window 1: the name must be", nil},
		{"unknown field", `return {{name = "a", used = 1, extra = true}}`, "window 1: has a field other than", nil},
		{"used missing", `return {{name = "a"}}`, "window 1: used must be a number", nil},
		{"used negative", `return {{name = "a", used = -1}}`, "window 1: used must be a number", nil},
		{"used NaN", `return {{name = "a", used = 0/0}}`, "window 1: used must be a number", nil},
		{"used infinite", `return {{name = "a", used = 1/0}}`, "window 1: used must be a number", nil},
		{"used text", `return {{name = "a", used = "5"}}`, "window 1: used must be a number", nil},
		{"bad reset text", `return {{name = "a", used = 1, resets_at = "tomorrow"}}`, "window 1: resets_at must be", nil},
		{"bad reset number", `return {{name = "a", used = 1, resets_at = -5}}`, "window 1: resets_at must be", nil},
		{"bad reset kind", `return {{name = "a", used = 1, resets_at = {}}}`, "window 1: resets_at must be", nil},
		{"label not text", `return {{name = "a", used = 1, label = 5}}`, "window 1: the label must be text", nil},
		{"label", `return {{name = "a", used = 1, label = "Weekly, Fable"}}`, "", []Window{{Name: "a", UsedPercent: 1, Label: "Weekly, Fable"}}},
		{"label on one line", `return {{name = "a", used = 1, label = "  Weekly,\n\tall\27[31m \226\128\174models\r "}}`, "", []Window{{Name: "a", UsedPercent: 1, Label: "Weekly, all [31m models"}}},
		{"label cut", `return {{name = "a", used = 1, label = string.rep("é", 70)}}`, "", []Window{{Name: "a", UsedPercent: 1, Label: strings.Repeat("é", 60)}}},
		{"label empty", `return {{name = "a", used = 1, label = " \n "}}`, "", []Window{{Name: "a", UsedPercent: 1}}},
		{"label invalid UTF-8", `return {{name = "a", used = 1, label = "a\255b"}}`, "", []Window{{Name: "a", UsedPercent: 1, Label: "a\uFFFDb"}}},
		{"empty list", `return {}`, "", []Window{}},
		{"clamped and resets", `return {
			{name = "session", used = 120, resets_at = "2026-11-15T18:00:00Z"},
			{name = "weekly", used = 12.5, resets_at = 1800000000},
			{name = "x_1-y", used = 0}}`, "", []Window{
			{Name: "session", UsedPercent: 100, ResetsAt: time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)},
			{Name: "weekly", UsedPercent: 12.5, ResetsAt: time.Unix(1800000000, 0).UTC()},
			{Name: "x_1-y", UsedPercent: 0},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := runScript(t, "function usage(account)\n"+tc.body+"\nend", RunOptions{})
			checkRun(t, res, err, tc.err, tc.want)
		})
	}
	t.Run("33 windows", func(t *testing.T) {
		_, err := runScript(t, many, RunOptions{})
		if err == nil || !strings.Contains(err.Error(), "more than 32 windows") {
			t.Fatalf("err = %v", err)
		}
	})
}

func checkRun(t *testing.T, res Result, err error, wantErr string, want []Window) {
	t.Helper()
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("err = %v, want %q", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(res.Windows) != len(want) {
		t.Fatalf("windows = %+v, want %+v", res.Windows, want)
	}
	for i := range want {
		got := res.Windows[i]
		if got.Name != want[i].Name || got.Label != want[i].Label || got.UsedPercent != want[i].UsedPercent || !got.ResetsAt.Equal(want[i].ResetsAt) {
			t.Fatalf("window %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestRunAccountTable(t *testing.T) {
	cleanEnv(t)
	setGOOS(t, "linux")
	home := t.TempDir()
	j := Job{Script: "t", Text: `function usage(a)
		error(a.profile .. "|" .. a.cli .. "|" .. a.account_dir .. "|" .. a.home .. "|" .. a.os)
	end`, Profile: api.Profile{Name: "work", CLI: "claude", AccountDir: "/acct"}}
	_, err := Run(context.Background(), j, RunOptions{Home: home, HTTPClient: noNetwork(t)})
	want := "line 2: work|claude|/acct|" + home + "|linux"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestRunErrorsAreOneShortLine(t *testing.T) {
	cleanEnv(t)
	_, err := runScript(t, "function usage()\nerror(string.rep('x', 400) .. '\\nnext line')\nend", RunOptions{})
	if err == nil {
		t.Fatal("no error")
	}
	text := err.Error()
	if strings.ContainsAny(text, "\n\r") || len([]rune(text)) > 300 {
		t.Fatalf("error is not one short line: %q", text)
	}
	_, err = runScript(t, "function usage()\nx = = 1\nend", RunOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "line ") {
		t.Fatalf("syntax error = %v", err)
	}
	_, err = runScript(t, "x = 1", RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "no function usage") {
		t.Fatalf("missing usage = %v", err)
	}
}

func TestRunEndsWithItsContext(t *testing.T) {
	cleanEnv(t)
	for _, helper := range []bool{false, true} {
		name := "running script"
		if helper {
			name = "blocked helper"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			opts := RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)}
			j := Job{Script: "t", Text: `function usage() omo.read_file("/ready"); while true do end end`, Profile: api.Profile{CLI: "custom"}}
			opts.ReadFile = func(string, int64) ([]byte, error) {
				close(entered)
				return []byte("ready"), nil
			}
			if helper {
				j.Text = `function usage() local ok, err = omo.codex_refresh("/x/auth.json"); error(err) end`
				j.Profile = api.Profile{CLI: "codex", Command: "codex"}
				opts.RefreshCodex = func(ctx context.Context, _ string, _ []string, _ string) error {
					close(entered)
					<-ctx.Done()
					return ctx.Err()
				}
			}
			result := make(chan error, 1)
			go func() { _, err := Run(ctx, j, opts); result <- err }()
			<-entered
			cancel()
			err := <-result
			if err == nil || err.Error() != "run canceled" {
				t.Fatalf("run after cancellation: %v", err)
			}
		})
	}
}

func TestRunTimeout(t *testing.T) {
	cleanEnv(t)
	_, err := runScript(t, "function usage() while true do end end", RunOptions{Timeout: 200 * time.Millisecond})
	if err == nil || err.Error() != "time limit reached" {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRunFunc(t *testing.T) {
	cleanEnv(t)
	run := NewRunFunc(RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)})
	res, err := run(context.Background(), Job{Script: "t", Text: `function usage() omo.log("hi") return {{name = "a", used = 3}} end`})
	if err != nil || len(res.Windows) != 1 || len(res.Log) != 1 || res.Log[0] != "hi" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

// canary is a fixture token: it must never leave a run.
const (
	canaryToken   = "sk-ant-oat01-CANARY-0123456789abcdef"
	canaryAccount = "canary-account-0123456789"
)

func writeCanaryCredentials(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, ".credentials.json")
	body := `{"claudeAiOauth":{"accessToken":"` + canaryToken + `"},"account":"` + canaryAccount + `"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokensNeverInRunOutput(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	path := writeCanaryCredentials(t, dir)
	echo := &fakeAPI{handler: func(w http.ResponseWriter, r *http.Request) {
		// A server that echoes what it got, as an error page might.
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "you sent: "+r.Header.Get("Authorization")+" for "+r.Header.Get("X-Account"))
	}}
	prelude := `local function creds()
		local text = omo.read_file(` + luaQuote(path) + `)
		local c = omo.json_decode(text)
		return c.claudeAiOauth.accessToken, c.account, text
	end
	`
	cases := map[string]string{
		"error with the token":     `local tok = creds() error("bad token " .. tok)`,
		"error with the file":      `local _, _, text = creds() error(text)`,
		"error with a table value": `local tok = creds() error({tok})`,
		"log and print":            `local tok, acct = creds() omo.log(tok) print("x", tok, acct) omo.log(string.format("%s", acct)) return {}`,
		"token as window name":     `local tok = creds() return {{name = tok, used = 1}}`,
		"account as window name":   `local _, acct = creds() return {{name = acct, used = 1}}`,
		"token in a label":         `local tok = creds() return {{name = "a", used = 1, label = "Token " .. tok}}`,
		"account as label":         `local _, acct = creds() return {{name = "a", used = 1, label = acct}}`,
		"echoing server": `local tok, acct = creds()
			local res = omo.http({url = "https://api.anthropic.com/x", headers = {Authorization = "Bearer " .. tok, ["X-Account"] = acct}})
			omo.log(res.body)
			error(res.body)`,
		"token in a helper's argument": `local tok = creds() local _, err = omo.read_file("/" .. tok) omo.log(err) error(err .. tok)`,
		"keychain secret":              `local s = omo.keychain("Claude Code-credentials") omo.log(s) error(s)`,
		"env value":                    `local v = omo.env("CANARY_VALUE") omo.log(v) error(v)`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			setGOOS(t, "darwin")
			j := Job{Script: "t", Text: prelude + "function usage(account)\n" + body + "\nend",
				Profile:  api.Profile{Name: "p", CLI: "claude", Env: map[string]string{"CANARY_VALUE": canaryToken}},
				AgentEnv: map[string]string{}}
			res, err := Run(context.Background(), j, RunOptions{Home: dir, HTTPClient: echo.client(),
				Keychain: func(context.Context, string) (string, error) { return canaryToken, nil }})
			var all []string
			if err != nil {
				all = append(all, err.Error())
			}
			all = append(all, res.Log...)
			for _, w := range res.Windows {
				all = append(all, w.Name, w.Label)
			}
			for _, s := range all {
				if strings.Contains(s, canaryToken) || strings.Contains(s, canaryAccount) || strings.Contains(s, "CANARY") {
					t.Fatalf("a secret left the run: %q", s)
				}
			}
			if name == "echoing server" && (err == nil || !strings.HasSuffix(err.Error(), ": you sent: [redacted] for [redacted]")) {
				t.Fatalf("echo: err = %v", err)
			}
			if name == "token as window name" || name == "account as window name" || name == "token in a label" || name == "account as label" {
				if err == nil || len(res.Windows) != 0 {
					t.Fatalf("a window named by a secret was accepted: %+v %v", res.Windows, err)
				}
			}
		})
	}
}

// luaQuote quotes s as a Lua string literal.
func luaQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
